#!/bin/bash
# posttrans: runs after the full RPM transaction (including file install).
set -e

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
  # Restart only if already active — never enable on install.
  if systemctl is-active --quiet design-cloud-engine.service 2>/dev/null; then
    systemctl try-restart design-cloud-engine.service 2>/dev/null || true
  fi
fi

exit 0
