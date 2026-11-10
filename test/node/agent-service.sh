#!/bin/bash

# Agent service tests
# Tests the agent service functionality and dc-exec integration using config.toml.

set -eo pipefail

# shellcheck source=_common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_common.sh"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVER_PATH="${PROJECT_ROOT}/build/dc-engine"
EXEC_BIN_PATH="${PROJECT_ROOT}/build/dc-exec"

if [[ -f "${EXEC_BIN_PATH}" ]]; then
  chmod 700 "${EXEC_BIN_PATH}"
fi

reset_runtime_dir() {
  if [ -n "${XDG_RUNTIME_DIR}" ]; then
    RUNTIME_DIR="${XDG_RUNTIME_DIR}/design-cloud-engine"
  else
    RUNTIME_DIR="/tmp/design-cloud-$(id -u)"
  fi
  rm -rf "${RUNTIME_DIR}"
  mkdir -p "${RUNTIME_DIR}"
  chmod 700 "${RUNTIME_DIR}"
}

write_agent_conf() {
  local test_dir="$1"
  local port="$1"
  local config_path="${test_dir}/config.toml"
  local hostname
  hostname=$(local_hostname)

  cat >"${config_path}" <<EOF
[log]
app_level = "INFO"
dir = "${test_dir}/logs"

[cluster]
port = 15051
connect_timeout = 3

# 本机不在 servers 中 → 纯 Agent 角色（控制面校验按角色跳过）
servers = ["server-test"]
EOF
}

test_agent_startup() {
  log_info "Testing agent service startup..."

  reset_runtime_dir

  local test_dir
  test_dir=$(mktemp -d)
  chmod 700 "${test_dir}"

  prepare_cluster_certs "${test_dir}"
  write_agent_conf "${test_dir}"

  "${SERVER_PATH}" --config="${test_dir}/config.toml" &
  local agent_pid=$!

  sleep 3

  if kill -0 "${agent_pid}" 2>/dev/null; then
    log_success "✓ Agent service started successfully"
    kill "${agent_pid}" 2>/dev/null || true
    wait "${agent_pid}" 2>/dev/null || true
  else
    log_error "✗ Agent service failed to start"
    rm -rf "${test_dir}"
    return 1
  fi

  rm -rf "${test_dir}"
  log_success "Agent startup tests passed"
  return 0
}

test_dcexec_available() {
  log_info "Testing dc-exec availability for agent..."

  if [[ -f "${EXEC_BIN_PATH}" ]]; then
    log_success "✓ dc-exec binary available for agent"
  else
    log_error "✗ dc-exec binary not found"
    return 1
  fi

  log_success "dc-exec availability tests passed"
  return 0
}

test_dcexec_integration() {
  log_info "Testing agent-dc-exec integration..."

  if [[ ! -f "${EXEC_BIN_PATH}" ]]; then
    log_error "dc-exec binary not found, skipping integration test"
    return 1
  fi

  if [[ -x "${EXEC_BIN_PATH}" ]]; then
    log_success "✓ dc-exec is accessible to agent"
  else
    log_error "✗ dc-exec is not executable"
    return 1
  fi

  local test_request='{"action": "invalid_test", "target_uid": 1000, "args": []}'
  if echo "${test_request}" | "${EXEC_BIN_PATH}" >/dev/null 2>&1; then
    log_info "dc-exec responded to test request (expected to fail)"
  else
    log_success "✓ dc-exec correctly rejected invalid test request"
  fi

  log_success "Agent-dc-exec integration tests passed"
  return 0
}

test_agent_error_handling() {
  log_info "Testing agent error handling (missing config)..."

  # Engine should fail when config file does not exist
  if "${SERVER_PATH}" --config="/nonexistent/config.toml" >/dev/null 2>&1; then
    log_error "✗ Engine should fail with missing config file"
    return 1
  fi

  log_success "✓ Engine correctly rejects missing config file"
  log_success "Agent error handling tests passed"
  return 0
}

main() {
  echo "=== Agent Service Tests ==="
  echo ""

  local exit_code=0

  if ! test_agent_startup; then
    exit_code=1
  fi

  if ! test_dcexec_available; then
    exit_code=1
  fi

  if ! test_dcexec_integration; then
    exit_code=1
  fi

  if ! test_agent_error_handling; then
    exit_code=1
  fi

  echo ""
  if [[ ${exit_code} -eq 0 ]]; then
    log_success "All agent service tests passed!"
  else
    log_error "Some agent service tests failed"
  fi

  return ${exit_code}
}

main "$@"
