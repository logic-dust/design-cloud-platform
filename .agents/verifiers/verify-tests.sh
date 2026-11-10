#!/bin/bash
# Harness Verifier: Unit Tests Validation
# 默认基于 git 变更只跑相关包的测试；可通过 --all 触发全量回归，--race 启用竞争检测。
#
# 用法:
#   ./.agents/verifiers/verify-tests.sh                        # 仅测试变更包（推荐日常迭代）
#   ./.agents/verifiers/verify-tests.sh --all                  # 全量单元测试
#   ./.agents/verifiers/verify-tests.sh ./internal/service/... # 指定包测试
#   ./.agents/verifiers/verify-tests.sh --race ./internal/...  # 启用 race 检测

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${PROJECT_ROOT}"

# 目标平台固定为 linux/amd64（唯一受支持的部署架构）
export GOOS="${GOOS:-linux}"
export GOARCH="${GOARCH:-amd64}"

echo "=== Verifying Unit Tests ==="

# 解析参数
RACE_FLAG=""
ALL=0
TARGET_PKG=""

for arg in "$@"; do
  case "${arg}" in
  --race)
    RACE_FLAG="-race"
    ;;
  --all)
    ALL=1
    ;;
  -*)
    echo "❌ Unknown option: ${arg}"
    echo "Usage: $0 [--all] [--race] [TARGET_PACKAGE]"
    exit 1
    ;;
  *)
    TARGET_PKG="${arg}"
    ;;
  esac
done

run_tests() {
  local target="$1"
  echo "[1/1] Running unit tests for: ${target}"

  if [ "$(uname -s)" != "Linux" ] && [ "${GOOS}" = "linux" ]; then
    echo "  ⚠️ Host OS is $(uname -s) (non-Linux) but target is Linux/amd64. Verifying test compilation..."
    for pkg in ${target}; do
      if ! go test -c "${pkg}" -o /dev/null; then
        echo "❌ Test compilation failed in: ${pkg}"
        return 1
      fi
    done
    echo "✅ Test compilation passed for: ${target}"
    return 0
  fi

  # 用 Go 原生 ./... 直接跑（一次编译 + 并行执行，最快）。
  # 本地节点/dcexec（test/node）与实验室场景（test/lab）不在本脚本内: make test-e2e / test/lab/*
  go test ${RACE_FLAG} -v "${target}"
}

# 情况 1：显式指定了包路径
if [ -n "${TARGET_PKG}" ]; then
  if ! run_tests "${TARGET_PKG}"; then
    echo "❌ Unit tests failed in ${TARGET_PKG}"
    echo "Harness Guard: You must read the trace above and fix the issue."
    exit 1
  fi
  echo "✅ Unit tests passed for: ${TARGET_PKG}"
  exit 0
fi

# 情况 2：--all 显式全量
if [ "${ALL}" -eq 1 ]; then
  if ! run_tests "./..."; then
    echo "❌ Global unit tests failed."
    echo "Harness Guard: Test suite failed, workflow paused for self-recovery."
    exit 1
  fi
  echo "✅ All unit tests passed safely!"
  exit 0
fi

# 情况 3：默认 —— 根据 git 变更推导相关包
CHANGED_FILES=$(
  {
    git diff --name-only -- '*.go' 2>/dev/null
    git diff --cached --name-only -- '*.go' 2>/dev/null
    git ls-files --others --exclude-standard -- '*.go' 2>/dev/null
  } | sort -u
)
if [ -z "${CHANGED_FILES}" ]; then
  echo "⚠️ No changed Go files detected; skipping unit tests."
  exit 0
fi

# 推导变更包：取 .go 文件所在目录，按包路径去重
CHANGED_PKGS=$(echo "${CHANGED_FILES}" | xargs -I{} dirname {} | sort -u | awk '{if($0==".") print "."; else print $0}' | sed 's|^\.|.|')

# 过滤出真实存在的 Go 包（含 _test.go 的包也算）
VALID_PKGS=()
for pkg in ${CHANGED_PKGS}; do
  if find "${pkg}" -maxdepth 1 -name '*.go' -print -quit 2>/dev/null | grep -q .; then
    VALID_PKGS+=("./${pkg}")
  fi
done

if [ ${#VALID_PKGS[@]} -eq 0 ]; then
  echo "⚠️ Changed files do not belong to any Go package; skipping tests."
  exit 0
fi

TARGET="${VALID_PKGS[*]}"
echo "🎯 Detected changed packages: ${TARGET}"

if ! go test ${RACE_FLAG} -v ${TARGET}; then
  echo "❌ Unit tests failed in changed packages: ${TARGET}"
  echo "Harness Guard: You must read the trace above and fix the issue."
  exit 1
fi

echo "✅ Unit tests passed for changed packages: ${TARGET}"
exit 0
