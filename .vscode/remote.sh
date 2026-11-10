#!/bin/bash
# 远程开发统一入口（配置在 env.sh，实现全在 common.sh）
#
#   remote.sh push     本地编译 dc-exec+engine → 上传安装 → dlv 启动
#   remote.sh build    rsync 源码 → 远程编译 → 安装 → dlv 启动
#   remote.sh status   查看状态（直连可达性 / 进程 / 端口 / API / dc-exec 权限）
#   remote.sh cleanup  停远程 dlv/engine
#
# push 与 build 只差“产物怎么来”，其余（安装、dc-exec 权限、配置预检、dlv 启动与 API 校验）完全共用。
#
# 不开端口转发：两条流程本就需要 SSH/rsync 到同一台机，而 Portal 与 dlv 均监听 0.0.0.0，
# 因此本机可直接 `curl http://$REMOTE_HOST:1234/...` 并让 VSCode 直连 $REMOTE_HOST:2345。
# dlv 无认证的安全权衡与加固办法见 DEBUG_GUIDE.md。

set -e
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${SCRIPT_DIR}/common.sh"

case "${1:-}" in
  push) run_flow push ;;
  build) run_flow build ;;
  status) show_status ;;
  cleanup) cleanup_all ;;
  *)
    print_error "用法: $(basename "$0") {push|build|status|cleanup}"
    exit 2
    ;;
esac
