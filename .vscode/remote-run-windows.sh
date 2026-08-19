#!/bin/bash
# remote-run-windows.sh
# 在远程 Windows 机器上下载并运行 design-cloud-engine agent

set -e

# 加载环境变量
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

print_step() {
  echo -e "\n${COLOR_YELLOW}$1${COLOR_NC}"
}

print_info() {
  echo -e "${COLOR_BLUE}ℹ️  $1${COLOR_NC}"
}

print_error() {
  echo -e "${COLOR_RED}❌ $1${COLOR_NC}"
}

print_success() {
  echo -e "${COLOR_GREEN}✅ $1${COLOR_NC}"
}

print_separator
print_step "开始在 Windows 远程机器上部署 Agent"

# 检查 SSH 连接
print_info "检查 SSH 连接到 ${WIN_REMOTE_HOST}..."
if ! ssh -p "${SSH_PORT}" -o ConnectTimeout=5 -o BatchMode=yes "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" "exit" 2>/dev/null; then
  print_error "无法连接到 ${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}:${SSH_PORT}"
  exit 1
fi
print_success "SSH 连接成功"

# 构建远程路径
REMOTE_BINARY="${WIN_REMOTE_PATH}\\design-cloud-engine.exe"

print_info "创建远程目录..."
ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" "if (-not (Test-Path '${WIN_REMOTE_PATH}')) { New-Item -ItemType Directory -Path '${WIN_REMOTE_PATH}' -Force }"

print_info "从 Portal 服务下载 Windows agent..."
DOWNLOAD_URL="http://${REMOTE_HOST}:${PORTAL_PORT}/dce/design-cloud-engine.exe"
print_info "下载地址: ${DOWNLOAD_URL}"

ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" "Invoke-WebRequest -Uri '${DOWNLOAD_URL}' -OutFile '${REMOTE_BINARY}'"

print_success "下载完成"

print_info "验证文件..."
ssh -p "${SSH_PORT}" "${WIN_REMOTE_USER}@${WIN_REMOTE_HOST}" "if (Test-Path '${REMOTE_BINARY}') { Write-Host '文件存在' } else { Write-Host '文件不存在'; exit 1 }"

print_success "Windows Agent 部署完成"
print_separator
