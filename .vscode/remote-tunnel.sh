#!/bin/bash
# Remote Tunnel - 建立 SSH 隧道到远程 dlv 调试器
# 快速重连模式，用于连接到已运行的远程 engine 调试器

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"

print_separator
echo -e "${COLOR_BLUE}建立到远程 dlv 的 SSH 隧道（快速连接模式）${COLOR_NC}"
print_separator

# 检查远程 dlv 是否在运行（带重试机制）
print_step "检查远程 dlv 是否运行..."
MAX_RETRIES=10
RETRY_INTERVAL=1
RETRY_COUNT=0
DLV_RUNNING=0

while [ "${RETRY_COUNT}" -lt "${MAX_RETRIES}" ]; do
  # remote_has_process 会排除执行检查的 shell 自身，避免 pgrep 自匹配恒真
  if remote_has_process "dlv exec.*engine"; then
    print_success "远程 dlv 正在运行"
    DLV_RUNNING=1
    break
  fi

  RETRY_COUNT=$((RETRY_COUNT + 1))
  if [ "${RETRY_COUNT}" -lt "${MAX_RETRIES}" ]; then
    echo "    → 等待 dlv 启动... (${RETRY_COUNT}/${MAX_RETRIES})"
    sleep "${RETRY_INTERVAL}"
  fi
done

if [ "${DLV_RUNNING}" -eq 0 ]; then
  print_error "远程 dlv 未运行，请先运行 remote-debug 任务"
  exit 1
fi

# 检查本地隧道是否已存在：LOCAL_PORT 已在监听且 ssh -L 进程在运行 → 直接复用，不重建
print_step "检查本地 SSH 隧道状态..."
if ss -tuln 2>/dev/null | grep -q ":${LOCAL_PORT} " && pgrep -f "ssh.*-L.*${LOCAL_PORT}" >/dev/null 2>&1; then
  TUNNEL_PID=$(pgrep -f "ssh.*-L.*${LOCAL_PORT}" | head -1)
  print_success "SSH 隧道已存在，直接复用 (PID: ${TUNNEL_PID})"
  echo ""
  print_separator
  print_success "SSH 隧道已就绪！"
  print_separator
  exit 0
fi
print_info "未检测到已有隧道，将重新建立..."

# 清理旧隧道
print_step "检查并清理旧的 SSH 隧道..."
pkill -f "ssh.*-L.*${LOCAL_PORT}" 2>/dev/null || true
sleep 1
pkill -9 -f "ssh.*-L.*${LOCAL_PORT}" 2>/dev/null || true
sleep 1

# 建立新的 SSH 隧道
print_step "建立 SSH 隧道 (本地 ${LOCAL_PORT} → 远程 127.0.0.1:${REMOTE_DEBUG_PORT})..."
echo "    → 启动新的隧道..."
ssh -p "${SSH_PORT}" -n -f -L "${LOCAL_PORT}":127.0.0.1:"${REMOTE_DEBUG_PORT}" -N "${REMOTE_USER}@${REMOTE_HOST}" 2>/dev/null

sleep 1
if ss -tuln 2>/dev/null | grep -q ":${LOCAL_PORT} "; then
  TUNNEL_PID=$(pgrep -f "ssh.*-L.*${LOCAL_PORT}" | head -1)
  print_success "SSH 隧道建立成功 (PID: ${TUNNEL_PID})"
else
  print_warning "隧道已建立但端口未监听，继续..."
fi

echo ""
print_separator
print_success "SSH 隧道已建立！"
print_separator
print_info "接下来："
echo -e "  • VSCode 将连接到 127.0.0.1:${LOCAL_PORT}"
echo -e "  • 将附加到远程正在运行的 engine"
print_info "清理隧道："
echo -e "  • 按 Shift+F5 停止调试"
echo -e "  • 或运行 cleanup: remote-tunnel 任务"
echo ""
