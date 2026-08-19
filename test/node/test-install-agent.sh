#!/bin/bash

# Agent install-script tests (no root required)
# Covers: the `server|agent` role contract and the derived defaults, plus the systemd unit
# and directory layout the script produces. The script is sourced (it only runs its main
# flow when executed directly) so the functions can be exercised into throwaway dirs
# without root, without systemd and without touching a real node registration.

set -eo pipefail

# shellcheck source=../_common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../_common.sh"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_SH="${PROJECT_ROOT}/deployment/install.sh"
ME="$(id -un)"
MYGROUP="$(id -gn)"
FAILURES=0

# parse_args 失败时 exit 1，用子 shell 隔离，避免带走测试进程
expect_parse_fail() {
  local label="$1" expect="$2"
  shift 2
  local output code=0
  output=$(bash -c "set -eo pipefail; source '${INSTALL_SH}'; parse_args \"\$@\"" _ "$@" 2>&1) || code=$?
  if [[ ${code} -eq 0 ]]; then
    log_error "✗ ${label}: expected non-zero exit, got 0"
    FAILURES=$((FAILURES + 1))
    return
  fi
  if ! grep -qF -- "${expect}" <<<"${output}"; then
    log_error "✗ ${label}: output does not contain '${expect}'"
    echo "${output}" | sed 's/^/    | /' | head -6
    FAILURES=$((FAILURES + 1))
    return
  fi
  log_success "✓ ${label} (exit ${code})"
}

expect_contains() {
  local label="$1" needle="$2" haystack="$3"
  if grep -qF -- "${needle}" <<<"${haystack}"; then
    log_success "✓ ${label}"
  else
    log_error "✗ ${label}: missing '${needle}'"
    echo "${haystack}" | sed 's/^/    | /' | head -8
    FAILURES=$((FAILURES + 1))
  fi
}

echo ""
log_info "Testing install.sh help output..."
help_out="$(bash "${INSTALL_SH}" --help 2>&1)"
expect_contains "help documents the role subcommand" "server|agent" "${help_out}"
expect_contains "help documents --portal-url" "--portal-url" "${help_out}"
expect_contains "help documents --skip-register" "--skip-register" "${help_out}"
expect_contains "help documents --insecure-http" "--insecure-http" "${help_out}"

echo ""
log_info "Testing role argument validation..."
expect_parse_fail "missing role" "缺少角色参数"
expect_parse_fail "unknown role" "未知角色" foo
expect_parse_fail "agent without portal-url" "agent 形态需要 --portal-url" agent
expect_parse_fail "portal-url must be a full URL" "必须是完整的 http(s) URL" \
  agent --portal-url mgt01:8443 --token dummy
expect_parse_fail "plain http without insecure flag" "使用了未加密的 http:// 协议" \
  agent --portal-url http://mgt01:8443 --token dummy
expect_parse_fail "cluster port lower bound" "必须在 1024..65535" \
  agent --portal-url https://mgt01:8443 --token dummy --port 80
expect_parse_fail "cluster port upper bound" "必须在 1024..65535" \
  agent --portal-url https://mgt01:8443 --token dummy --port 70000
expect_parse_fail "log level whitelist" "只能是 DEBUG/INFO/WARN/ERROR" \
  agent --portal-url https://mgt01:8443 --token dummy --log-level LOUD
expect_parse_fail "token required unless --skip-register" "需要 --token <OTP>" \
  agent --portal-url https://mgt01:8443
expect_parse_fail "unknown flag" "未知参数" agent --not-a-flag

echo ""
log_info "Testing derived defaults..."
defaults_out="$(bash -c "
  source '${INSTALL_SH}'
  parse_args agent --portal-url https://mgt01:8443 --token dummy --data-dir /var/lib/dc-probe
  echo \"SERVERS=\$SERVERS\"
  echo \"LOG_DIR=\$LOG_DIR\"
  echo \"MODE=\$MODE\"
" 2>&1)"
expect_contains "servers default to the portal host" "SERVERS=mgt01" "${defaults_out}"
expect_contains "log dir defaults to FHS" "LOG_DIR=/var/log/design-cloud" "${defaults_out}"
expect_contains "mode switches to agent" "MODE=agent" "${defaults_out}"

echo ""
log_info "Testing the generated agent layout and systemd unit..."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

BUILD="${WORK}/build"
INSTALL="${WORK}/install"
DATA="${WORK}/data"
LOGS="${WORK}/logs"
UNITS="${WORK}/units"
mkdir -p "${BUILD}" "${INSTALL}" "${UNITS}"
printf '#!/bin/sh\necho fake-engine\n' >"${BUILD}/design-cloud-engine"
printf '#!/bin/sh\necho fake-launcher\n' >"${BUILD}/dc-launcher"
chmod 0755 "${BUILD}/design-cloud-engine" "${BUILD}/dc-launcher"

build_out="$(bash -c "
  set -eo pipefail
  source '${INSTALL_SH}'
  parse_args agent --portal-url https://mgt01:8443 --skip-register \
    --servers mgt01,mgt02 --port 52255 \
    --data-dir '${DATA}' --log-dir '${LOGS}' --log-level DEBUG \
    --install-dir '${INSTALL}' --service-user '${ME}' --service-group '${MYGROUP}' \
    --service-name dce-agent-test --service-dir '${UNITS}'
  BUILD_DIR='${BUILD}'
  create_directories
  install_engine
  create_systemd_service
  show_summary
" 2>&1)" || true

UNIT="${UNITS}/dce-agent-test.service"
DROPIN="${UNITS}/dce-agent-test.service.d/configless.conf"
if [[ -f "${UNIT}" ]]; then
  log_success "✓ unit written: ${UNIT}"
else
  log_error "✗ unit not written: ${UNIT}"
  echo "${build_out}" | sed 's/^/    | /' | head -20
  FAILURES=$((FAILURES + 1))
fi

if [[ -f "${DROPIN}" ]]; then
  log_success "✓ configless drop-in written: ${DROPIN}"
  dropin_content="$(cat "${DROPIN}")"
  expect_contains "drop-in runs configless" "--servers mgt01,mgt02" "${dropin_content}"
  expect_contains "drop-in carries the cluster port" "--port 52255" "${dropin_content}"
  expect_contains "drop-in points at the data dir" "--data-dir ${DATA}" "${dropin_content}"
  expect_contains "drop-in uses the given log dir" "--log-dir ${LOGS}" "${dropin_content}"
  expect_contains "drop-in uses the given log level" "--log-level DEBUG" "${dropin_content}"
  if grep -q -- "--config" <<<"${dropin_content}"; then
    log_error "✗ drop-in must not reference a config file"
    FAILURES=$((FAILURES + 1))
  else
    log_success "✓ drop-in has no --config"
  fi
else
  log_error "✗ configless drop-in missing: ${DROPIN}"
  echo "${build_out}" | sed 's/^/    | /' | head -20
  FAILURES=$((FAILURES + 1))
fi

[[ -d "${DATA}" ]] && log_success "✓ data dir created: ${DATA}" || { log_error "✗ data dir missing"; FAILURES=$((FAILURES + 1)); }
[[ -x "${INSTALL}/design-cloud-engine" ]] && log_success "✓ engine installed: ${INSTALL}/design-cloud-engine" || { log_error "✗ engine not installed"; FAILURES=$((FAILURES + 1)); }
expect_contains "summary reports the agent mode" "无配置文件" "${build_out}"
expect_contains "summary reports the cluster servers" "mgt01,mgt02:52255" "${build_out}"


echo ""
if [[ ${FAILURES} -eq 0 ]]; then
  log_success "All agent install-script tests passed!"
  exit 0
fi
log_error "Agent install-script tests failed: ${FAILURES} assertion(s)"
exit 1
