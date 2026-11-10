#!/bin/bash
# 节点 / dc-exec 手工集成测试的唯一入口。
# 覆盖：node 角色模式、configless 引导、agent service、dc-exec 安全特性。
# 需要本机 build/ 产物；部分用例要求 root 或 dc-exec 具备 setcap，故不进自动化门禁。
# Portal HTTP 集成测试属另一层：test/portal/ 与 `make test-api`。

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

cd "${PROJECT_ROOT}"

RUN_BUILD=false

usage() {
  cat <<'EOF'
用法: test/node/run_all.sh [选项]

选项:
  --build    先执行 make engine dc-exec 再跑用例
  -h, --help 显示本帮助

说明:
  需要 build/dc-engine 与 build/dc-exec 已存在。
  部分用例需 root / setcap 权限，失败时按输出提示区分「环境不满足」与「实现缺陷」。
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --build) RUN_BUILD=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      log_error "未知参数: $1"
      usage
      exit 2
      ;;
  esac
done

if [ "${RUN_BUILD}" = "true" ]; then
  log_info "构建 engine + dcexec..."
  make engine dc-exec
fi

for bin in build/dc-engine build/dc-exec; do
  if [ ! -f "${bin}" ]; then
    log_error "缺少 ${bin}：先执行 make engine dc-exec，或加 --build"
    exit 1
  fi
done

FAILED=()

run_case() {
  local name="$1"
  local script="$2"
  log_info "→ ${name} (${script})"
  if bash "${SCRIPT_DIR}/${script}"; then
    log_success "✓ ${name}"
  else
    log_error "✗ ${name}"
    FAILED+=("${name}")
  fi
}

run_case "node 角色模式" node-modes.sh
run_case "configless 引导" configless.sh
run_case "agent service" agent-service.sh
run_case "dc-exec 安全特性" security-features.sh

echo ""
if [ ${#FAILED[@]} -gt 0 ]; then
  log_error "失败用例: ${FAILED[*]}"
  exit 1
fi
log_success "全部 e2e 用例通过"
