#!/bin/bash

# Security feature tests
# Keeps launcher-focused checks and a server auth smoke test aligned with current config.toml.

set -eo pipefail

# shellcheck source=../_common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../_common.sh"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVER_PATH="${PROJECT_ROOT}/build/design-cloud-engine"
LAUNCHER_PATH="${PROJECT_ROOT}/build/dc-launcher"

write_server_conf() {
  local dir="$1"
  local hostname
  hostname=$(local_hostname)
  cat >"${dir}/config.toml" <<EOF
# 显式置空 → 数据目录回退 BaseDir（配置所在临时目录）；
# 省略该键会继承内建默认 /var/lib/design-cloud，非 root 运行必然失败。
data_dir = ""

[log]
app_level = "INFO"
dir = "${dir}/logs"

[portal]
host = "127.0.0.1"
port = "18080"
tls = true
cert = "conf/certs/portal/portal.crt"
key = "conf/certs/portal/portal.key"

[cluster]
port = 15051
servers = ["${hostname}"]

[database]
type = "sqlite"
path = "${dir}/security.db"
# sqlite 无密码：显式置空覆盖默认 "dbpwd"，否则 Database.Validate 会要求该文件存在
password_file = ""

[user]
auth_mode = "local"

# local 认证不使用 LDAP：显式置空 bind_password_file，覆盖默认 "ldapbindpwd"
[user.ldap]
bind_password_file = ""

EOF
}

test_launcher_security() {
  log_info "Testing launcher security features..."

  if [[ ! -f "${LAUNCHER_PATH}" ]]; then
    log_error "Launcher binary not found"
    return 1
  fi

  local invalid_inputs=(
    ""
    "invalid json"
    '{"incomplete": "request"}'
    '{"action": "invalid_action"}'
  )

  for input in "${invalid_inputs[@]}"; do
    if echo "${input}" | "${LAUNCHER_PATH}" >/dev/null 2>&1; then
      log_error "✗ Launcher should reject invalid input: ${input}"
      return 1
    fi
  done

  log_success "✓ Launcher correctly validates input"

  local invalid_actions=(
    '{"action": "rm_rf", "target_uid": 1000, "args": []}'
    '{"action": "sudo_su", "target_uid": 1000, "args": []}'
    '{"action": "backdoor", "target_uid": 1000, "args": []}'
  )

  for action in "${invalid_actions[@]}"; do
    if echo "${action}" | "${LAUNCHER_PATH}" >/dev/null 2>&1; then
      log_error "✗ Launcher should reject invalid action: ${action}"
      return 1
    fi
  done

  log_success "✓ Launcher correctly enforces action allowlist"
  log_success "Launcher security tests passed"
  return 0
}

test_server_auth_smoke() {
  log_info "Testing server auth configuration smoke startup..."

  local test_dir
  test_dir=$(mktemp -d)
  chmod 700 "${test_dir}"
  write_server_conf "${test_dir}"
  prepare_cluster_certs "${test_dir}"
  prepare_api_certs "${test_dir}"

  "${SERVER_PATH}" --config="${test_dir}/config.toml" &
  local server_pid=$!
  sleep 3

  if kill -0 "${server_pid}" 2>/dev/null; then
    log_success "✓ Server started with mTLS cluster and HTTPS API configuration"
    kill "${server_pid}" 2>/dev/null || true
    wait "${server_pid}" 2>/dev/null || true
  else
    log_error "✗ Server failed to start with mTLS cluster and HTTPS API configuration"
    rm -rf "${test_dir}"
    return 1
  fi

  rm -rf "${test_dir}"
  log_success "Server auth smoke test passed"
  return 0
}

test_auth_security() {
  log_info "Testing authentication security..."

  if [[ ! -f "${LAUNCHER_PATH}" ]]; then
    log_info "Launcher not available, skipping auth security tests"
    return 0
  fi

  local auth_tests=(
    '{"action": "authenticate", "username": "nonexistent", "password": "wrong"}'
    '{"action": "authenticate", "username": "", "password": "password"}'
    '{"action": "authenticate", "username": "user", "password": ""}'
    '{"action": "authenticate", "username": "root", "password": "password123"}'
  )

  for auth_test in "${auth_tests[@]}"; do
    local result
    result=$(echo "${auth_test}" | "${LAUNCHER_PATH}" 2>/dev/null || echo "failed")

    if echo "${result}" | grep -q '"success":true'; then
      log_error "✗ Authentication should fail for invalid credentials"
      return 1
    fi
  done

  log_success "✓ Authentication correctly rejects invalid credentials"
  log_success "Authentication security tests passed"
  return 0
}

test_privilege_escalation() {
  log_info "Testing privilege escalation prevention..."

  if [[ ! -f "${LAUNCHER_PATH}" ]]; then
    log_info "Launcher not available, skipping privilege escalation tests"
    return 0
  fi

  local escalation_tests=(
    '{"action": "shell_command", "target_uid": 0, "args": ["whoami"]}'
    '{"action": "shell_command", "target_uid": 1, "args": ["sudo", "whoami"]}'
  )

  if [[ ${EUID} -ne 0 ]]; then
    for escalation_test in "${escalation_tests[@]}"; do
      local result
      result=$(echo "${escalation_test}" | "${LAUNCHER_PATH}" 2>/dev/null || echo "failed")

      if echo "${result}" | grep -q '"success":true'; then
        log_error "✗ Privilege escalation should be prevented"
        return 1
      fi
    done

    log_success "✓ Privilege escalation correctly prevented"
  else
    log_warn "Running as root, skipping privilege escalation tests"
  fi

  log_success "Privilege escalation tests passed"
  return 0
}

main() {
  echo "=== Security Features Tests ==="
  echo ""

  local exit_code=0

  if ! test_launcher_security; then
    exit_code=1
  fi

  if ! test_server_auth_smoke; then
    exit_code=1
  fi

  if ! test_auth_security; then
    exit_code=1
  fi

  if ! test_privilege_escalation; then
    exit_code=1
  fi

  echo ""
  if [[ ${exit_code} -eq 0 ]]; then
    log_success "All security feature tests passed!"
  else
    log_error "Some security feature tests failed"
  fi

  return ${exit_code}
}

main "$@"
