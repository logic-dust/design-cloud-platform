#!/usr/bin/env bash
# systemd + FHS 下 Desktop 发布烟测（无 dlv）。与 .vscode/remote.sh 并存。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [[ -f "${REPO_ROOT}/.vscode/env.sh" ]]; then
  # shellcheck source=/dev/null
  source "${REPO_ROOT}/.vscode/env.sh"
else
  export REMOTE_USER="${REMOTE_USER:-hpcadmin}"
  export REMOTE_HOST="${REMOTE_HOST:-cent7.dev.vm}"
  export SSH_PORT="${SSH_PORT:-22}"
  export ROOT_USER="${ROOT_USER:-root}"
  export ENGINE_UNIT="${ENGINE_UNIT:-design-cloud-engine}"
  export PORTAL_PORT="${PORTAL_PORT:-1234}"
fi

LAB_SHARE_DATA="${LAB_SHARE_DATA:-/share/apps/design-cloud/data}"
DESKTOP_RELEASES_DIR="${DESKTOP_RELEASES_DIR:-${REPO_ROOT}/../design-cloud-desktop/Releases}"
RELEASES_JSON="${DESKTOP_RELEASES_DIR}/releases.win.json"

UNIT="${ENGINE_UNIT}"
REMOTE_PORTAL="http://${REMOTE_HOST}:${PORTAL_PORT}"

LAYOUT="fhs"
INSTALL_ENGINE=""
REMOTE_BUILD=0
STOP_DEV=1
DO_RESTORE=0
NO_RESTART=0
SMOKE_ONLY=0

usage() {
  cat <<'EOF'
用法: systemd-desktop-smoke.sh [选项]

  --layout fhs|symlink|config   数据布局（默认 fhs）
  --install-engine PATH         安装本地 dc-engine 到 /opt/design-cloud 后重启 unit
  --remote-build                rsync 源码并在远端 make（同 remote.sh build），装 engine+dcexec
  --restore-fhs                 恢复 FHS 默认（去掉 lab data_dir / 软链）
  --no-stop-dev                 不停 dlv / 手工 dc-engine
  --no-restart                  只落盘文件，不 restart unit
  --smoke-only                  仅 curl API（不改远端）
  -h, --help

与 .vscode/remote.sh（dlv）互补：本脚本在实验室主机上模拟 RPM/systemd 生产形态。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --layout) LAYOUT="$2"; shift 2 ;;
    --install-engine) INSTALL_ENGINE="$2"; shift 2 ;;
    --remote-build) REMOTE_BUILD=1; shift ;;
    --restore-fhs) DO_RESTORE=1; shift ;;
    --no-stop-dev) STOP_DEV=0; shift ;;
    --no-restart) NO_RESTART=1; shift ;;
    --smoke-only) SMOKE_ONLY=1; shift ;;
    -h | --help) usage; exit 0 ;;
    *) echo "未知参数: $1" >&2; usage >&2; exit 2 ;;
  esac
done

ssh_root() {
  ssh -p "${SSH_PORT}" "${ROOT_USER}@${REMOTE_HOST}" "$@"
}

require_releases_json() {
  if [[ ! -f "${RELEASES_JSON}" ]]; then
    echo "缺少 ${RELEASES_JSON}（可设 DESKTOP_RELEASES_DIR）" >&2
    exit 1
  fi
}

smoke_curl() {
  echo "→ GET ${REMOTE_PORTAL}/api/desktop/releases/latest?os=win"
  curl -sS -f "${REMOTE_PORTAL}/api/desktop/releases/latest?os=win" | head -c 400
  echo ""
  echo "→ GET ${REMOTE_PORTAL}/api/desktop/releases/installer"
  curl -sS -f "${REMOTE_PORTAL}/api/desktop/releases/installer" | head -c 400
  echo ""
}

asset_placeholders_from_json() {
  python3 - "${RELEASES_JSON}" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
for a in data.get("Assets", data.get("assets", [])):
    fn = a.get("FileName") or a.get("fileName")
    if fn:
        print(fn)
PY
  echo "DesignCloudDesktop-win-Setup.exe"
}

if [[ "${SMOKE_ONLY}" -eq 1 ]]; then
  smoke_curl
  exit 0
fi

require_releases_json
PLACEHOLDER_LIST="$(mktemp)"
trap 'rm -f "${PLACEHOLDER_LIST}"' EXIT
asset_placeholders_from_json | sort -u > "${PLACEHOLDER_LIST}"

if [[ "${REMOTE_BUILD}" -eq 1 ]]; then
  # shellcheck source=/dev/null
  source "${REPO_ROOT}/.vscode/common.sh"
  STAGING_ENGINE="${REMOTE_TMP}/dc-engine.$$"
  STAGING_EXEC_BIN="${REMOTE_TMP}/dc-exec.$$"
  require_ssh
  stage_build_artifacts
  ssh -p "${SSH_PORT}" "${REMOTE_USER}@${REMOTE_HOST}" \
    "cp -f '${REMOTE_SRC_DIR}/build/dc-engine-dev' /tmp/dc-engine-lab && cp -f '${REMOTE_SRC_DIR}/build/dc-exec-dev' /tmp/dc-exec-lab"
elif [[ -n "${INSTALL_ENGINE}" ]]; then
  [[ -f "${INSTALL_ENGINE}" ]] || { echo "二进制不存在: ${INSTALL_ENGINE}" >&2; exit 1; }
  scp -P "${SSH_PORT}" -C "${INSTALL_ENGINE}" "${ROOT_USER}@${REMOTE_HOST}:/tmp/dc-engine-lab"
fi

scp -P "${SSH_PORT}" -C "${RELEASES_JSON}" "${ROOT_USER}@${REMOTE_HOST}:/tmp/releases.win.json"
scp -P "${SSH_PORT}" -C "${PLACEHOLDER_LIST}" "${ROOT_USER}@${REMOTE_HOST}:/tmp/desktop-placeholders.txt"

ssh_root env \
  UNIT="${UNIT}" \
  LAYOUT="${LAYOUT}" \
  DO_RESTORE="${DO_RESTORE}" \
  STOP_DEV="${STOP_DEV}" \
  NO_RESTART="${NO_RESTART}" \
  LAB_SHARE_DATA="${LAB_SHARE_DATA}" \
  bash -s <<'REMOTE'
set -euo pipefail
CONFIG=/etc/design-cloud/config.toml
FHS_DATA=/var/lib/design-cloud/data
DESKTOP_REL=desktop
ENGINE=/opt/design-cloud/dc-engine
MARK='# --- lab: explicit data_dir (systemd-desktop-smoke'

mapfile -t FILES < /tmp/desktop-placeholders.txt

stop_dev_processes() {
  [[ "${STOP_DEV}" == "1" ]] || return 0
  systemctl stop "${UNIT}" 2>/dev/null || true
  pkill -u hpcadmin -f '/opt/dce/dc-engine' 2>/dev/null || true
  pkill -u hpcadmin -f 'dlv exec /opt/design-cloud/dc-engine' 2>/dev/null || true
  sleep 1
  if ss -tlnp 2>/dev/null | grep -q ':1234 '; then
    echo "WARN: 端口 1234 仍被占用，尝试 fuser -k"
    fuser -k 1234/tcp 2>/dev/null || true
    sleep 1
  fi
}

strip_lab_data_dir() {
  local tmp
  tmp="$(mktemp)"
  awk -v b="${MARK}" '
    index($0, b) == 1 { skip=1; next }
    skip && /^data_dir = / { next }
    skip && /^$/ { skip=0; next }
    { print }
  ' "${CONFIG}" > "${tmp}"
  mv "${tmp}" "${CONFIG}"
  chown design-cloud:design-cloud "${CONFIG}"
  chmod 0600 "${CONFIG}"
}

insert_lab_data_dir() {
  strip_lab_data_dir
  local tmp
  tmp="$(mktemp)"
  awk -v mark="${MARK}" -v line="data_dir = '${LAB_SHARE_DATA}'" '
    BEGIN { done=0 }
    /^\[log\]/ && !done { print mark " test; remove via --restore-fhs)"; print line; print ""; done=1 }
    { print }
  ' "${CONFIG}" > "${tmp}"
  mv "${tmp}" "${CONFIG}"
  chown design-cloud:design-cloud "${CONFIG}"
  chmod 0600 "${CONFIG}"
}

deploy_desktop_tree() {
  local root="$1"
  local desk="${root}/${DESKTOP_REL}"
  install -d -m 0750 -o design-cloud -g design-cloud "${desk}/plugins"
  install -m 0644 -o design-cloud -g design-cloud /tmp/releases.win.json "${desk}/releases.win.json"
  local f
  for f in "${FILES[@]}"; do
    [[ -n "${f}" ]] || continue
    touch "${desk}/${f}"
    chown design-cloud:design-cloud "${desk}/${f}"
  done
  chown -R design-cloud:design-cloud "${root}" 2>/dev/null || true
  ls -la "${desk}"
}

apply_layout_fhs() {
  strip_lab_data_dir
  [[ -L "${FHS_DATA}" ]] && rm -f "${FHS_DATA}"
  install -d -m 0750 -o design-cloud -g design-cloud "${FHS_DATA}"
  deploy_desktop_tree "${FHS_DATA}"
}

apply_layout_symlink() {
  strip_lab_data_dir
  install -d -m 0750 -o design-cloud -g design-cloud "${LAB_SHARE_DATA}"
  deploy_desktop_tree "${LAB_SHARE_DATA}"
  if [[ -d "${FHS_DATA}" && ! -L "${FHS_DATA}" ]]; then
    mv "${FHS_DATA}" "${FHS_DATA}.bak-smoke-$(date +%s)"
  fi
  ln -sfn "${LAB_SHARE_DATA}" "${FHS_DATA}"
  ls -la "${FHS_DATA}"
}

apply_layout_config() {
  install -d -m 0750 -o design-cloud -g design-cloud "${LAB_SHARE_DATA}"
  deploy_desktop_tree "${LAB_SHARE_DATA}"
  [[ -L "${FHS_DATA}" ]] && rm -f "${FHS_DATA}"
  install -d -m 0750 -o design-cloud -g design-cloud "${FHS_DATA}"
  insert_lab_data_dir
}

restore_fhs() {
  strip_lab_data_dir
  [[ -L "${FHS_DATA}" ]] && rm -f "${FHS_DATA}"
  for bak in "${FHS_DATA}.bak-smoke-"*; do
    [[ -d "${bak}" ]] || continue
    rm -rf "${FHS_DATA}"
    mv "${bak}" "${FHS_DATA}"
    break
  done
  install -d -m 0750 -o design-cloud -g design-cloud "${FHS_DATA}/${DESKTOP_REL}/plugins"
}

if [[ -f /tmp/dc-engine-lab ]]; then
  install -m 0755 /tmp/dc-engine-lab "${ENGINE}"
  chown design-cloud:design-cloud "${ENGINE}"
  rm -f /tmp/dc-engine-lab
fi
if [[ -f /tmp/dc-exec-lab ]]; then
  install -m 0700 /tmp/dc-exec-lab /opt/design-cloud/dc-exec
  chown design-cloud:design-cloud /opt/design-cloud/dc-exec
  setcap -r /opt/design-cloud/dc-exec 2>/dev/null || true
  setcap cap_setuid,cap_setgid+ep /opt/design-cloud/dc-exec
  rm -f /tmp/dc-exec-lab
fi

stop_dev_processes

if [[ "${DO_RESTORE}" -eq 1 ]]; then
  restore_fhs
  LAYOUT=fhs
  deploy_desktop_tree "${FHS_DATA}"
else
  case "${LAYOUT}" in
    fhs) apply_layout_fhs ;;
    symlink) apply_layout_symlink ;;
    config) apply_layout_config ;;
    *) echo "未知 layout: ${LAYOUT}" >&2; exit 2 ;;
  esac
fi

"${ENGINE}" check-config --config "${CONFIG}"
if [[ "${NO_RESTART}" -eq 1 ]]; then
  exit 0
fi
systemctl start "${UNIT}" 2>/dev/null || true
systemctl restart "${UNIT}"
sleep 3
systemctl is-active --quiet "${UNIT}"
echo "systemd ${UNIT}: active"
REMOTE

echo ""
smoke_curl
echo "✅ systemd desktop smoke 完成 (layout=${LAYOUT})"
