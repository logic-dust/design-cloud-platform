#!/usr/bin/env bash
# Picks a Go binary that satisfies go.mod (prefer cached toolchain on CI/VM images).
resolve_go_cmd() {
  if [ -n "${GO_CMD:-}" ] && [ -x "${GO_CMD}" ]; then
    printf '%s' "${GO_CMD}"
    return 0
  fi
  # 若 go.mod 声明 toolchain，GOTOOLCHAIN=auto 会选择该版本；解析其 GOROOT 下的真实 go 二进制
  if command -v go >/dev/null 2>&1; then
    local goroot
    goroot="$(GOTOOLCHAIN=auto go env GOROOT 2>/dev/null)"
    if [ -n "${goroot}" ] && [ -x "${goroot}/bin/go" ]; then
      printf '%s' "${goroot}/bin/go"
      return 0
    fi
  fi
  if command -v go >/dev/null 2>&1; then
    command -v go
    return 0
  fi
  return 1
}

export_go_toolchain() {
  local go_cmd
  go_cmd="$(resolve_go_cmd)" || {
    echo "❌ Go toolchain not found (need Go 1.27.2+ or set GO_CMD to a 1.27.2+ binary)."
    return 1
  }
  export GO_CMD="${go_cmd}"
  export PATH="$(dirname "${GO_CMD}"):${PATH}"

  # 镜像/CI 常预设 GOROOT 指向另一版本（例如 /usr/local/go 的 1.21），
  # 直接执行 toolchain 二进制时会去错误的 GOROOT 找标准库，报 "package iter is not in std"。
  # 这里以 GO_CMD 自身位置为准校准：不匹配则丢弃继承值，让 go 自行解析。
  local want_root
  want_root="$(cd "$(dirname "$(dirname "${GO_CMD}")")" && pwd -P)"
  if [ -n "${GOROOT:-}" ] && [ "$(cd "${GOROOT}" 2>/dev/null && pwd -P)" != "${want_root}" ]; then
    echo "⚠️  Ignoring inherited GOROOT=${GOROOT} (does not match ${GO_CMD})"
    unset GOROOT
  fi

  echo "Using Go: ${GO_CMD} ($("${GO_CMD}" version))"
}
