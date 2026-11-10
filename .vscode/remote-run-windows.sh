#!/bin/bash
# 旁路：在远程 Windows 上部署 agent（非 push/build 主流程；直接 bash 本脚本）

set -e
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"

print_separator
print_step "Windows 远程部署 Agent → ${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}"

if ! ssh -p "${SSH_PORT}" -o ConnectTimeout=5 -o BatchMode=yes "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" "exit" 2>/dev/null; then
  print_error "无法连接 ${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}"
  exit 1
fi

REMOTE_BINARY="${WIN_REMOTE_PATH}\\dc-engine.exe"
DOWNLOAD_URL="http://${REMOTE_HOST}:${PORTAL_PORT}/dce/dc-engine.exe"

ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" \
  "if (-not (Test-Path '${WIN_REMOTE_PATH}')) { New-Item -ItemType Directory -Path '${WIN_REMOTE_PATH}' -Force }"
print_info "下载 ${DOWNLOAD_URL}"
ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" \
  "Invoke-WebRequest -Uri '${DOWNLOAD_URL}' -OutFile '${REMOTE_BINARY}'"
ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" \
  "if (-not (Test-Path '${REMOTE_BINARY}')) { exit 1 }"

print_success "Windows Agent 已下载: ${REMOTE_BINARY}"
print_separator
