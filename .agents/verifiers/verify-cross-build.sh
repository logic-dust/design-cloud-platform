#!/usr/bin/env bash
# Verifier: Full-repo Windows cross-compilation gate.
#
# 覆盖面（2026-09 从"仅 cmd/engine"升级为全仓）：
#   - 所有包（含 server 面）必须能交叉编译到 windows/amd64；
#   - dc-launcher 链路（cmd/launcher、internal/launcher/**）是 Linux-only，
#     以 //go:build !windows 显式声明，交叉构建时整体跳过（不是编译失败）；
#   - go vet 会同时类型检查 *_test.go，因此 [2/3] 覆盖测试文件的可编译性。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

export CGO_ENABLED=0
export GOOS=windows
export GOARCH=amd64

echo "=== Windows Cross-Build Verification (GOOS=windows GOARCH=amd64 CGO_ENABLED=0) ==="

echo "[1/3] Cross-compiling all packages..."
if ! go build ./...; then
  echo "❌ Windows cross-compilation failed."
  exit 1
fi

echo "[2/3] go vet all packages (includes test files)..."
if ! go vet ./...; then
  echo "❌ Windows vet failed."
  exit 1
fi

echo "[3/3] Compiling Windows-runnable test binaries (must execute on a real Windows host)..."
for pkg in ./internal/agent ./internal/agent/probe ./internal/execution ./cmd/engine/command; do
  if ! go test -c -o /dev/null "${pkg}"; then
    echo "❌ Windows test compilation failed: ${pkg}"
    exit 1
  fi
done

echo "✅ Windows cross-compilation verified successfully (build + vet + test-compile)."
