#!/bin/bash
# Remote Debug - 本地编译 → 推送到远程 → 远程启动 dlv 调试器
# 底层复用 common.sh 的编译/清理/上传步骤

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"
print_separator
echo -e "${COLOR_BLUE}启动远程 dlv 调试器${COLOR_NC}"
echo -e "${COLOR_BLUE}目标: ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_PATH}${COLOR_NC}"
print_separator

# Steps 1-4: 本地编译 → 清理 → 上传
deploy_engine_to_remote

# 若远程存在 dc-launcher，同步数据库 agent.launcherPath 配置
if ssh_remote "test -x '${REMOTE_PATH}/dc-launcher'" 2>/dev/null; then
  sync_remote_launcher_path "${REMOTE_PATH}/dc-launcher"
fi

# Step 5: 确保远程 dlv 存在并启动
print_step "[5/5] 确保远程 dlv 并启动..."

if ! ensure_remote_dlv; then
  exit 1
fi

# 启动 dlv（--accept-multiclient + --continue 共存：engine 立即启动运行）
start_dlv_debug ${REMOTE_ENGINE_ARGS}

# 等待 dlv 启动并监听（最多 15 秒）
if ! wait_dlv_ready 15; then
  print_error "dlv 启动超时 (15s)"
  echo "    → 远程 dlv 日志 (${REMOTE_DLV_LOG}) 末尾："
  ssh_remote "tail -30 ${REMOTE_DLV_LOG}" 2>/dev/null || true
  exit 1
fi
print_success "dlv 已启动并监听端口 0.0.0.0:${REMOTE_DEBUG_PORT}"

echo ""
print_separator
print_success "远程 dlv 已启动！"
print_separator
print_info "接下来："
echo -e "  • 如需通过 SSH 隧道连接，请运行 create-tunnel 任务"
echo -e "  • 或直接连接 ${REMOTE_HOST}:${REMOTE_DEBUG_PORT}（内网）"
print_info "查看日志："
echo -e "  • ssh -p ${SSH_PORT} ${REMOTE_USER}@${REMOTE_HOST} 'tail -f ${REMOTE_DLV_LOG}'"
echo ""
