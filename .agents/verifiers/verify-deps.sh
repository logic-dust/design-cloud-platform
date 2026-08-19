#!/bin/bash
# Harness Verifier: Dependencies, Format & Architecture Validation
# 默认基于 git 变更做增量检查（快速反馈），仅在 --all 时执行全量扫描。
#
# 用法:
#   ./.agents/verifiers/verify-deps.sh        # 增量：变更文件的 fmt/vet + 全量架构
#   ./.agents/verifiers/verify-deps.sh --all  # 全量：所有文件 fmt/vet + 架构

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${PROJECT_ROOT}"

# 目标平台固定为 linux/amd64（唯一受支持的部署架构）
export GOOS="${GOOS:-linux}"
export GOARCH="${GOARCH:-amd64}"

EXPLICIT_ALL=0
FULL_SCAN=0
for arg in "$@"; do
  case "${arg}" in
  --all)
    EXPLICIT_ALL=1
    FULL_SCAN=1
    ;;
  -*)
    echo "❌ Unknown option: ${arg}"
    echo "Usage: $0 [--all]"
    exit 1
    ;;
  esac
done

# 收集变更的 .go 文件及依赖文件（包含已修改和未跟踪的新文件）
CHANGED_FILES=$(
  git diff --name-only -- '*.go' 'go.mod' 'go.sum' 2>/dev/null
  git ls-files --others --exclude-standard -- '*.go' 'go.mod' 'go.sum' 2>/dev/null
)
if [ -z "${CHANGED_FILES}" ] && [ "${EXPLICIT_ALL}" -eq 0 ]; then
  echo "⚠️ No changed Go files detected. Skipping incremental Go fmt/vet check."
  echo "=== Verifying Dependencies, Formats and Architecture ==="
  echo "[3/3] Checking architecture boundary rules..."
  if ! python3 .agents/verifiers/verify-arch.py; then
    exit 1
  fi
  echo "✅ Architecture boundaries respected."
  echo "✅ Verification complete for non-Go changes!"
  exit 0
fi

# 从变更文件推导涉及的 Go 包路径
CHANGED_PKGS=""
if [ "${FULL_SCAN}" -eq 0 ]; then
  RAW_PKGS=$(echo "${CHANGED_FILES}" | xargs -I{} dirname {} | sort -u)
  for pkg in ${RAW_PKGS}; do
    if [ -d "${pkg}" ]; then
      if [ "${pkg}" = "." ]; then
        CHANGED_PKGS="${CHANGED_PKGS} ./..."
      else
        CHANGED_PKGS="${CHANGED_PKGS} ./${pkg}/..."
      fi
    fi
  done
  # deduplicate
  CHANGED_PKGS=$(echo "${CHANGED_PKGS}" | tr ' ' '\n' | sort -u | tr '\n' ' ')
fi

echo "=== Verifying Dependencies, Formats and Architecture ==="

# 1. Dependency module check (go mod tidy)
echo "[1/4] Checking module dependencies (go mod tidy)..."
MOD_HASH_BEFORE=$(sha256sum go.mod go.sum 2>/dev/null || true)
go mod tidy
MOD_HASH_AFTER=$(sha256sum go.mod go.sum 2>/dev/null || true)
if [ "${MOD_HASH_BEFORE}" != "${MOD_HASH_AFTER}" ]; then
  echo "❌ go.mod or go.sum is not tidy! 'go mod tidy' generated changes."
  echo "   Please run 'go mod tidy' and commit the resulting changes."
  exit 1
fi
echo "✅ Module dependencies are tidy."

# 2. Format check
echo "[2/4] Running format check..."
if [ "${FULL_SCAN}" -eq 1 ]; then
  UNFORMATTED=$(gofmt -l . 2>/dev/null || true)
else
  UNFORMATTED=$(echo "${CHANGED_FILES}" | xargs gofmt -l 2>/dev/null || true)
fi

if [ -n "${UNFORMATTED}" ]; then
  echo "❌ The following files are not formatted:"
  echo "${UNFORMATTED}"
  echo "Run 'gofmt -w' on them or use the formatter in your IDE."
  exit 1
fi
echo "✅ Format check passed."

# 3. Vet check
echo "[3/4] Running go vet..."
if [ "${FULL_SCAN}" -eq 1 ]; then
  VET_TARGET="./..."
else
  VET_TARGET="${CHANGED_PKGS}"
fi

if ! go vet ${VET_TARGET}; then
  echo "❌ go vet failed for: ${VET_TARGET}"
  exit 1
fi
echo "✅ go vet passed."

# 4. Arch boundary enforcement (matches constraints/arch-rules.yaml)
echo "[4/4] Checking architecture boundary rules..."
if ! python3 .agents/verifiers/verify-arch.py; then
  exit 1
fi

echo "✅ All dependency and architecture verifications passed! Model can proceed."
exit 0
