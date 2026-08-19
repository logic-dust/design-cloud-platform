#!/bin/bash
# Harness Verifier: Incremental Validation for Changed Packages
# 只验证 git 工作区中发生变更的包，提供秒级反馈，适合日常高频迭代。
#
# 用法:
#   ./.agents/verifiers/verify-changed.sh                    # 对变更包做核心门禁和条件检查
#   ./.agents/verifiers/verify-changed.sh --race             # 同上并启用 race 检测
#   ./.agents/verifiers/verify-changed.sh --skip-tests       # 跳过单元测试（快速构建验证）
#
# 核心门禁:
#   [1/5] gofmt    变更文件格式检查
#   [2/5] go build 变更包编译
#   [3/5] go vet   变更包静态检查
#   [4/5] go test  变更包单元测试（--skip-tests 可跳过）
#   [5/5] arch     架构依赖边界（仅在 import 变更时触发）
#
# 附加检查:
#   [+] deps       模块依赖整洁度检查（go mod tidy 脏状态校验）—— 确保依赖无遗漏/无残留
#   [+] security   安全红线静态扫描（verify-security.py）—— 所有 Go 变更均执行
#   [+] deadrefs   死代码与兼容残留扫描（scan_dead_refs.sh）—— 仅在删除导出符号时触发
#   [+] sync       API/文档自动同步检测（portal/api 变更 → http 校验；actions 变更 → 文档抽取）

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${PROJECT_ROOT}"

export GOOS="${GOOS:-linux}"
export GOARCH="${GOARCH:-amd64}"

if [ -x "/usr/bin/python3" ]; then
  PYTHON_CMD="/usr/bin/python3"
else
  PYTHON_CMD="$(command -v python3 || echo "python3")"
fi

RACE_FLAG=""
SKIP_TESTS=0
for arg in "$@"; do
  case "${arg}" in
    --race)
      RACE_FLAG="-race"
      ;;
    --skip-tests)
      SKIP_TESTS=1
      ;;
    -*)
      echo "❌ Unknown option: ${arg}"
      echo "Usage: $0 [--race] [--skip-tests]"
      exit 1
      ;;
  esac
done

MOD_CHANGED=$(
  {
    git diff --name-only -- go.mod go.sum 2>/dev/null
    git diff --cached --name-only -- go.mod go.sum 2>/dev/null
  } | sort -u
)

PROTO_CHANGED=$(
  {
    git diff --name-only -- '*.proto' 2>/dev/null
    git diff --cached --name-only -- '*.proto' 2>/dev/null
    git ls-files --others --exclude-standard -- '*.proto' 2>/dev/null
  } | sort -u
)

CHANGED_FILES=$(
  {
    git diff --name-only -- '*.go' 2>/dev/null
    git diff --cached --name-only -- '*.go' 2>/dev/null
    git ls-files --others --exclude-standard -- '*.go' 2>/dev/null
  } | sort -u
)
if [ -z "${CHANGED_FILES}" ]; then
  if [ -n "${PROTO_CHANGED}" ]; then
    echo "=== Verifying Protobuf Schema Health (changed .proto) ==="
    if ! "${PYTHON_CMD}" .agents/scripts/scan_proto_issues.py; then
      exit 1
    fi
    echo "✅ Protobuf schema checks passed."
    exit 0
  fi
  if [ -n "${MOD_CHANGED}" ]; then
    echo "=== Verifying Module Dependencies (go mod tidy) ==="
    MOD_HASH_BEFORE=$(sha256sum go.mod go.sum 2>/dev/null || true)
    go mod tidy
    MOD_HASH_AFTER=$(sha256sum go.mod go.sum 2>/dev/null || true)
    if [ "${MOD_HASH_BEFORE}" != "${MOD_HASH_AFTER}" ]; then
      echo "❌ go.mod or go.sum is not tidy! 'go mod tidy' generated changes."
      echo "   Please run 'go mod tidy' and commit the resulting changes."
      exit 1
    fi
    echo "✅ Module dependencies are tidy."
    exit 0
  fi
  echo "⚠️ No changed Go files detected. Nothing to verify incrementally."
  exit 0
fi

# 推导变更包路径
CHANGED_PKGS=$(echo "${CHANGED_FILES}" | xargs -I{} dirname {} | sort -u | awk '{if($0==".") print "."; else print $0}')

VALID_PKGS=()
for pkg in ${CHANGED_PKGS}; do
  if find "${pkg}" -maxdepth 1 -name '*.go' -print -quit 2>/dev/null | grep -q .; then
    VALID_PKGS+=("./${pkg}")
  fi
done

if [ ${#VALID_PKGS[@]} -eq 0 ]; then
  echo "⚠️ Changed files do not belong to any Go package; nothing to verify."
  exit 0
fi

TARGET="${VALID_PKGS[*]}"
EXISTING_GO_FILES=()
for file in ${CHANGED_FILES}; do
  [ -f "${file}" ] && EXISTING_GO_FILES+=("${file}")
done
echo "=== Incremental Verification for Changed Packages ==="
echo "🎯 Target packages: ${TARGET}"

# 1. Format check on changed files
echo "[1/5] Running format check on changed files..."
UNFORMATTED=$(gofmt -l "${EXISTING_GO_FILES[@]}" 2>/dev/null || true)
if [ -n "${UNFORMATTED}" ]; then
  echo "❌ The following files are not formatted:"
  echo "${UNFORMATTED}"
  exit 1
fi
echo "✅ Format check passed."

# [+] Check module tidiness
echo "[+] Checking module dependencies (go mod tidy)..."
MOD_HASH_BEFORE=$(sha256sum go.mod go.sum 2>/dev/null || true)
go mod tidy
MOD_HASH_AFTER=$(sha256sum go.mod go.sum 2>/dev/null || true)
if [ "${MOD_HASH_BEFORE}" != "${MOD_HASH_AFTER}" ]; then
  echo "❌ go.mod or go.sum is not tidy! 'go mod tidy' generated changes."
  echo "   Please run 'go mod tidy' and commit the resulting changes."
  exit 1
fi
echo "✅ Module dependencies are tidy."

# 2. Build changed packages
echo "[2/5] Building changed packages..."
if ! go build ${TARGET}; then
  echo "❌ Build failed for: ${TARGET}"
  exit 1
fi
echo "✅ Build passed."

# 3. Vet changed packages
echo "[3/5] Running go vet on changed packages..."
if ! go vet ${TARGET}; then
  echo "❌ go vet failed for: ${TARGET}"
  exit 1
fi
echo "✅ go vet passed."

# 4. Test changed packages
if [ "${SKIP_TESTS}" -eq 1 ]; then
  echo "[4/5] Skipping unit tests (--skip-tests)."
else
  echo "[4/5] Running unit tests for changed packages..."
  if [ "$(uname -s)" != "Linux" ] && [ "${GOOS}" = "linux" ]; then
    echo "  ⚠️ Host OS is $(uname -s) (non-Linux) but target is Linux/amd64. Verifying test compilation..."
    for pkg in ${TARGET}; do
      if ! go test -c "${pkg}" -o /dev/null; then
        echo "❌ Test compilation failed in changed package: ${pkg}"
        exit 1
      fi
    done
    echo "✅ Test compilation passed for all changed packages."
  else
    if ! go test ${RACE_FLAG} -v ${TARGET}; then
      echo "❌ Unit tests failed in changed packages: ${TARGET}"
      exit 1
    fi
    echo "✅ Unit tests passed."
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# 5. Architecture boundary — 仅在变更文件含 import 语句时触发
#    理由：改 struct tag / 注释 / 字面量 不会引入新包依赖，跑全量 arch 是浪费。
# ─────────────────────────────────────────────────────────────────────────────
IMPORT_CHANGED=""
for file in "${EXISTING_GO_FILES[@]}"; do
  if grep -qE '^\s*import\b' "${file}"; then
    IMPORT_CHANGED="${file}"
    break
  fi
done
if [ -n "${IMPORT_CHANGED}" ]; then
  echo "[5/5] Checking architecture boundary rules..."
  if ! "${PYTHON_CMD}" .agents/verifiers/verify-arch.py; then
    exit 1
  fi
else
  echo "[5/5] Skipping architecture boundary check (no import changes detected)."
fi

# ─────────────────────────────────────────────────────────────────────────────
# [必做] Security — 所有 Go 变更都运行全量静态扫描。
# 安全风险由代码内容而非文件名决定，扫描成本也足够低。
# ─────────────────────────────────────────────────────────────────────────────
echo "[+] Running security red-line scan..."
if ! "${PYTHON_CMD}" .agents/verifiers/verify-security.py; then
  exit 1
fi
echo "✅ Security scan passed."

# ─────────────────────────────────────────────────────────────────────────────
# [条件] Dead refs — 仅在变更包含删除行（-行 > 0）时触发
#   触发条件：git diff 有减少行（可能删掉导出符号），新增变更不会产生死引用。
# ─────────────────────────────────────────────────────────────────────────────
DELETED_LINES=$(
  {
    git diff --numstat -- '*.go' 2>/dev/null
    git diff --cached --numstat -- '*.go' 2>/dev/null
  } | awk '{sum += $2} END {print sum+0}'
)
if [ "${DELETED_LINES}" -gt 0 ]; then
  echo "[+] Running dead code & cleanup compliance scan (deleted lines detected)..."
  if ! ./.agents/scripts/scan_dead_refs.sh; then
    exit 1
  fi
  echo "✅ Dead code scan passed."
else
  echo "[+] Skipping dead code scan (only additions/renames, no deletions)."
fi

# ─────────────────────────────────────────────────────────────────────────────
# [条件] API / doc sync — 仅在 portal/api 或 launcher actions 变更时触发
# ─────────────────────────────────────────────────────────────────────────────
SYNC_FAILED=0
API_CHANGED=$(echo "${CHANGED_FILES}" | grep -c '^internal/server/portal/api/.*\.go$' || true)
ACTIONS_CHANGED=$(echo "${CHANGED_FILES}" | grep -cE '^(internal/execution/actions\.go|internal/launcher/.*\.go)$' || true)

if [ "${API_CHANGED}" -gt 0 ] || [ "${ACTIONS_CHANGED}" -gt 0 ]; then
  echo "[+] Checking API & documentation sync..."

  if [ "${API_CHANGED}" -gt 0 ]; then
    echo "  → Portal API changed; extracting API summary..."
    "${PYTHON_CMD}" .agents/scripts/scan_portal_api.py > /dev/null 2>&1 || true
    echo "  → Verifying .http test coverage (strict)..."
    if ! "${PYTHON_CMD}" .agents/scripts/verify_http_tests.py --strict; then
      echo "❌ .http test files not synced with API routes. Update test/api/*.http."
      SYNC_FAILED=1
    fi
  fi

  if [ "${ACTIONS_CHANGED}" -gt 0 ]; then
    echo "  → Launcher actions changed; extracting action docs..."
    if ! "${PYTHON_CMD}" .agents/scripts/extract_actions.py > /dev/null 2>&1; then
      echo "❌ extract_actions.py failed."
      SYNC_FAILED=1
    fi
  fi

  if [ "${SYNC_FAILED}" -eq 0 ]; then
    echo "✅ API & documentation sync checks passed."
  else
    exit 1
  fi
else
  echo "[+] Skipping API/doc sync (no portal API or launcher action changes)."
fi

# ─────────────────────────────────────────────────────────────────────────────
# [条件] Proto baggage & health — 仅在 .proto 变更时触发
# ─────────────────────────────────────────────────────────────────────────────
if [ -n "${PROTO_CHANGED}" ]; then
  echo "[+] Running Protobuf baggage & health scan (changed .proto detected)..."
  if ! "${PYTHON_CMD}" .agents/scripts/scan_proto_issues.py; then
    exit 1
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# [条件] Windows cross-build check — 任意 cmd/ 或 internal/ 下 .go 变动即触发
# （门禁已升级为全仓交叉编译，任何包都可能破坏 Windows 构建，故不再限于 agent 链路）
# ─────────────────────────────────────────────────────────────────────────────
GO_CHANGED=$(echo "${CHANGED_FILES}" | grep -cE '^(cmd/|internal/).*\.go$' || true)
if [ "${GO_CHANGED}" -gt 0 ]; then
  echo "[+] Checking Windows cross-compilation (full repo)..."
  if ! "${PROJECT_ROOT}/.agents/verifiers/verify-cross-build.sh"; then
    echo "❌ Windows cross-compilation failed."
    exit 1
  fi
fi

echo "✅ All incremental verifications passed!"
exit 0
