#!/bin/bash
# 旁路：scp 本机构建的 dc-engine.exe 到远程 Windows（非 push/build 主流程）
#
# 前置：make win-agent（产出 build/dc-engine.exe）
# 目标：env.sh 中的 WIN_REMOTE_*（默认 10.1.1.101 / C:\design-cloud）
# 正式 Windows 引导请用 deployment/install-win-agent-configless.ps1。

set -e
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"

print_separator
print_step "Windows 远程部署 Agent → ${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}"

LOCAL_EXE="${SCRIPT_DIR}/../build/dc-engine.exe"
if [ ! -f "${LOCAL_EXE}" ]; then
  print_error "缺少 ${LOCAL_EXE}：先执行 make win-agent"
  exit 1
fi

if ! ssh -p "${SSH_PORT}" -o ConnectTimeout=5 -o BatchMode=yes "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" "exit" 2>/dev/null; then
  print_error "无法连接 ${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}"
  exit 1
fi

REMOTE_BINARY="${WIN_REMOTE_PATH}\\dc-engine.exe"
TS="$(date +%s)"
STAGING="C:/Users/${WIN_REMOTE_USER}/dc-engine.${TS}.exe"

ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" \
  "if (-not (Test-Path '${WIN_REMOTE_PATH}')) { New-Item -ItemType Directory -Path '${WIN_REMOTE_PATH}' -Force | Out-Null }"

print_info "上传 ${LOCAL_EXE} → ${STAGING}"
scp -P "${SSH_PORT}" "${LOCAL_EXE}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}:${STAGING}"

ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" \
  "Copy-Item -Path '${STAGING}' -Destination '${REMOTE_BINARY}' -Force; Remove-Item '${STAGING}' -Force -ErrorAction SilentlyContinue; if (-not (Test-Path '${REMOTE_BINARY}')) { exit 1 }"

print_success "Windows Agent 已安装: ${REMOTE_BINARY}"
print_info "若已注册 DesignCloudEngine 服务，请在 Windows 上 Stop/Start 或重跑 install-win-agent-configless.ps1 -SkipRegister"
print_separator
