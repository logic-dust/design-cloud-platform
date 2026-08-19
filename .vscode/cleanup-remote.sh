#!/bin/bash
# Cleanup Remote - 清理完整调试环境：本地 SSH 隧道 + 远程 dlv/engine 进程
# 复用 common.sh 的 remote_kill_pattern（排除 shell 自身 PID，避免误杀清理命令自身）

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"

print_separator
echo -e "${COLOR_BLUE}清理远程调试环境${COLOR_NC}"
echo -e "${COLOR_BLUE}目标: ${REMOTE_USER}@${REMOTE_HOST}${COLOR_NC}"
print_separator

# 1. 清理本地 SSH 隧道
print_step "[1/3] 清理本地 SSH 隧道..."
if pgrep -f "ssh.*-L.*${LOCAL_PORT}" >/dev/null 2>&1; then
  pkill -f "ssh.*-L.*${LOCAL_PORT}" 2>/dev/null || true
  sleep 1
  pkill -9 -f "ssh.*-L.*${LOCAL_PORT}" 2>/dev/null || true
  print_success "本地隧道已清理 (端口 ${LOCAL_PORT})"
else
  print_info "无本地隧道"
fi

# 2. 停止远程 dlv（pgrep 收集 PID 再 kill，不用 pkill；排除远程 shell 自身）
print_step "[2/3] 停止远程 dlv..."
remote_kill_pattern "dlv exec.*engine" || true

# 3. 停止远程 engine
print_step "[3/3] 停止远程 engine..."
remote_kill_pattern "design-cloud-engine" || true
sleep 2
print_success "远程进程已清理"

echo ""
print_separator
print_success "清理完成！"
print_separator

