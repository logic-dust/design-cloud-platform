#!/bin/bash
# Harness Script: Dead Code & Cleanup Compliance Scanner
# 对应 .agents/rules/cleanup-compliance.md 的可执行细则，扫描工作区中的残留与死代码。
#
# 检测项（非 _test.go 文件）:
#   1. TODO/FIXME remove 残留标记（旧代码迁移后必须删除，禁止注释占位）→ 阻断
#   2. 兼容别名: type X = Y / var X = Y（可能是有意的 DTO 语义别名）→ 警告
#   3. 注释掉的代码: // func / // var / // type / // const 开头的行 → 阻断
#   4. 未被引用的导出错误变量: var ErrX = errors.New(...) 全库仅出现 1 次 → 阻断
#
# 用法:
#   ./.agents/scripts/scan_dead_refs.sh        # 扫描 internal/ + cmd/
#   ./.agents/scripts/scan_dead_refs.sh --all  # 同默认（脚本按全量设计，不设增量）

set -u

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${PROJECT_ROOT}"

SCAN_DIRS="internal cmd"
SCAN_TARGETS=()
for d in ${SCAN_DIRS}; do
  [ -d "${d}" ] && SCAN_TARGETS+=("${d}")
done

GO_FILES() {
  find "${SCAN_TARGETS[@]}" -name '*.go' ! -name '*_test.go' 2>/dev/null
}

TOTAL_ISSUES=0
TOTAL_WARNINGS=0

report() {
  local category="$1"
  local file="$2"
  local line="$3"
  echo "❌ [${category}] ${file}:${line}"
  TOTAL_ISSUES=$((TOTAL_ISSUES + 1))
}

warn() {
  local category="$1"
  local file="$2"
  local line="$3"
  echo "⚠️  [${category}] ${file}:${line}"
  TOTAL_WARNINGS=$((TOTAL_WARNINGS + 1))
}

echo "========================================================================="
echo "  Dead Code & Cleanup Compliance Scan"
echo "========================================================================="

# --- 1. TODO/FIXME remove 残留标记 ---
echo ""
echo "[1/4] Scanning removal markers (// TODO: remove / delete / cleanup)..."

MARKERS=$(grep -rnE '//\s*(TODO|FIXME|XXX)\s*:\s*(remove|delete|cleanup|drop)' \
  $(GO_FILES) 2>/dev/null || true)
if [ -n "${MARKERS}" ]; then
  while IFS=: read -r file line rest; do
    report "removal-marker" "${file}" "${line}"
  done <<< "${MARKERS}"
else
  echo "✅ No removal markers found."
fi

# --- 2. 兼容别名: type X = Y / var X = Y ---
echo ""
echo "[2/4] Scanning compatibility aliases (type X = Y / var X = Y)..."

ALIASES=$(grep -rnE '^\s*(type|var)\s+[A-Z][A-Za-z0-9_]*\s*=\s*[A-Za-z_][A-Za-z0-9_.]*\s*$' \
  $(GO_FILES) 2>/dev/null || true)
if [ -n "${ALIASES}" ]; then
  while IFS=: read -r file line rest; do
    warn "compat-alias" "${file}" "${line}"
  done <<< "${ALIASES}"
else
  echo "✅ No compatibility aliases found."
fi

# --- 3. 注释掉的代码 ---
echo ""
echo "[3/4] Scanning commented-out code (// func / // var / // type / // const)..."

COMMENTED=$(grep -rnE '^\s*//\s*(func|var|type|const)\s+[A-Za-z_][A-Za-z0-9_]*' \
  $(GO_FILES) 2>/dev/null || true)
if [ -n "${COMMENTED}" ]; then
  while IFS=: read -r file line rest; do
    report "commented-code" "${file}" "${line}"
  done <<< "${COMMENTED}"
else
  echo "✅ No commented-out code found."
fi

# --- 4. 未被引用的导出错误变量 ---
echo ""
echo "[4/4] Scanning unreferenced exported error variables..."

ERR_VARS=$(grep -rhoE '^\s*var\s+(Err[A-Za-z0-9_]+)\s*=' $(GO_FILES) 2>/dev/null | \
  sed -E 's/.*var\s+([A-Za-z0-9_]+)\s*=.*/\1/' | sort -u)

UNREF_FOUND=0
for name in ${ERR_VARS}; do
  count=$(grep -rhoE "\b${name}\b" $(GO_FILES) 2>/dev/null | wc -l)
  if [ "${count}" -le 1 ]; then
    file=$(grep -rlE "^\s*var\s+${name}\s*=" $(GO_FILES) 2>/dev/null | head -1)
    report "dead-error-var" "${file:-?}" "${name}"
    UNREF_FOUND=1
  fi
done
if [ "${UNREF_FOUND}" -eq 0 ]; then
  echo "✅ All exported error variables are referenced."
fi

echo ""
if [ "${TOTAL_ISSUES}" -eq 0 ]; then
  if [ "${TOTAL_WARNINGS}" -eq 0 ]; then
    echo "✅ No dead code or cleanup compliance violations detected."
  else
    echo "⚠️  ${TOTAL_WARNINGS} warning(s) (aliases may be intentional DTO types; review manually)."
    echo "✅ No blocking dead code violations detected."
  fi
  exit 0
fi

echo "❌ ${TOTAL_ISSUES} blocking issue(s) detected. Clean them up before proceeding."
exit 1
