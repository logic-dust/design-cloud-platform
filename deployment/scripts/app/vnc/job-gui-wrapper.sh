#!/bin/bash
# ==============================================================================
# Design Cloud Engine - EDA GUI Job Wrapper Script
# 
# Usage:
#   job-gui-wrapper.sh <command> [args...]
#
# Features:
#   1. Starts lightweight TurboVNC server with dynamic free display allocation.
#   2. Validates exit status ($?) of vncserver startup.
#   3. Extracts allocated display and port from startup log.
#   4. Automatically terminates VNC server and cleans up logs on EXIT/SIGINT/SIGTERM.
#   5. Runs target EDA application in foreground.
# ==============================================================================
set -o pipefail

JOB_ID="${LSB_JOBID:-${SLURM_JOB_ID:-$$}}"
INFO_DIR="${WORKDIR:-.}"
LOG_FILE="${INFO_DIR}/.dce_job_${JOB_ID}.log"

# 1. 启动 vncserver（自动寻找可用 display，并重定向日志）
VNCSERVER_BIN="${VNCSERVER_BIN:-/opt/TurboVNC/bin/vncserver}"
if [ ! -x "${VNCSERVER_BIN}" ]; then
    VNCSERVER_BIN="$(command -v vncserver 2>/dev/null || echo "vncserver")"
fi

"${VNCSERVER_BIN}" -desktop "dce-job-${JOB_ID}" > "${LOG_FILE}" 2>&1
STATUS=$?

# 2. 检查启动返回值
if [ ${STATUS} -ne 0 ]; then
    echo "[ERROR] Failed to start VNC server (exit code: ${STATUS}):" >&2
    cat "${LOG_FILE}" >&2
    rm -f "${LOG_FILE}"
    exit ${STATUS}
fi

# 3. 从启动日志中提取实际分配到的 display (兼容各种 TurboVNC / TigerVNC 输出)
TARGET_DISPLAY=$(grep -oE "desktop is [^:]+:[0-9]+" "${LOG_FILE}" | awk -F':' '{print $2}' | tail -n 1)

if [ -z "${TARGET_DISPLAY}" ]; then
    TARGET_DISPLAY=$(grep -oE "desktop [^:]+:[0-9]+" "${LOG_FILE}" | awk -F':' '{print $2}' | tail -n 1)
fi

if [ -z "${TARGET_DISPLAY}" ]; then
    echo "[ERROR] Unable to parse display number from VNC log:" >&2
    cat "${LOG_FILE}" >&2
    "${VNCSERVER_BIN}" -kill ":${TARGET_DISPLAY}" >/dev/null 2>&1
    rm -f "${LOG_FILE}"
    exit 1
fi

PORT=$((5900 + TARGET_DISPLAY))
HOSTNAME="$(hostname -f 2>/dev/null || hostname)"

# 4. 注册退出陷阱（无论仿真正常退出还是被 bkill/scancel 终止，均清理现场）
cleanup() {
    "${VNCSERVER_BIN}" -kill ":${TARGET_DISPLAY}" >/dev/null 2>&1
    rm -f "${LOG_FILE}"
}
trap cleanup EXIT INT TERM

# 6. 注入 DISPLAY 环境变量并前台阻塞运行目标命令
export DISPLAY=":${TARGET_DISPLAY}"
"$@"
