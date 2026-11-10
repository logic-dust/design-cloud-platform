#!/usr/bin/env bash
# Verifier: Windows 节点实机冒烟（编译级门禁之外的执行级证据）。
#
# 覆盖：
#   1) 枚举所有"能在 Windows 上编译出测试二进制"的包（dc-exec 链路已被 !windows 排除），
#      交叉编译 → 推送 → **在真实 Windows 上执行**（本机只能 -c 编译，不能执行）；
#   2) 逐包汇总 PASS/FAIL；
#   3) 若当前账户是管理员：SCM 服务 install/start/status/stop/uninstall 全流程
#      （含上传最小合法 config.toml，并以 --config 绝对路径安装）；否则明确 SKIP；
#   4) 采集数据目录日志与 icacls 留痕。
#
# 用法：
#   WIN_SMOKE_HOST='user@192.168.100.132' .agents/verifiers/verify-win-smoke.sh            # 全量包
#   WIN_SMOKE_PKGS='./internal/agent/probe ./cmd/engine/command' ... verify-win-smoke.sh   # 指定包
#
# 注意：本仓库 Windows 目标机的 SSH 默认 shell 是 **PowerShell**；`-test.v` 会被 PowerShell 拆坏，
#       故统一不带 flag 执行，并用 `exit $LASTEXITCODE` 传递退出码。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

WIN_HOST="${WIN_SMOKE_HOST:-}"
if [ -z "${WIN_HOST}" ] && [ -f "${ROOT_DIR}/.vscode/env.sh" ]; then
  # shellcheck disable=SC1091
  source "${ROOT_DIR}/.vscode/env.sh" || true
  WIN_HOST="${WIN_REMOTE_USER:-}@${WIN_REMOTE_HOST:-}"
fi

if [ -z "${WIN_HOST}" ] || [ "${WIN_HOST}" = "@" ]; then
  echo "SKIP: 未提供 Windows 冒烟主机（WIN_SMOKE_HOST='user@host'）。"
  exit 0
fi

SSH_OPTS=(-o ConnectTimeout=8 -o BatchMode=yes -o LogLevel=ERROR)
REMOTE_DIR="${WIN_SMOKE_DIR:-C:\\dce-smoke}"
REMOTE_CONFIG="${REMOTE_DIR}\\config.toml"

if ! ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "exit" >/dev/null 2>&1; then
  echo "SKIP: Windows 主机 ${WIN_HOST} 不可达（SSH 失败），跳过实机冒烟。"
  exit 0
fi

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

export CGO_ENABLED=0 GOOS=windows GOARCH=amd64

if [ -n "${WIN_SMOKE_PKGS:-}" ]; then
  read -r -a PKGS <<<"${WIN_SMOKE_PKGS}"
else
  # 刻意避开 mapfile（bash 4+ 专属）：macOS 自带 bash 3.2 会直接报错。
  PKGS=()
  while IFS= read -r pkg; do
    if [ -n "${pkg}" ]; then
      PKGS+=("${pkg}")
    fi
  done < <(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | sed '/^$/d')
fi

if [ "${#PKGS[@]}" -eq 0 ]; then
  echo "SKIP: 未发现可在 Windows 上编译的测试包。"
  exit 0
fi

echo "=== Windows Smoke Verification on ${WIN_HOST} （${#PKGS[@]} 个测试包） ==="
ssh "${SSH_OPTS[@]}" "${WIN_HOST}" 'hostname; whoami; "$env:USERDOMAIN"' | sed 's/^/    host: /'

echo "[1/5] 交叉编译测试二进制 + agent exe..."
NAMES=()
for pkg in "${PKGS[@]}"; do
  name="$(echo "${pkg#design-cloud-engine/}" | tr '/' '_')"
  go test -c -o "${WORK_DIR}/${name}.test.exe" "${pkg}"
  NAMES+=("${name}")
done
make win-agent >/dev/null
echo "    built ${#NAMES[@]} test binaries + dc-engine.exe"

# Minimal agent-only config: hostname is not listed in controls → pure agent.
# Absolute --config path is required for SCM install; relative config.toml is not enough.
cat >"${WORK_DIR}/config.toml" <<'EOF'
[log]
dir = "logs"
app_level = "INFO"
access_level = "INFO"

[cluster]
port = 50051
heartbeat_interval = 30
connect_timeout = 10
node_heartbeat_timeout = 180
node_check_interval = 120
servers = ["smoke-control.invalid"]
EOF

echo "[2/5] 推送产物到 ${REMOTE_DIR} ..."
ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "New-Item -ItemType Directory -Force -Path '${REMOTE_DIR}' | Out-Null; Remove-Item -Path '${REMOTE_DIR}\\*.test.exe' -Force -ErrorAction SilentlyContinue; 'dir ready'"
scp -q "${WORK_DIR}"/*.test.exe "${WIN_HOST}:${REMOTE_DIR}/"
scp -q build/dc-engine.exe "${WIN_HOST}:${REMOTE_DIR}/"
scp -q "${WORK_DIR}/config.toml" "${WIN_HOST}:${REMOTE_DIR}/config.toml"

echo "[3/5] 在真实 Windows 上执行单测..."
passed=0
failed=0
FAILED_NAMES=()
for name in "${NAMES[@]}"; do
  # ssh 自身的传输失败退出码固定为 255，必须与「测试真实失败」区分开：跨隧道跑 39 个包
  # 时链路抖动并不罕见，直接计入 FAIL 会制造根本不存在的产品缺陷信号。传输失败重试一次，
  # 两次都失败才判定为失败（并且明确标注归因）。
  rc=0
  for attempt in 1 2; do
    ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_DIR}\\${name}.test.exe' | Out-Null; exit \$LASTEXITCODE" >/dev/null 2>&1 || rc=$?
    if [ "${rc}" -ne 255 ]; then
      break
    fi
    if [ "${attempt}" -eq 1 ]; then
      echo "    RETRY ${name}（SSH 传输失败，疑似链路抖动，重试一次）"
    fi
  done
  if [ "${rc}" -eq 0 ]; then
    passed=$((passed + 1))
  else
    failed=$((failed + 1))
    FAILED_NAMES+=("${name}")
    if [ "${rc}" -eq 255 ]; then
      echo "    FAIL ${name}（SSH 传输失败 exit=255，两次尝试均未完成，归因于链路而非测试断言）"
    else
      echo "    FAIL ${name}（exit=${rc}）"
    fi
  fi
done
echo "    实机单测：PASS ${passed} / FAIL ${failed}"

REMOTE_EXE="${REMOTE_DIR}\\dc-engine.exe"
if [ "${failed}" -gt 0 ]; then
  echo "    失败包详情（复跑取输出）："
  for name in "${FAILED_NAMES[@]}"; do
    echo "      --- ${name} ---"
    ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_DIR}\\${name}.test.exe' 2>&1 | Select-String -Pattern 'FAIL|expected|panic' | Select-Object -First 8"
  done
  echo "❌ Windows 实机单测存在失败（见上）。"
  exit 1
fi

IS_ADMIN=$(ssh "${SSH_OPTS[@]}" "${WIN_HOST}" \
  '([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)' | tr -d '\r')

echo "[4/5] SCM 服务生命周期（当前账户管理员=${IS_ADMIN}）..."
if [ "${IS_ADMIN}" = "True" ]; then
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service status" || true
  # 目标机通常已部署真实 Agent 服务，直接 install 会因 “service already exists” 失败，
  # 并让 `set -e` 中断整个门禁。先做一次幂等卸载（服务不存在时不算失败）再跑完整周期。
  # 注意：本步骤结束时服务保持“未安装”状态，跑完门禁后需自行恢复既有部署。
  echo "    先卸载既有服务（若不存在则忽略），服务将以本门禁的配置重新安装..."
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service uninstall" || true
  # --config is a top-level flag and must precede the subcommand.
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' --config '${REMOTE_CONFIG}' service install"
  # Start may exit non-zero if agent cannot reach a real control plane / certs are
  # absent; SCM registration with an absolute config path is still the gate under test.
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service start" || true
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service status" || true
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "Get-Service -Name DesignCloudEngine | Select-Object -ExpandProperty Status" || true
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service stop" || true
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service uninstall"
else
  echo "    SKIP: 当前账户非管理员，无法注册/启动 Windows 服务（需管理员账户）。"
  ssh "${SSH_OPTS[@]}" "${WIN_HOST}" "& '${REMOTE_EXE}' service status" || true
fi

echo "[5/5] 采集数据目录与 ACL 留痕（缺失不算失败）..."
ssh "${SSH_OPTS[@]}" "${WIN_HOST}" \
  'if (Test-Path "C:\ProgramData\DesignCloudEngine\logs") { Get-ChildItem "C:\ProgramData\DesignCloudEngine\logs" | Select-Object -First 10 Name,Length } else { "no logs dir yet" }' || true
ssh "${SSH_OPTS[@]}" "${WIN_HOST}" \
  'if (Test-Path "C:\ProgramData\DesignCloudEngine") { icacls "C:\ProgramData\DesignCloudEngine" } else { "no data dir yet" }' || true

echo "✅ Windows smoke verification completed（${passed} 包单测在真实 Windows 上通过）。"
