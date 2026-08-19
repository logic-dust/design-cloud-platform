#!/bin/bash
# VSCode Remote Debug Environment Configuration
# This file contains all configurable variables used by debug scripts

# 脚本自身目录（被 source 时仍指向 env.sh 所在位置），用于解析不依赖 cwd 的路径
export SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

################################
# SSH & Remote Host Configuration
################################
export REMOTE_USER="hpcadmin"                 # SSH 远程用户名
export REMOTE_HOST="cent7.dev.vm"             # SSH 远程主机地址（Linux）
export SSH_PORT=22                            # SSH 端口（默认 22）
export REMOTE_PATH="/share/apps/design-cloud" # 远程运行工作目录

# Windows Agent Target Configuration
export WIN_REMOTE_USER="hpcadmin"             # Windows 远程用户名
export WIN_REMOTE_HOST="win1.dev.vm"          # Windows SSH 远程主机地址
export WIN_REMOTE_PATH="C:\\design-cloud"     # Windows 远程工作目录

################################
# Binary & Config Paths
################################
export LOCAL_BINARY="${SCRIPT_DIR}/../build/engine-debug"  # 本地编译输出路径（基于脚本目录，不依赖 cwd）
export REMOTE_BINARY="${REMOTE_PATH}/design-cloud-engine"  # 远程二进制文件位置
export LOCAL_CONFIG="${SCRIPT_DIR}/../config.dev.toml"     # 本地配置文件完整路径（基于脚本目录）
export REMOTE_CONFIG_FILE="${REMOTE_PATH}/config.dev.toml" # 远程配置文件完整路径

################################
# Debug Port Configuration
################################
export LOCAL_PORT=2345        # 本地 VSCode 连接的端口
export REMOTE_DEBUG_PORT=2345 # 远程 dlv 监听的端口
export PORTAL_PORT=1234       # engine Portal HTTP API 端口（与 config.dev.toml [portal].port 保持一致）

################################
# Remote Tools & Commands
################################
export REMOTE_DLV_CMD="${REMOTE_PATH}/dlv"                 # 上传到远程工作目录的静态 dlv
export LOCAL_DLV="${SCRIPT_DIR}/../build/dlv"              # 本地交叉编译产出的 dlv
export DLV_VERSION="v1.27.1"                             # 上传用的 dlv 版本（需支持 go1.27，固定避免漂移）
export REMOTE_ENGINE_ARGS="--config ${REMOTE_CONFIG_FILE}" # engine 启动参数

################################
# RPM 部署配置（deploy-launcher.sh 使用）
################################
export ROOT_USER="root"  # RPM 部署 SSH 用户
export REMOTE_TMP="/tmp" # 远程临时目录

################################
# Build Configuration
################################
# 编译已统一走 Makefile 的 engine-dev 目标（见 Makefile），
# 编译标志由 Makefile 变量控制：
#   ENGINE_OUT         输出路径（默认 build/design-cloud-engine-debug，脚本传 $LOCAL_BINARY）
#   ENGINE_GC_FLAGS    -gcflags，默认 all=-N -l（禁优化）；置空即优化编译
#   ENGINE_EXTRA_FLAGS 追加任意 go build 标志
export CGO_ENABLED=0  # 禁用 CGo（与 Makefile 保持一致）
export GOOS="linux"   # 目标操作系统
export GOARCH="amd64" # 目标架构

################################
# Logging & Output
################################
export REMOTE_DLV_LOG="/tmp/dlv-debug.log" # 远程 dlv 日志文件位置

################################
# Terminal Colors (可在脚本中复用)
################################
export COLOR_RED='\033[0;31m'
export COLOR_GREEN='\033[0;32m'
export COLOR_YELLOW='\033[1;33m'
export COLOR_BLUE='\033[0;34m'
export COLOR_NC='\033[0m' # No Color

################################
# Helper Functions (可在脚本中复用)
################################

# 打印成功消息
print_success() {
  echo -e "${COLOR_GREEN}✅ $1${COLOR_NC}"
}

# 打印错误消息
print_error() {
  echo -e "${COLOR_RED}❌ $1${COLOR_NC}"
}

# 打印警告消息
print_warning() {
  echo -e "${COLOR_YELLOW}⚠️  $1${COLOR_NC}"
}

# 打印信息消息
print_info() {
  echo -e "${COLOR_BLUE}ℹ️  $1${COLOR_NC}"
}

# 打印步骤标题
print_step() {
  echo -e "\n${COLOR_YELLOW}$1${COLOR_NC}"
}

# 打印分隔线
print_separator() {
  echo -e "${COLOR_BLUE}========================================${COLOR_NC}"
}

# 检查 SSH 连接
check_ssh_connection() {
  if ! ssh -p "${SSH_PORT}" -o ConnectTimeout=5 -o BatchMode=yes "${REMOTE_USER}@${REMOTE_HOST}" "exit" 2>/dev/null; then
    print_error "无法连接到 ${REMOTE_USER}@${REMOTE_HOST}:${SSH_PORT}，请检查 SSH 配置"
    return 1
  fi
  return 0
}
