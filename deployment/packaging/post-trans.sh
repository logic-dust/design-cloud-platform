#!/bin/bash
# posttrans: runs after the full RPM transaction (including file install).
set -e

EXEC_BIN="/opt/design-cloud/dc-exec"
if [[ -f "${EXEC_BIN}" ]]; then
  dce_apply_exec_setcap "${EXEC_BIN}" || {
    echo "ERROR: post-trans setcap failed for ${EXEC_BIN}." >&2
    exit 1
  }
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
  # Restart only if already active — never enable on install.
  if systemctl is-active --quiet design-cloud-engine.service 2>/dev/null; then
    systemctl try-restart design-cloud-engine.service 2>/dev/null || true
  fi
fi

exit 0
