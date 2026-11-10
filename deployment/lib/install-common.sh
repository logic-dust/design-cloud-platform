# Shared helpers for deployment/install.sh and RPM/DEB scriptlets.
# Sourced or embedded by packaging/build-packages.sh — do not execute directly.

dce_apply_exec_setcap() {
  local dc-exec_bin="$1"
  if [[ ! -f "${dcexec_bin}" ]]; then
    return 0
  fi
  if command -v setcap >/dev/null 2>&1; then
    setcap cap_setuid,cap_setgid+ep "${dcexec_bin}"
  elif [[ -x /sbin/setcap ]]; then
    /sbin/setcap cap_setuid,cap_setgid+ep "${dcexec_bin}"
  elif [[ -x /usr/sbin/setcap ]]; then
    /usr/sbin/setcap cap_setuid,cap_setgid+ep "${dcexec_bin}"
  else
    echo "WARNING: setcap not found; ${dcexec_bin} will fail L0 capability check." >&2
    return 1
  fi
}

dce_ensure_service_user() {
  local user="$1"
  local group="$2"
  getent group "${group}" >/dev/null || groupadd -r "${group}"
  getent passwd "${user}" >/dev/null ||
    useradd -r -M -g "${group}" -s /sbin/nologin -c "Design Cloud Engine" "${user}"
}

dce_systemd_tmpfiles_create() {
  if command -v systemd-tmpfiles >/dev/null 2>&1; then
    systemd-tmpfiles --create /usr/lib/tmpfiles.d/design-cloud-engine.conf 2>/dev/null ||
      systemd-tmpfiles --create 2>/dev/null || true
  fi
}
