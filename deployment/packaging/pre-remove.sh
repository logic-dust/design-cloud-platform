#!/bin/bash
set -e

SERVICE_NAME="design-cloud-engine"

# Only stop and disable service on complete uninstall/removal.
# In RPM: $1 == 0 on erase/uninstall; $1 >= 1 on upgrade.
# In DEB: $1 == "remove" on uninstall; $1 == "upgrade" on upgrade.
is_uninstall=0
if [[ "$1" == "0" || "$1" == "remove" ]]; then
  is_uninstall=1
fi

if [[ "${is_uninstall}" -eq 1 ]]; then
  if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
      systemctl stop "${SERVICE_NAME}" || true
    fi
    if systemctl is-enabled --quiet "${SERVICE_NAME}" 2>/dev/null; then
      systemctl disable "${SERVICE_NAME}" || true
    fi
    systemctl daemon-reload 2>/dev/null || true
  fi
fi

exit 0
