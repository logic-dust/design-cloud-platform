#!/usr/bin/env bash
# Harness Verifier: Change-Aware One-Stop Verification
# 智能调度器：根据 git 工作区变更类型，决定执行哪一级验证，避免对无关变更跑重型流程。
#
# 用法:
#   ./.agents/verifiers/verify-all.sh                 # 变更感知（默认）：
#                                                     #   - 无变更 → 直接退出
#                                                     #   - 仅非 Go 变更 → 跳过重型验证
#                                                     #   - 有 Go 变更 → verify-changed.sh 核心门禁 + 条件检查
#   ./.agents/verifiers/verify-all.sh --full          # 强制全量回归（大版本/大重构前）：
#                                                     #   build + vet + 全量测试 + arch + security + deadrefs
#   ./.agents/verifiers/verify-all.sh --skip-tests    # 跳过测试阶段（快速构建验证）
#   ./.agents/verifiers/verify-all.sh --race          # 增量模式启用 race 检测

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

FULL=0
SKIP_TESTS=0
RACE=0
for arg in "$@"; do
  case "${arg}" in
    --full)
      FULL=1
      ;;
    --skip-tests)
      SKIP_TESTS=1
      ;;
    --race)
      RACE=1
      ;;
    -*)
      echo "❌ Unknown option: ${arg}"
      echo "Usage: $0 [--full] [--skip-tests] [--race]"
      exit 1
      ;;
  esac
done

echo "========================================================================"
if [ "${FULL}" -eq 1 ]; then
  echo "🚀 Running FULL Regression Verification (--full)"
else
  echo "🚀 Running Change-Aware Verification"
fi
echo "========================================================================="

# =============================================================================
# 模式一：--full 强制全量回归（不感知变更，全部跑一遍）
# =============================================================================
if [ "${FULL}" -eq 1 ]; then
  echo ""
  echo "--- [1/8] Build & Vet (all packages) ---"
  "${SCRIPT_DIR}/verify-build.sh"

  echo ""
  echo "--- [2/8] Architecture Boundaries ---"
  python3 "${SCRIPT_DIR}/verify-arch.py"

  echo ""
  if [ "${SKIP_TESTS}" -eq 1 ]; then
    echo "--- [3/8] Skipping All Unit Tests (--skip-tests) ---"
  else
    echo "--- [3/8] All Unit Tests ---"
    TEST_ARGS="--all"
    [ "${RACE}" -eq 1 ] && TEST_ARGS="${TEST_ARGS} --race"
    "${SCRIPT_DIR}/verify-tests.sh" ${TEST_ARGS}
  fi

  echo ""
  echo "--- [4/8] Security Red-Line Scan ---"
  python3 "${SCRIPT_DIR}/verify-security.py"

  echo ""
  echo "--- [5/8] Dead Code & Cleanup Scan ---"
  "${ROOT_DIR}/.agents/scripts/scan_dead_refs.sh"

  echo ""
  echo "--- [6/8] HTTP Test Coverage (strict) ---"
  python3 "${ROOT_DIR}/.agents/scripts/verify_http_tests.py" --strict

  echo ""
  echo "--- [7/8] Protobuf Baggage & Structural Health Scan ---"
  python3 "${ROOT_DIR}/.agents/scripts/scan_proto_issues.py"

  echo ""
  echo "--- [8/8] Dependency Updates Advisory Check ---"
  UPDATES=$(go list -u -m -f '{{if and (not .Indirect) .Update}}{{.Path}}: {{.Version}} -> {{.Update.Version}}{{end}}' all 2>/dev/null || true)
  if [ -n "${UPDATES}" ]; then
    echo "💡 Outdated direct dependencies detected:"
    echo "${UPDATES}"
    echo "   (Advisory: Patch/minor updates can be updated cleanly via 'go get <pkg>@latest'."
    echo "    Major/breaking updates require cautious evaluation and dedicated validation.)"
  else
    echo "✅ All direct dependencies are up to date."
  fi

  echo ""
  echo "========================================================================"
  echo "✅ Full Regression Passed Cleanly! Ready to Commit."
  echo "========================================================================="
  exit 0
fi

# =============================================================================
# 模式二：变更感知（默认）
# =============================================================================
CHANGED_FILES=$(
  {
    git diff --name-only 2>/dev/null
    git diff --cached --name-only 2>/dev/null
    git ls-files --others --exclude-standard 2>/dev/null
  } | sort -u
)
if [ -z "${CHANGED_FILES}" ]; then
  echo "ℹ️  Working tree is clean. Nothing to verify."
  exit 0
fi

GO_CHANGED=$(echo "${CHANGED_FILES}" | grep -cE '(\.go$|go\.mod$|go\.sum$|\.proto$)' || true)
HARNESS_CHANGED=$(echo "${CHANGED_FILES}" | grep -c '^\.agents/' || true)

echo "Changed files: $(echo "${CHANGED_FILES}" | wc -l | tr -d ' ') (Go: ${GO_CHANGED})"

if [ "${HARNESS_CHANGED}" -gt 0 ]; then
  echo "--- Harness self-check (.agents changed) ---"
  bash "${SCRIPT_DIR}/verify-harness.sh"
fi

if [ "${GO_CHANGED}" -eq 0 ]; then
  echo "ℹ️  Only non-Go files changed (docs/config/scripts). Skipping Go verification."
  exit 0
fi

RACE_ARGS=""
[ "${RACE}" -eq 1 ] && RACE_ARGS="--race"
[ "${SKIP_TESTS}" -eq 1 ] && RACE_ARGS="${RACE_ARGS} --skip-tests"

echo ""
echo "--- Incremental gate via verify-changed.sh ---"
"${SCRIPT_DIR}/verify-changed.sh" ${RACE_ARGS}

echo ""
echo "========================================================================"
echo "✅ Change-Aware Verification Passed! Ready to Commit."
echo "========================================================================="
exit 0
