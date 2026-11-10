#!/bin/bash
set -e

TARGET_DIR="/opt/dce"
ENGINE_BIN="${TARGET_DIR}/dc-engine"
EXEC_BIN="${TARGET_DIR}/dc-exec"
PKG_USER="__PKG_USER__"
PKG_GROUP="__PKG_GROUP__"

# Install root stays root-owned; binaries owned by the service account.
chown root:root "${TARGET_DIR}"
chmod 0755 "${TARGET_DIR}"

if [[ -f "${ENGINE_BIN}" ]]; then
  chown "${PKG_USER}:${PKG_GROUP}" "${ENGINE_BIN}"
  chmod 0755 "${ENGINE_BIN}"
fi

if [[ -f "${EXEC_BIN}" ]]; then
  chown "${PKG_USER}:${PKG_GROUP}" "${EXEC_BIN}"
  chmod 0700 "${EXEC_BIN}"

  # RPM file replacement drops security.capability xattr — always re-apply.
  dce_apply_exec_setcap "${EXEC_BIN}" || true
fi

# Known data paths: if they exist from a previous install under another owner,
# reclaim for the packaged service account (not a general migration tool).
for path in /etc/design-cloud /var/lib/design-cloud /var/log/design-cloud /run/design-cloud; do
  if [[ -e "${path}" ]]; then
    chown -R "${PKG_USER}:${PKG_GROUP}" "${path}" || true
  fi
done

# Config template must stay 0600 (may contain credential path hints).
if [[ -f /etc/design-cloud/config.toml ]]; then
  chown "${PKG_USER}:${PKG_GROUP}" /etc/design-cloud/config.toml
  chmod 0600 /etc/design-cloud/config.toml
fi

# Apply tmpfiles (creates /run/design-cloud etc. when systemd-tmpfiles exists).
dce_systemd_tmpfiles_create

exit 0
