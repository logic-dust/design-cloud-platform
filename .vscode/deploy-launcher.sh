#!/bin/bash
# VSCode: 打包 design-cloud-engine（engine + launcher）→ 分发 → 安装
#
# 前置条件:
#   1. 本地已配置 root@cent7.dev.vm 的 SSH 免密登录
#   2. 远程主机已安装 createrepo（可选，仅本地 yum 仓库场景）
#
# 使用方式:
#   Ctrl+Shift+P → Tasks: Run Task → deploy-launcher
#   或直接: bash .vscode/deploy-launcher.sh

set -e

source "$(dirname "${BASH_SOURCE[0]}")/env.sh"

print_separator
echo -e "${COLOR_BLUE}design-cloud-engine 整包部署${COLOR_NC}"
echo -e "${COLOR_BLUE}目标: ${ROOT_USER}@${REMOTE_HOST}${COLOR_NC}"
print_separator

# Step 1: 编译正式版二进制并打包（锁步）
print_step "[1/4] 编译 engine+launcher 并打包..."
make -C "${SCRIPT_DIR}/.." package PKG_TYPE=rpm
print_success "RPM 包构建成功（design-cloud-engine）"

# Step 2: 查找最新构建的 RPM
print_step "[2/4] 查找 RPM 包..."
RPM_FILE=$(ls -t build/packages/design-cloud-engine-*.rpm 2>/dev/null | head -1)
if [ -z "${RPM_FILE}" ]; then
  print_error "未找到 RPM 包"
  exit 1
fi
echo "    → ${RPM_FILE}"
print_success "找到 RPM 包: $(basename "${RPM_FILE}")"

# Step 3: 上传到远程
print_step "[3/4] 上传 RPM 包到远程..."
echo "    → ${RPM_FILE} → ${ROOT_USER}@${REMOTE_HOST}:${REMOTE_TMP}/"
scp -P "${SSH_PORT}" "${RPM_FILE}" "${ROOT_USER}@${REMOTE_HOST}:${REMOTE_TMP}/"
print_success "上传完成"

# Step 4: 远程安装
print_step "[4/4] 远程安装 RPM..."
REMOTE_RPM="${REMOTE_TMP}/$(basename "${RPM_FILE}")"
ssh -p "${SSH_PORT}" "${ROOT_USER}@${REMOTE_HOST}" \
  "rpm -Uvh --force ${REMOTE_RPM} && rm -f ${REMOTE_RPM}"

# 同步数据库 agent.launcherPath 配置为 RPM 生产标准路径
source "${SCRIPT_DIR}/common.sh"
sync_remote_launcher_path "/opt/design-cloud/dc-launcher"

print_success "design-cloud-engine 部署完成（请确认 /etc/design-cloud/config.toml 后再 enable）"
