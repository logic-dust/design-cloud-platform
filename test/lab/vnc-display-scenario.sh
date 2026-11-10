#!/usr/bin/env bash
# 实验室 display 选号：保留节点上已有 Xvnc，验证 Launch 选号不与占用冲突。
#
# 不主动 vncserver -kill；默认也不删 connection 行（避免 Delete 远程杀进程）。
# 若需空配额可设 CLEANUP_CONNECTIONS=1（仍不杀未受管 Xvnc）。
#
# 用法: bash test/lab/vnc-display-scenario.sh
# 可选: ADDR=... API_USER=hpcadmin API_PASS=... TEST_NODE=agent7-1.dev.vm
set -euo pipefail

ADDR="${ADDR:-http://cent7.dev.vm:1234}"
API_USER="${API_USER:-hpcadmin}"
API_PASS="${API_PASS:-pwd_hpcadmin}"
TEST_NODE="${TEST_NODE:-agent7-1.dev.vm}"
AGENT_SSH="${AGENT_SSH:-hpcadmin@${TEST_NODE}}"
CLEANUP_CONNECTIONS="${CLEANUP_CONNECTIONS:-0}"

log() { printf '[vnc-scenario] %s\n' "$*"; }
fail() { printf '[vnc-scenario] FAIL: %s\n' "$*" >&2; exit 1; }

api() {
  local method=$1 path=$2
  shift 2
  curl -s -w '\n__HTTP__%{http_code}' -X "$method" "${ADDR}${path}" \
    -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' "$@"
}

login() {
  TOKEN=$(curl -s -X POST "${ADDR}/api/sessions" -H 'Content-Type: application/json' \
    -d "{\"username\":\"${API_USER}\",\"password\":\"${API_PASS}\",\"client\":{\"hostname\":\"vnc-scenario\",\"platform\":\"linux\",\"osVersion\":\"5.15\",\"appVersion\":\"1\",\"deviceId\":\"vnc-scenario\"}}" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['token'])")
}

parse_body() { sed '/^__HTTP__/d' "$1"; }
parse_code() { grep '^__HTTP__' "$1" | sed 's/__HTTP__//'; }

ensure_resource() {
  local name=$1 display=$2
  local tmp=$(mktemp)
  api GET "/api/admin/system/connections/resources" >"$tmp"
  local id
  id=$(parse_body "$tmp" | python3 -c "
import sys,json
name=sys.argv[1]
data=json.load(sys.stdin).get('data') or {}
items=data.get('items') if isinstance(data,dict) else data
if items is None: items=[]
for r in items:
    if r.get('name')==name: print(r['id']); break
" "$name" 2>/dev/null || true)
  if [ -n "$id" ]; then
    log "reuse resource $name id=$id" >&2
    echo "$id"
    return
  fi
  local body
  if [ "$display" = "null" ]; then
    body="{\"name\":\"${name}\",\"protocol\":\"VNC\",\"nodes\":[\"${TEST_NODE}\"],\"maxPerUser\":4,\"public\":true}"
  else
    body="{\"name\":\"${name}\",\"protocol\":\"VNC\",\"nodes\":[\"${TEST_NODE}\"],\"display\":${display},\"maxPerUser\":4,\"public\":true}"
  fi
  api POST "/api/admin/system/connections/resources" -d "$body" >"$tmp"
  local code; code=$(parse_code "$tmp")
  if [ "$code" = "201" ]; then
    parse_body "$tmp" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['id'])"
    return
  fi
  if [ "$code" = "409" ]; then
    api GET "/api/admin/system/connections/resources" >"$tmp"
    id=$(parse_body "$tmp" | python3 -c "
import sys,json
name=sys.argv[1]
data=json.load(sys.stdin).get('data') or {}
items=data.get('items') if isinstance(data,dict) else data
if items is None: items=[]
for r in items:
    if r.get('name')==name: print(r['id']); break
" "$name")
    [ -n "$id" ] || fail "create $name 409 but not found in list"
    log "reuse resource $name id=$id (409)" >&2
    echo "$id"
    return
  fi
  fail "create $name HTTP $code: $(parse_body "$tmp")"
}

launch_to() {
  local rid=$1 out_body=$2
  local tmp=$(mktemp)
  api POST "/api/connections" -d "{\"resourceId\":${rid}}" >"$tmp"
  parse_code "$tmp" >"${out_body}.code"
  parse_body "$tmp" >"${out_body}.body"
  rm -f "$tmp"
}

live_displays_csv() {
  ssh -o BatchMode=yes "$AGENT_SSH" \
    "pgrep -a Xvnc 2>/dev/null | sed -n 's/.*Xvnc :\\([0-9]*\\).*/\\1/p' | sort -nu | tr '\\n' ',' | sed 's/,\$//'" \
    || true
}

list_x_displays() {
  ssh -o BatchMode=yes "$AGENT_SSH" 'echo -n "unix:"; ls /tmp/.X11-unix/ 2>/dev/null | tr "\n" " "; echo; pgrep -a Xvnc 2>/dev/null | sed -n "s/.*Xvnc :\([0-9]*\).*/proc:\1/p" | tr "\n" " "; echo'
}

cleanup_user_connections() {
  local tmp=$(mktemp)
  api GET "/api/connections" >"$tmp"
  local ids
  ids=$(parse_body "$tmp" | python3 -c "
import sys,json
ids=[str(c['id']) for c in (json.load(sys.stdin).get('data') or []) if c.get('nodeId')=='${TEST_NODE}']
print(','.join(ids))
")
  [ -z "$ids" ] && return 0
  api POST "/api/connections/batch-delete" -d "{\"ids\":[${ids}]}" >/dev/null || true
}

ensure_manual_display() {
  local d="${MANUAL_DISPLAY:-3}"
  if ssh -o BatchMode=yes "$AGENT_SSH" "pgrep -af 'Xvnc :${d} ' >/dev/null 2>&1"; then
    log "keeping existing manual/unmanaged Xvnc :${d}"
    return 0
  fi
  ssh -o BatchMode=yes "$AGENT_SSH" \
    "/opt/TurboVNC/bin/vncserver :${d} -geometry 800x600 -desktop dce-scenario-manual 2>&1" \
    || fail "manual vncserver :${d} failed"
  log "started manual Xvnc :${d} (no connection row)"
}

login
log "logged in as ${API_USER}"

BASELINE=$(live_displays_csv)
log "baseline Xvnc on ${TEST_NODE} (unchanged): ${BASELINE:-none}"

if [ "$CLEANUP_CONNECTIONS" = "1" ]; then
  cleanup_user_connections
  log "CLEANUP_CONNECTIONS=1: deleted connection rows on ${TEST_NODE} (does not kill unmanaged Xvnc)"
fi

R_FIX1=$(ensure_resource "scenario-fix-d1" 1)
R_FIX2=$(ensure_resource "scenario-fix-d2" 2)
R_RES3=$(ensure_resource "scenario-reserve-d3" 3)
R_DYN=$(ensure_resource "scenario-dynamic-pool" null)
log "resources: fix1=$R_FIX1 fix2=$R_FIX2 reserve3=$R_RES3 dynamic=$R_DYN" >&2

# 固定规格：成功则占号；若节点上已有旧 Xvnc 占同号 → 409 视为预期，不 fail
launch_to "$R_FIX1" /tmp/vnc-scenario-b1
code1=$(cat /tmp/vnc-scenario-b1.code)
if [ "$code1" = "201" ]; then
  D1=$(python3 -c "import json; c=json.load(open('/tmp/vnc-scenario-b1.body'))['data']; print(c['display'], c['port'], c['status'])")
  log "fixed :1 launch -> $D1"
elif [ "$code1" = "409" ]; then
  log "fixed :1 launch -> 409 (display already in use on node, OK with pre-existing X)"
else
  fail "fixed :1 unexpected HTTP $code1: $(cat /tmp/vnc-scenario-b1.body)"
fi

launch_to "$R_FIX2" /tmp/vnc-scenario-b2
code2=$(cat /tmp/vnc-scenario-b2.code)
if [ "$code2" = "201" ]; then
  D2=$(python3 -c "import json; c=json.load(open('/tmp/vnc-scenario-b2.body'))['data']; print(c['display'], c['port'], c['status'])")
  log "fixed :2 launch -> $D2"
elif [ "$code2" = "409" ]; then
  log "fixed :2 launch -> 409 (display conflict, OK with pre-existing X)"
else
  fail "fixed :2 unexpected HTTP $code2: $(cat /tmp/vnc-scenario-b2.body)"
fi

ensure_manual_display
LIVE_BEFORE=$(live_displays_csv)
log "live before dynamic: ${LIVE_BEFORE:-none}"
log "detail: $(list_x_displays)"

launch_to "$R_DYN" /tmp/vnc-scenario-b3
code3=$(cat /tmp/vnc-scenario-b3.code)
[ "$code3" = "201" ] || fail "dynamic launch HTTP $code3: $(cat /tmp/vnc-scenario-b3.body)"

CHOSEN=$(python3 -c "import json; print(json.load(open('/tmp/vnc-scenario-b3.body'))['data']['display'])")
log "dynamic launch -> display=$CHOSEN (Agent picked)"

export LIVE_BEFORE CHOSEN
python3 - <<'PY'
import os
live = {int(x) for x in os.environ.get("LIVE_BEFORE", "").split(",") if x.strip().isdigit()}
avoid_spec = {1, 2, 3}
taken = live | avoid_spec
chosen = int(os.environ["CHOSEN"])
if chosen in taken:
    raise SystemExit(f"FAIL: dynamic picked :{chosen} but taken={sorted(taken)}")
expect = min(d for d in range(1, 100) if d not in taken)
if chosen != expect:
    raise SystemExit(f"FAIL: dynamic picked :{chosen}, want min free :{expect} given taken={sorted(taken)}")
print(f"ASSERT OK: :{chosen} is min free above live∪avoid_spec (live={sorted(live)} avoid_spec={sorted(avoid_spec)})")
PY

log "after dynamic: $(list_x_displays)"
log "PASS — display pick avoids conflict with retained + scenario occupancy"
