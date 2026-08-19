#!/bin/bash

# Role startup configuration tests
# Tests server-only and agent-only startup using topology-driven roles.

set -eo pipefail

# shellcheck source=../_common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../_common.sh"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVER_PATH="${PROJECT_ROOT}/build/design-cloud-engine"
LAUNCHER_PATH="${PROJECT_ROOT}/build/dc-launcher"
TEST_CONFIG_DIR=""

if [[ -f "${LAUNCHER_PATH}" ]]; then
  chmod 700 "${LAUNCHER_PATH}"
fi

setup_test_configs() {
  log_info "Setting up test configurations..."

  TEST_CONFIG_DIR=$(mktemp -d)
  chmod 700 "${TEST_CONFIG_DIR}"

  if [ -n "${XDG_RUNTIME_DIR}" ]; then
    RUNTIME_DIR="${XDG_RUNTIME_DIR}/design-cloud-engine"
  else
    RUNTIME_DIR="/tmp/design-cloud-$(id -u)"
  fi
  rm -rf "${RUNTIME_DIR}"
  mkdir -p "${RUNTIME_DIR}"
  chmod 700 "${RUNTIME_DIR}"

  prepare_cluster_certs "${TEST_CONFIG_DIR}"

  log_success "Test configurations created"
}

write_config() {
  local role="$1"
  local config_path="${TEST_CONFIG_DIR}/${role}.toml"
  local hostname
  hostname=$(local_hostname)

  local servers_val
  if [[ "${role}" == "server" ]]; then
    prepare_api_certs "${TEST_CONFIG_DIR}"
    servers_val="[\"${hostname}\"]"
  else
    servers_val='["mgt01.internal"]'
  fi

  cat >"${config_path}" <<EOF
# 显式置空 → 数据目录回退 BaseDir（配置所在临时目录）；
# 省略该键会继承内建默认 /var/lib/design-cloud，非 root 运行必然失败。
data_dir = ""

[log]
app_level = "INFO"
dir = "${TEST_CONFIG_DIR}/logs"

[portal]
host = "127.0.0.1"
port = "18080"
tls = true
cert = "conf/certs/portal/portal.crt"
key = "conf/certs/portal/portal.key"

[cluster]
port = 15051
servers = ${servers_val}

[database]
type = "sqlite"
path = "${TEST_CONFIG_DIR}/${role}.db"
# sqlite 无密码：显式置空覆盖默认 "dbpwd"，否则 Database.Validate 会要求该文件存在
password_file = ""

[user]
auth_mode = "local"

# local 认证不使用 LDAP：显式置空 bind_password_file，覆盖默认 "ldapbindpwd"
# （loadSecrets 对该字段的强制存在性检查未按 auth_mode 门控，见测试报告）
[user.ldap]
bind_password_file = ""

[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
EOF
}

test_server_binary() {
  log_info "Testing server binary..."

  if [[ ! -f "${SERVER_PATH}" ]]; then
    log_error "Server binary not found at ${SERVER_PATH}"
    return 1
  fi

  if [[ ! -x "${SERVER_PATH}" ]]; then
    log_error "Server binary is not executable"
    return 1
  fi

  log_success "Server binary tests passed"
  return 0
}

test_server_help() {
  log_info "Testing server help and version..."

  if "${SERVER_PATH}" --help >/dev/null 2>&1; then
    log_success "✓ Server help command works"
  else
    log_error "✗ Server help command failed"
    return 1
  fi

  log_success "Server help tests passed"
  return 0
}

test_server_startup() {
  log_info "Testing startup with topology-driven roles..."

  local roles=("server" "agent")

  for role in "${roles[@]}"; do
    write_config "${role}"
    log_info "Testing startup for role: ${role}"

    "${SERVER_PATH}" --config="${TEST_CONFIG_DIR}/${role}.toml" &
    local server_pid=$!

    sleep 5

    if kill -0 "${server_pid}" 2>/dev/null; then
      log_success "✓ Process started successfully for role ${role}"
      kill "${server_pid}" 2>/dev/null || true
      wait "${server_pid}" 2>/dev/null || true
    else
      log_error "✗ Process failed to start for role ${role}"
      return 1
    fi
  done

  log_success "Role startup tests passed"
  return 0
}

test_missing_config() {
  log_info "Testing missing configuration handling..."

  if "${SERVER_PATH}" --config="/nonexistent/config.toml" >/dev/null 2>&1; then
    log_error "✗ Process should fail for missing configuration file"
    return 1
  fi

  log_success "✓ Missing configuration file handled correctly"
  return 0
}

cleanup() {
  log_info "Cleaning up test files..."
  rm -rf "${TEST_CONFIG_DIR}"
  log_success "Cleanup completed"
}

main() {
  echo "=== Role Startup Configuration Tests ==="
  echo ""

  local exit_code=0

  setup_test_configs

  if ! test_server_binary; then
    exit_code=1
  fi

  if ! test_server_help; then
    exit_code=1
  fi

  if ! test_server_startup; then
    exit_code=1
  fi

  if ! test_missing_config; then
    exit_code=1
  fi

  cleanup

  echo ""
  if [[ ${exit_code} -eq 0 ]]; then
    log_success "All role startup tests passed!"
  else
    log_error "Some role startup tests failed"
  fi

  return ${exit_code}
}

main "$@"
