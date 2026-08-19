#!/bin/bash
# Check Status - 检查远程调试状态（本地隧道、远程 dlv 进程、远程端口监听）

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"

print_separator
echo -e "${COLOR_BLUE}远程调试状态检查${COLOR_NC}"
echo -e "${COLOR_BLUE}目标: ${REMOTE_USER}@${REMOTE_HOST}${COLOR_NC}"
print_separator

# 1. 本地隧道状态
echo "=== 本地隧道状态 ==="
if ss -tuln 2>/dev/null | grep -q ":${LOCAL_PORT} "; then
  TUNNEL_PID="$(pgrep -f "ssh.*-L.*${LOCAL_PORT}" | head -1)"
  print_success "隧道运行中 (PID: ${TUNNEL_PID}, 端口 ${LOCAL_PORT})"
else
  print_info "无隧道"
fi

# 2. 远程 dlv 进程
echo ""
echo "=== 远程 dlv 进程 ==="
if ssh_remote "pgrep -fa 'dlv exec'" 2>/dev/null; then
  :
else
  print_info "未运行"
fi

# 3. 远程端口监听（CentOS 7 兼容：netstat 优先，回退 ss）
echo ""
echo "=== 远程端口监听 (${REMOTE_DEBUG_PORT}) ==="
if ssh_remote "(netstat -ltnp 2>/dev/null || ss -tlpn 2>/dev/null) | grep ':${REMOTE_DEBUG_PORT} '" 2>/dev/null; then
  :
else
  print_info "无监听"
fi
