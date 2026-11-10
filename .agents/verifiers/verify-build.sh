#!/bin/bash
# Harness Verifier: Build Health Check
# 快速验证整个项目能否成功编译，作为轻量级的"任何修改后"首道卡点。
# 复用 Go 构建缓存；不清理共享缓存。
#
# 用法:
#   ./.agents/verifiers/verify-build.sh                       # 全项目构建（复用缓存，秒级反馈）
#   ./.agents/verifiers/verify-build.sh ./internal/agent/...  # 指定包构建

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${PROJECT_ROOT}"

# 目标平台固定为 linux/amd64（唯一受支持的部署架构）
export GOOS="${GOOS:-linux}"
export GOARCH="${GOARCH:-amd64}"

echo "=== Verifying Build Health ==="

TARGET="./..."

for arg in "$@"; do
  case "${arg}" in
    -*)
      echo "❌ Unknown option: ${arg}"
      echo "Usage: $0 [TARGET]"
      exit 1
      ;;
    *)
      TARGET="${arg}"
      ;;
  esac
done

echo "[1/2] Building (incremental): ${TARGET}"

if ! go build "${TARGET}"; then
  echo "❌ Build failed for: ${TARGET}"
  echo "Harness Guard: Fix compilation errors before running tests."
  exit 1
fi

echo "[2/2] Running go vet: ${TARGET}"
if ! go vet "${TARGET}"; then
  echo "❌ go vet failed for: ${TARGET}"
  exit 1
fi

echo "✅ Build and vet passed for: ${TARGET}"
exit 0
