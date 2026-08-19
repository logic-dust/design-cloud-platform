#!/bin/bash
# Remote Run - 本地编译 → 推送到远程 → 远程直接运行 engine（不含 dlv）
# 底层复用 common.sh 的编译/清理/上传步骤

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"

print_separator
echo -e "${COLOR_BLUE}远程运行 engine（生产模式）${COLOR_NC}"
echo -e "${COLOR_BLUE}目标: ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_PATH}${COLOR_NC}"
print_separator

# Steps 1-4: 本地编译（优化模式，不带 -N -l）→ 清理 → 上传
deploy_engine_to_remote opt

# 若远程存在 dc-launcher，同步数据库 agent.launcherPath 配置
if ssh_remote "test -x '${REMOTE_PATH}/dc-launcher'" 2>/dev/null; then
  sync_remote_launcher_path "${REMOTE_PATH}/dc-launcher"
fi

# Step 5: 远程直接启动 engine（不用 dlv）
print_step "[5/5] 远程启动 engine..."

# 用 setsid + & + < /dev/null 让 engine 脱离 ssh 会话
start_engine_remote ${REMOTE_ENGINE_ARGS}

# 等待 engine 启动（最多 10 秒）
if ! wait_for_remote "engine 启动" 10 "pgrep -f 'design-cloud-engine' | grep -vx \"\$\$\" | grep -q ."; then
  print_error "engine 启动超时 (10s)"
  echo "    → 远程日志末尾："
  ssh_remote "tail -20 /tmp/engine-remote.log" 2>/dev/null || true
  exit 1
fi
print_success "engine 已启动"

# 服务可用性 + 版本验证：循环探测 /api/version（engine 起 agent+server 需要时间），替代固定 sleep
MAX_API_WAIT=15
VERSION_JSON=""
for i in $(seq 1 "${MAX_API_WAIT}"); do
  VERSION_JSON="$(ssh_remote "curl -s http://localhost:${PORTAL_PORT}/api/version 2>/dev/null")"
  if echo "${VERSION_JSON}" | grep -q '"version"'; then
    break
  fi
  echo "    → 等待 Portal API 就绪... (${i}s/${MAX_API_WAIT}s)"
  sleep 1
done
if echo "${VERSION_JSON}" | grep -q '"version"'; then
  print_success "API 版本: ${VERSION_JSON}"
else
  print_error "服务未就绪或 /api/version 无响应: ${VERSION_JSON}"
  exit 1
fi

echo ""
print_separator
print_success "远程 engine 已启动！"
print_separator
print_info "查看日志："
echo -e "  • ssh -p ${SSH_PORT} ${REMOTE_USER}@${REMOTE_HOST} 'tail -f /tmp/engine-remote.log'"
print_info "停止运行："
echo -e "  • 运行 cleanup: remote-debug 任务"
echo ""
