#!/bin/bash
# VSCode 远程开发环境变量（两条流程共用）
#
# 只有两条入口，均安装到 /opt/dce 并以 hpcadmin 运行 engine：
#   push)  remote-push.sh   本地编译(dcexec+engine) → 上传安装 → dlv 启动
#   build) remote-build.sh  rsync 源码 → 远程编译 → 安装 → dlv 启动
#
# 身份与目录约定：
#   - SSH / rsync / 进程属主 / engine 运行用户 = hpcadmin（同一身份，无 runuser 间接层）
#   - /opt/dce 属主 = hpcadmin：engine 的 data/certs/logs 均落在同树，无需 /var/lib
#   - 运行配置 = /opt/dce/config.toml（hpcadmin 可读）
#   - root 仅用于：写 /opt、setcap、复制 /etc/design-cloud 密钥、停 systemd unit
#
# 本目录下的脚本即完整的开发部署与调试路径。

export SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

################################
# SSH 与主机
################################
export REMOTE_USER="hpcadmin"           # SSH / rsync / engine 运行用户
export REMOTE_HOST="cent7.dev.vm"
export SSH_PORT=22
export ROOT_USER="root"                 # 仅写 /opt、setcap、停 unit 时使用
export REMOTE_TMP="/tmp"

################################
# 安装前缀（/opt/dce）
################################
export REMOTE_PATH="/opt/dce"
export REMOTE_BINARY="${REMOTE_PATH}/dc-engine"
export REMOTE_EXEC_BIN="${REMOTE_PATH}/dc-exec"
export REMOTE_DLV_CMD="${REMOTE_PATH}/dlv"

# 运行配置（由本地 config.dev.toml 上传）
export REMOTE_CONFIG="${REMOTE_PATH}/config.toml"
export LOCAL_CONFIG="${SCRIPT_DIR}/../config.dev.toml"

# SECURITY-REVIEW: 不是生产密钥泄露。
# 本脚本只用于实验室主机（默认 cent7.dev.vm）。engine 以 hpcadmin 跑在 /opt/dce，
# 读不到属主为 design-cloud、模式 0600 的 /etc/design-cloud。安装时把 dbpwd/ldapbindpwd
# 复制到 /opt/dce 并改属主，配置里的相对路径才能用。不要把它当成把生产密钥交给无关用户。
export REMOTE_SECRET_SRC="/etc/design-cloud"
export REMOTE_SECRET_FILES="dbpwd ldapbindpwd"

# systemd 单元：与开发流程争用 1234/52255 端口，启动前必须停掉
export ENGINE_UNIT="design-cloud-engine"

################################
# 本地构建产物
################################
export LOCAL_ENGINE="${SCRIPT_DIR}/../build/dc-engine-dev"
export LOCAL_EXEC_BIN="${SCRIPT_DIR}/../build/dc-exec-dev"

# B：远程源码树（rsync 目标，不占 /opt）
export REMOTE_SRC_DIR="/home/${REMOTE_USER}/design-cloud-engine"

################################
# 调试端口
################################
export REMOTE_DEBUG_PORT=2345   # 远程 dlv 监听端口（launch.json 里 VSCode 连接的端口）
export PORTAL_PORT=1234         # 远程 Portal HTTP API 端口（以 check-config 解析值为准）

# dlv 绑定地址。0.0.0.0 = 本机直连 attach（历次版本的既有做法，无需隧道）；
# dlv 无认证且明文：受信内网一般无妨，换到不可信/共享网段时改为 127.0.0.1。
# 改 127.0.0.1 后 VSCode 需经隧道附加：
#   ssh -N -L 2345:127.0.0.1:2345 hpcadmin@cent7.dev.vm
export DLV_BIND="0.0.0.0"

################################
# dlv
################################
export LOCAL_DLV="${SCRIPT_DIR}/../build/dlv"
export DLV_VERSION="v1.27.1"

################################
# 日志
################################
export REMOTE_DLV_LOG="/tmp/dlv-debug.log"

################################
# Windows Agent（旁路脚本 remote-run-windows.sh 用）
################################
export WIN_REMOTE_USER="hpcadmin"
export WIN_REMOTE_HOST="10.1.1.101"
export WIN_REMOTE_PATH="C:\\design-cloud"

################################
# 打印
################################
export COLOR_RED='\033[0;31m'
export COLOR_GREEN='\033[0;32m'
export COLOR_YELLOW='\033[1;33m'
export COLOR_BLUE='\033[0;34m'
export COLOR_NC='\033[0m'

print_success() { echo -e "${COLOR_GREEN}✅ $1${COLOR_NC}"; }
print_error() { echo -e "${COLOR_RED}❌ $1${COLOR_NC}"; }
print_warning() { echo -e "${COLOR_YELLOW}⚠️  $1${COLOR_NC}"; }
print_info() { echo -e "${COLOR_BLUE}ℹ️  $1${COLOR_NC}"; }
print_step() { echo -e "\n${COLOR_YELLOW}$1${COLOR_NC}"; }
print_separator() { echo -e "${COLOR_BLUE}========================================${COLOR_NC}"; }

# 检查 SSH 连通性（失败时回显 ssh 原始报错，便于定位）
check_ssh_connection() {
  local ERR
  if ! ERR="$(ssh -p "${SSH_PORT}" -o ConnectTimeout=5 -o BatchMode=yes "${REMOTE_USER}@${REMOTE_HOST}" "exit" 2>&1)"; then
    print_error "无法连接到 ${REMOTE_USER}@${REMOTE_HOST}:${SSH_PORT}"
    [ -n "${ERR}" ] && echo "    → ssh: $(printf '%s' "${ERR}" | tail -1)"
    return 1
  fi
  return 0
}
