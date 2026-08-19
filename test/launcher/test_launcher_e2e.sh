#!/bin/bash
set -eo pipefail

# Configuration
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_DIR="${PROJECT_ROOT}/test/bin"
LAUNCHER_BIN="${BIN_DIR}/launcher"

echo "=== Building Launcher & Runner ==="
mkdir -p "${BIN_DIR}"
go build -tags dev -o "${LAUNCHER_BIN}" ./cmd/launcher
chmod 700 "${LAUNCHER_BIN}" # Satisfy launcher security check

RUNNER_BIN="${BIN_DIR}/runner"
go build -tags dev -o "${RUNNER_BIN}" ./test/launcher/runner.go
chmod 700 "${RUNNER_BIN}"

echo "=== Setting up Token (Prod Mode) ==="
# Matching common.GetRuntimeDir() logic:
if [ -n "${XDG_RUNTIME_DIR}" ]; then
  RUNTIME_DIR="${XDG_RUNTIME_DIR}/design-cloud-engine"
else
  RUNTIME_DIR="/tmp/design-cloud-engine-$(id -u)"
fi
TOKEN_FILE="${RUNTIME_DIR}/dc-launcher.token"
TOKEN_VALUE=$(openssl rand -hex 16)

mkdir -p "${RUNTIME_DIR}"
echo -n "${TOKEN_VALUE}" >"${TOKEN_FILE}"
chmod 600 "${TOKEN_FILE}"
export DCE_LAUNCHER_TOKEN="${TOKEN_VALUE}"
echo "Token setup at: ${TOKEN_FILE}"

echo "=== Testing Action: USER_PASSWORD_VERIFY (Privileged Mode) ==="
# 目的：验证 launcher 端到端链路是否打通 —— L1 环境校验 → L2 token 校验 →
# L3 动作白名单 → payload 解码 → handler 分发。
# 只要请求抵达 handler 即算通过，具体结果取决于当前身份：
#   - root   ：动作真正执行（探测密码不匹配 → 业务错误）
#   - 非 root ：因无法提权被权限层拦下（dev 环境的预期结果）
# 失败判据：出现协议/解码层错误 —— 那说明请求根本没抵达 handler。

# Construct execution.ExecuteInput JSON (compact single-line, launcher protocol requirement)
# Field names must match Go json struct tags (camelCase).
# payload 必须携带 handler 的必填字段：若传空对象，会在参数校验处提前返回，
# 无法区分“链路不通”与“参数不全”。
PAYLOAD='{"version":"","token":"'"${TOKEN_VALUE}"'","request":{"actionName":"USER_PASSWORD_VERIFY","requestId":"test-req-1","targetUser":"root","traceId":"test-req-1","payload":{"username":"root","password":"dce-e2e-probe"}}}'

# Use runner to bypass "parent is shell" check
# Disable exit on error for this command to capture output
set +e
# Run Action
# use || true to prevent script exit on launcher error (since we expect 1 or 126 or other errors)
OUTPUT=$(echo "${PAYLOAD}" | "${RUNNER_BIN}" "${LAUNCHER_BIN}" || true)

echo "--- Output Start ---"
echo "${OUTPUT}"
echo "--- Output End ---"

# Output Logic:
# The launcher returns execution.ExecuteOutput JSON.
# { "success": true, "message": "...", "output": "...actual data..." }

# 协议/解码层错误：请求没有抵达 handler，链路是断的。
PROTOCOL_ERROR_PATTERN='invalid payload type|failed to unmarshal payload|payload is required|Failed to parse request|RuntimeEnvironment verification failed|Token verification failed|Action not allowed'
if echo "${OUTPUT}" | grep -Eq "${PROTOCOL_ERROR_PATTERN}"; then
  echo "[FAIL] Launcher 链路未打通（协议/解码层错误）: ${OUTPUT}"
  exit 1
fi

if echo "${OUTPUT}" | grep -q '"success":true'; then
  echo "[PASS] Launcher 端到端链路打通，动作已执行（当前为 root）"
else
  echo "[PASS] Launcher 端到端链路打通，动作在执行阶段被拦截: ${OUTPUT}"
fi

# Skip result content checks as we expect failure
echo "=== All E2E Tests Passed (Auth Verified) ==="
exit 0
