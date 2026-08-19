#!/bin/bash
# Cleanup Tunnel - 仅清理本地 SSH 隧道（不影响远程 dlv/engine 进程）

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"

print_separator
echo -e "${COLOR_BLUE}清理本地 SSH 隧道${COLOR_NC}"
print_separator

if pgrep -f "ssh.*-L.*${LOCAL_PORT}" >/dev/null 2>&1; then
  pkill -f "ssh.*-L.*${LOCAL_PORT}" 2>/dev/null || true
  sleep 1
  pkill -9 -f "ssh.*-L.*${LOCAL_PORT}" 2>/dev/null || true
  print_success "本地隧道已清理 (端口 ${LOCAL_PORT})"
else
  print_warning "没有找到隧道进程"
fi
