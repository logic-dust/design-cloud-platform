#!/bin/bash
# Remote Build & Debug - sync 代码到远程 → 远程编译 launcher+engine → 安装 → dlv debug engine
#
# 流程:
#   1. sync go 源码 + Makefile + 品牌资产源 到远程 ~/design-cloud-engine（rsync 增量，回退 scp 全量）
#   2. 远程编译 launcher-dev + engine-dev（保留调试符号，供 dlv）
#   3. 清理远程旧进程（kill 旧 engine/dlv，不删二进制）
#   4. 安装 launcher + engine 到 /share/apps/design-cloud
#   5. 确保远程 dlv（远程 go install，回退本地编译上传）
#   6. 启动 dlv debug engine（headless，监听调试端口）

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"

# 远程源码目录（hpcadmin home）
REMOTE_SRC_DIR="/home/hpcadmin/design-cloud-engine"
# 远程安装目录（复用现有运行目录）
REMOTE_INSTALL_DIR="${REMOTE_PATH}"
REMOTE_LAUNCHER="${REMOTE_INSTALL_DIR}/dc-launcher"

print_separator
echo -e "${COLOR_BLUE}远程编译 + 安装 + dlv 调试 engine${COLOR_NC}"
echo -e "${COLOR_BLUE}目标: ${REMOTE_USER}@${REMOTE_HOST}${COLOR_NC}"
print_separator

# Step 1: sync 代码到远程（go 文件 + Makefile + 品牌资产源，sync-assets 依赖）
print_step "[1/6] 同步 go 源码 + Makefile + 品牌资产到远程..."
sync_code() {
  if ! command -v rsync >/dev/null 2>&1; then
    print_error "本地未安装 rsync，无法增量同步。请先安装：sudo pacman -S rsync"
    exit 1
  fi
  print_info "使用 rsync 增量同步（只同步变化的文件）..."
  rsync -az --delete \
    --include='*/' \
    --include='*.go' \
    --include='go.mod' \
    --include='go.sum' \
    --include='Makefile' \
    --include='docs-site/static/images/logo.svg' \
    --include='docs-site/static/favicon.ico' \
    --include='internal/server/service/user/weakpasswords.txt' \
    --include='internal/server/portal/web/**' \
    --exclude='*' \
    -e "ssh -p ${SSH_PORT}" \
    "${SCRIPT_DIR}/../" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_SRC_DIR}/"
  print_success "代码已同步到 ${REMOTE_SRC_DIR}"
}
sync_code

# Step 2: 远程并行编译 launcher + engine（make -j2 并行构建两个独立目标，加快进度）
#   engine-dev 依赖 sync-assets，launcher-dev 独立，二者可并行
print_step "[2/6] 远程并行编译 launcher-dev + engine-dev..."
# 远程主机可能未安装 git（rsync 也不同步 .git 元数据），版本信息在本地计算后显式传入，
# 避免远程编译产物 COMMIT=unknown（版本号为纯净 BASE_VERSION，git 信息仅走 COMMIT 字段）
LOCAL_GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || true)"
LOCAL_GIT_DIRTY="$(git status --porcelain 2>/dev/null || true)"
ssh_remote "cd ${REMOTE_SRC_DIR} && make launcher-dev engine-dev -j2 GIT_COMMIT='${LOCAL_GIT_COMMIT}' GIT_DIRTY='${LOCAL_GIT_DIRTY}'"
print_success "远程编译完成"

# Step 3: 清理远程旧进程（kill 旧 engine/dlv，不删二进制）
print_step "[3/6] 清理远程旧进程..."
remote_kill_pattern "dlv exec.*engine" || true
remote_kill_pattern "design-cloud-engine" || true
sleep 2
print_success "远程旧进程已清理"

# Step 4: 安装 launcher + engine
print_step "[4/6] 安装 launcher + engine..."
ssh_remote "
  mkdir -p ${REMOTE_INSTALL_DIR} &&
  cp -f ${REMOTE_SRC_DIR}/build/dc-launcher-dev ${REMOTE_LAUNCHER} &&
  cp -f ${REMOTE_SRC_DIR}/build/design-cloud-engine-dev ${REMOTE_BINARY} &&
  chmod 0755 ${REMOTE_BINARY}
"
print_success "二进制已安装: ${REMOTE_LAUNCHER} + ${REMOTE_BINARY}"

# launcher 权限与 setcap 需 root：复用 deployment/packaging/post-install.sh，
# 本地适配安装目录与用户/组占位符后上传，再以 root 执行
# （TARGET_DIR 默认 /opt/design-cloud → 覆盖为远程安装目录；__PKG_USER__/__PKG_GROUP__ → hpcadmin/cad）
print_info "用 root 执行 post-install.sh 设置 launcher 权限与 setcap..."
REMOTE_GROUP="cad"
POST_INSTALL_SRC="${SCRIPT_DIR}/../deployment/packaging/post-install.sh"
POST_INSTALL_REMOTE="/tmp/dc-post-install.sh"
TMP_SCRIPT="$(mktemp)"
# setcap 失败不中断：部分文件系统（NFS）或受限环境不支持 file capabilities，仅提示
sed -e "s@^TARGET_DIR=.*@TARGET_DIR=\"${REMOTE_INSTALL_DIR}\"@" \
  -e "s@__PKG_USER__@${REMOTE_USER}@g" \
  -e "s@__PKG_GROUP__@${REMOTE_GROUP}@g" \
  -e "s@\(setcap cap_setuid,cap_setgid+ep \"\${LAUNCHER_BIN}\"\)@\1 || echo \"⚠️  WARNING: setcap 不可用（文件系统不支持或受限环境），launcher 无法获得特权能力\"@g" \
  "${POST_INSTALL_SRC}" >"${TMP_SCRIPT}"
scp_to_remote "${TMP_SCRIPT}" "${POST_INSTALL_REMOTE}"
rm -f "${TMP_SCRIPT}"
if ssh -p "${SSH_PORT}" "${ROOT_USER}@${REMOTE_HOST}" "chmod +x ${POST_INSTALL_REMOTE} && ${POST_INSTALL_REMOTE} && rm -f ${POST_INSTALL_REMOTE} && (loginctl enable-linger ${REMOTE_USER} 2>/dev/null || true)"; then
  print_success "launcher 权限与 setcap 已配置（属主 ${REMOTE_USER}:${REMOTE_GROUP}）"
else
  print_warning "root 执行 post-install.sh 失败，请手动检查 launcher 权限与 setcap"
fi

# 同步数据库 agent.launcherPath 配置为远程安装路径
sync_remote_launcher_path "${REMOTE_LAUNCHER}"

# Step 5: 确保远程 dlv
print_step "[5/6] 确保远程 dlv..."
if ! ensure_remote_dlv; then
  exit 1
fi

# Step 6: 启动 dlv debug engine
print_step "[6/6] 启动 dlv debug engine..."
# 检测远程 engine 配置文件（远程实际存在 config.toml）
REMOTE_ENGINE_CFG="$(detect_remote_config)"
print_info "engine 配置文件: ${REMOTE_ENGINE_CFG}"

# 启动前预检：用刚安装的二进制校验远程配置能否被正确加载（schema 字段/必填项/运行时解析）。
# 远程 config.toml 是运行期配置，不随 rsync 同步，代码重构 config 字段后易与二进制 schema
# 脱节；不预检会出现"dlv 正常监听但 engine 立即退出"的迷惑现象（排查成本高）。
print_info "预检远程配置（check-config）..."
if ! ssh_remote "cd ${REMOTE_PATH} && ${REMOTE_BINARY} check-config --config ${REMOTE_ENGINE_CFG}"; then
  print_error "远程配置校验失败，已中止启动（engine 会启动即退出）"
  echo "    → 配置文件: ${REMOTE_ENGINE_CFG}"
  echo "    → 常见原因：代码重构了 config 字段名，而远程运行期配置未同步"
  echo "    → 修复后重跑本任务；或用 engine gen-config 生成最新示例对照"
  exit 1
fi
print_success "远程配置校验通过"

start_dlv_debug --config "${REMOTE_ENGINE_CFG}"

# 等待 dlv 启动并监听
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
