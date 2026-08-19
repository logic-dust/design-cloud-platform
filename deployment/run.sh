#!/bin/bash
set -euo pipefail

# =============================================================================
# 全局变量
# =============================================================================

workdir="$(dirname "$(readlink -f "$0")")"
exec_name="design-cloud-engine"
config_file="${workdir}/config.toml"
log_file="/tmp/${exec_name}.log"

# --- Go runtime settings
export GOGC=100
export GOMEMLIMIT=64GiB  # Soft limit for Go runtime (25% of 256GB RAM)
export GOTRACEBACK=all

# =============================================================================
# main() 函数
# =============================================================================

main() {
  local action="${1:-status}"
  
  # Set GOMAXPROCS at runtime (in case CPU limits changed)
  export GOMAXPROCS="$(get_max_procs)"

  case "${action}" in
  status)
    show_status
    ;;
  start)
    start_server
    ;;
  stop)
    stop_server
    ;;
  restart)
    stop_server
    sleep 1
    start_server
    ;;
  log)
    show_log
    ;;
  version | ver)
    show_version
    ;;
  *)
    usage
    exit 1
    ;;
  esac
}

# =============================================================================
# 辅助函数
# =============================================================================

usage() {
  echo "Usage: $0 {start|stop|restart|log|status|version}"
}

get_max_procs() {
  local nproc
  nproc=$(nproc)
  if [[ ${nproc} -lt 16 ]]; then
    echo "${nproc}"
  else
    echo "16"
  fi
}

check_process() {
  set +e
  "${workdir}/${exec_name}" service status >/dev/null 2>&1
  local rc=$?
  set -e
  return ${rc}
}

start_server() {
  echo "start ..."

  if [[ ! -f "${workdir}/${exec_name}" ]]; then
    echo "[ERROR] Binary not found: ${workdir}/${exec_name}" >&2
    return 1
  fi

  if [[ ! -f "${config_file}" ]]; then
    echo "[ERROR] Config not found: ${config_file}" >&2
    return 1
  fi

  if check_process; then
    echo "[OK] Server is already running"
    return 0
  fi

  cd "${workdir}" || {
    echo "[ERROR] Failed to change directory to ${workdir}"
    exit 1
  }

  setsid ./"${exec_name}" --config "${config_file}" >>"${log_file}" 2>&1 &
  cd - &>/dev/null || true

  local max_wait=10
  local count=0
  while ! check_process && [[ ${count} -lt ${max_wait} ]]; do
    sleep 1
    count=$((count + 1))
  done

  if check_process; then
    echo "[OK] Start success"
    return 0
  else
    echo "[ERROR] Server failed to start, please check the log file: ${log_file}" >&2
    return 1
  fi
}

stop_server() {
  if check_process; then
    echo "server is running, stopping it"
    "${workdir}/${exec_name}" service stop
    echo "[OK] Stop success"
    return 0
  fi

  echo "Already stopped"
  return 0
}

show_status() {
  echo "=== Design Cloud Engine ==="
  echo "working directory: ${workdir}"
  echo "config file:       ${config_file}"
  echo "log file:          ${log_file}"
  echo

  if ! "${workdir}/${exec_name}" service status 2>/dev/null; then
    echo "Status: Stopped"
  fi
}

show_log() {
  echo "log is file://${log_file}"
  if [[ -s "${log_file}" ]]; then
    tail -f "${log_file}"
  fi
  exit 0
}

show_version() {
  if [[ -x "${workdir}/${exec_name}" ]]; then
    "${workdir}/${exec_name}" --version
  fi
  exit 0
}

# =============================================================================
# 程序入口
# =============================================================================

main "$@"
