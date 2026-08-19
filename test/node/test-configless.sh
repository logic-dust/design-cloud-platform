#!/bin/bash

# Configless bootstrap tests (binary-level, no server required)
# Covers: mode ladder fail-fast, bootstrap parameter validation, cert-missing
# guidance, node register portal-url validation and the cert permission matrix.

set -eo pipefail

# shellcheck source=../_common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../_common.sh"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVER_PATH="${PROJECT_ROOT}/build/design-cloud-engine"
WORK_DIR=""
FAILURES=0

run_expect_fail() {
  local label="$1"
  local expect="$2"
  shift 2

  local output
  local code=0
  output=$(timeout 15 "$@" 2>&1) || code=$?

  if [[ ${code} -eq 0 ]]; then
    log_error "✗ ${label}: expected non-zero exit, got 0"
    FAILURES=$((FAILURES + 1))
    return
  fi
  # -F 固定串 + -- 终止选项解析：期望串可能以 '-' 开头（如 "--portal-url <url> is required"），
  # 否则 grep 会把它当选项报 "unrecognized option"，断言永远失败。
  if ! grep -qF -- "${expect}" <<<"${output}"; then
    log_error "✗ ${label}: output does not contain '${expect}'"
    echo "${output}" | sed 's/^/    | /' | head -8
    FAILURES=$((FAILURES + 1))
    return
  fi
  log_success "✓ ${label} (exit ${code})"
}

setup() {
  WORK_DIR=$(mktemp -d)
  chmod 700 "${WORK_DIR}"
}

test_binary() {
  if [[ ! -x "${SERVER_PATH}" ]]; then
    log_error "Server binary not found or not executable: ${SERVER_PATH}"
    exit 1
  fi
}

test_mode_ladder() {
  log_info "Testing start-mode ladder failures..."

  # Conflict: explicit --config + explicit --servers.
  run_expect_fail "conflict --config/--servers" "cannot specify both" \
    "${SERVER_PATH}" --config "${WORK_DIR}/any.toml" --servers "mgt01"

  # Neither declared and no default config.toml (run inside an empty directory).
  run_expect_fail "no mode declared" "no configuration specified" \
    bash -c "cd '${WORK_DIR}' && '${SERVER_PATH}'"

  # Conflict: --port without --servers.
  run_expect_fail "port without --servers" "only valid with --servers" \
    "${SERVER_PATH}" --port 52255

  # Conflict: --config with join-only --data-dir.
  run_expect_fail "config with --data-dir" "only valid with --servers" \
    "${SERVER_PATH}" --config "${WORK_DIR}/any.toml" --data-dir "${WORK_DIR}"

  # Legacy flag name must not exist.
  run_expect_fail "legacy --server rejected" "not defined" \
    "${SERVER_PATH}" --server "mgt01"

  # Removed cert overrides must not exist.
  run_expect_fail "legacy --cert rejected" "not defined" \
    "${SERVER_PATH}" --servers "mgt01" --cert "${WORK_DIR}/x.crt"
}

test_bootstrap_params() {
  log_info "Testing bootstrap parameter validation..."

  run_expect_fail "port inside --servers" "must be set via --port" \
    "${SERVER_PATH}" --servers "mgt01:52255"
  run_expect_fail "topology pattern rejected" "does not support topology patterns" \
    "${SERVER_PATH}" --servers "cn[01-11]"
  run_expect_fail "cluster port range" "cluster port must be in range" \
    "${SERVER_PATH}" --servers "mgt01" --port 80
  run_expect_fail "invalid log level" "invalid --log-level" \
    "${SERVER_PATH}" --servers "mgt01" --log-level verbose
}

test_missing_certificate_guidance() {
  log_info "Testing certificate-missing guidance in configless mode..."

  local empty_dir="${WORK_DIR}/empty-data"
  mkdir -p "${empty_dir}"
  run_expect_fail "missing certs guide to node register" "node register --portal-url" \
    "${SERVER_PATH}" --servers "mgt01" --data-dir "${empty_dir}"
}

test_register_validation() {
  log_info "Testing node register parameter validation (configless)..."

  # No --portal-url → explicit bootstrap failure (never reads config.toml).
  run_expect_fail "register requires --portal-url" "--portal-url <url> is required" \
    bash -c "cd '${WORK_DIR}' && '${SERVER_PATH}' node register --token dummy"

  # Bare hostname/URL missing scheme must be rejected before any network IO.
  run_expect_fail "register rejects bare hostname" "must be a full http(s) URL" \
    bash -c "cd '${WORK_DIR}' && '${SERVER_PATH}' node register --token dummy --portal-url mgt01"

  # Plain http without --insecure-http must be rejected to prevent MitM credential interception.
  run_expect_fail "register rejects plain http without override" "insecure http portal URL rejected" \
    bash -c "cd '${WORK_DIR}' && '${SERVER_PATH}' node register --token dummy --portal-url http://mgt01:8443"
}

test_cert_permission_matrix() {
  log_info "Testing cluster cert permission matrix..."

  local cert_root="${WORK_DIR}/certs-matrix"
  prepare_cluster_certs "${cert_root}" "cn-test"

  local hostname="cn-test"
  local cluster_dir="${cert_root}/certs/cluster"
  local ca_dir="${cluster_dir}/ca"
  local node_dir="${cluster_dir}/${hostname}"

  assert_perm() {
    local path="$1"
    local want="$2"
    local got
    got=$(stat -c '%a' "${path}" 2>/dev/null || echo "missing")
    if [[ "${got}" != "${want}" ]]; then
      log_error "✗ ${path} mode=${got} want=${want}"
      FAILURES=$((FAILURES + 1))
    else
      log_success "✓ ${path} mode=${got}"
    fi
  }

  assert_perm "${cluster_dir}" "755"
  assert_perm "${cluster_dir}/ca.crt" "644"
  assert_perm "${ca_dir}" "700"
  assert_perm "${ca_dir}/ca.key" "600"
  assert_perm "${node_dir}" "700"
  assert_perm "${node_dir}/${hostname}.crt" "644"
  assert_perm "${node_dir}/${hostname}.key" "600"
}

cleanup() {
  rm -rf "${WORK_DIR}"
}

main() {
  echo "=== Configless Bootstrap Tests ==="
  echo ""

  setup
  trap cleanup EXIT

  test_binary
  test_mode_ladder
  test_bootstrap_params
  test_missing_certificate_guidance
  test_register_validation
  test_cert_permission_matrix

  echo ""
  if [[ ${FAILURES} -eq 0 ]]; then
    log_success "All configless bootstrap tests passed!"
    return 0
  fi
  log_error "Configless bootstrap tests failed: ${FAILURES} assertion(s)"
  return 1
}

main "$@"
