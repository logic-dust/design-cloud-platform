#!/bin/bash
set -e

TARGET_DIR="/opt/design-cloud"
ENGINE_BIN="${TARGET_DIR}/design-cloud-engine"
LAUNCHER_BIN="${TARGET_DIR}/dc-launcher"
PKG_USER="__PKG_USER__"
PKG_GROUP="__PKG_GROUP__"

# Install root stays root-owned; binaries owned by the service account.
chown root:root "${TARGET_DIR}"
chmod 0755 "${TARGET_DIR}"

if [[ -f "${ENGINE_BIN}" ]]; then
  chown "${PKG_USER}:${PKG_GROUP}" "${ENGINE_BIN}"
  chmod 0755 "${ENGINE_BIN}"
fi

if [[ -f "${LAUNCHER_BIN}" ]]; then
  chown "${PKG_USER}:${PKG_GROUP}" "${LAUNCHER_BIN}"
  chmod 0700 "${LAUNCHER_BIN}"

  # RPM file replacement drops security.capability xattr — always re-apply.
  if command -v setcap >/dev/null 2>&1; then
    setcap cap_setuid,cap_setgid+ep "${LAUNCHER_BIN}"
  elif [[ -x /sbin/setcap ]]; then
    /sbin/setcap cap_setuid,cap_setgid+ep "${LAUNCHER_BIN}"
  elif [[ -x /usr/sbin/setcap ]]; then
    /usr/sbin/setcap cap_setuid,cap_setgid+ep "${LAUNCHER_BIN}"
  else
    echo "WARNING: setcap not found; ${LAUNCHER_BIN} will fail L0 capability check."
  fi
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
if command -v systemd-tmpfiles >/dev/null 2>&1; then
  systemd-tmpfiles --create /usr/lib/tmpfiles.d/design-cloud-engine.conf 2>/dev/null || true
fi

exit 0
