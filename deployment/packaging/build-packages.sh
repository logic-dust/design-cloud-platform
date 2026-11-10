#!/bin/bash
set -euo pipefail

# ==============================================================================
# build-packages.sh — build design-cloud-engine RPM/DEB (engine + dcexec lockstep)
#
# Usage:
#   ./build-packages.sh <engine-bin> <dcexec-bin> [version] [release] [pkg_type] [user] [group] [arch]
#   pkg_type: rpm | deb | all  (default: rpm — production is RHEL/CentOS)
#
# Note: gen-config always uses a host-native `go build` of cmd/engine so the
# packaged binary may be cross-compiled (GOARCH≠host) without exec-format errors.
# ==============================================================================

ENGINE_PATH="${1:?engine binary path required}"
EXEC_BIN_PATH="${2:?dc-exec binary path required}"
export VERSION="${3:-0.1.0}"
export PKG_RELEASE="${4:-1}"
PKG_TYPE="${5:-rpm}"
export PKG_USER="${6:-dce}"
export PKG_GROUP="${7:-dce}"
export PKG_ARCH="${8:-amd64}"
PACKAGE_NAME="design-cloud-engine"

# 智能拆解与容错兼容：
# 1) 若 VERSION 传了 "0.2.5-2"，拆分为 VERSION=0.2.5 与 PKG_RELEASE=2
# 2) 若调用方按旧签名调用（$4 为 rpm/deb/all），平滑识别回落
if [[ "${PKG_RELEASE}" == "rpm" || "${PKG_RELEASE}" == "deb" || "${PKG_RELEASE}" == "all" ]]; then
  PKG_ARCH="${PKG_GROUP:-amd64}"
  PKG_GROUP="${PKG_USER:-dce}"
  PKG_USER="${PKG_TYPE:-dce}"
  PKG_TYPE="${PKG_RELEASE}"
  if [[ "${VERSION}" == *-* ]]; then
    PKG_RELEASE="${VERSION#*-}"
    VERSION="${VERSION%%-*}"
  else
    PKG_RELEASE="1"
  fi
elif [[ "${VERSION}" == *-* ]]; then
  PKG_RELEASE="${VERSION#*-}"
  VERSION="${VERSION%%-*}"
fi

export ENGINE_ABS_PATH="$(realpath "${ENGINE_PATH}")"
export EXEC_BIN_ABS_PATH="$(realpath "${EXEC_BIN_PATH}")"
export SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
PROJECT_ROOT="$(realpath "${SCRIPT_DIR}/../..")"

TMP_NFPM_YAML=""
TMP_CONFIG_DIR=""
TMP_STAGING_DIR=""
TMP_HOST_ENGINE=""
TMP_UNIT=""
TMP_TMPFILES=""

cleanup() {
  rm -f "${TMP_NFPM_YAML}" "${TMP_HOST_ENGINE}" "${TMP_UNIT}" "${TMP_TMPFILES}"
  rm -rf "${TMP_CONFIG_DIR}" "${TMP_STAGING_DIR}"
}
trap cleanup EXIT

if [[ ! -f "${ENGINE_ABS_PATH}" ]]; then
  echo "ERROR: engine binary not found: ${ENGINE_PATH}" >&2
  exit 1
fi
if [[ ! -f "${EXEC_BIN_ABS_PATH}" ]]; then
  echo "ERROR: dc-exec binary not found: ${EXEC_BIN_PATH}" >&2
  exit 1
fi

UNIT_SRC="${PROJECT_ROOT}/deployment/systemd/design-cloud-engine.service"
if [[ ! -f "${UNIT_SRC}" ]]; then
  echo "ERROR: systemd unit missing: ${UNIT_SRC}" >&2
  exit 1
fi

if ! command -v nfpm >/dev/null 2>&1; then
  echo "ERROR: nfpm is required (single packaging authority)." >&2
  echo "Install: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest" >&2
  exit 1
fi

OUTPUT_DIR="${PROJECT_ROOT}/build"
mkdir -p "${OUTPUT_DIR}"

# --- Render unit + tmpfiles for PKG_USER/GROUP (canonical sources stay dce) ---
TMP_UNIT="$(mktemp /tmp/dce-unit.XXXXXX.service)"
sed -e "s/^User=dce$/User=${PKG_USER}/" \
    -e "s/^Group=dce$/Group=${PKG_GROUP}/" \
  "${UNIT_SRC}" >"${TMP_UNIT}"
export UNIT_ABS_PATH="$(realpath "${TMP_UNIT}")"

TMP_TMPFILES="$(mktemp /tmp/dce-tmpfiles.XXXXXX.conf)"
sed -e "s/__PKG_USER__/${PKG_USER}/g" -e "s/__PKG_GROUP__/${PKG_GROUP}/g" \
  "${SCRIPT_DIR}/design-cloud-engine.tmpfiles" >"${TMP_TMPFILES}"
export TMPFILES_ABS_PATH="$(realpath "${TMP_TMPFILES}")"

export CONFIGLESS_EXAMPLE_ABS_PATH="$(realpath "${SCRIPT_DIR}/configless.conf.example")"
export VNC_WRAPPER_ABS_PATH="$(realpath "${PROJECT_ROOT}/deployment/share/vnc/job-gui-wrapper.sh")"
export LOGROTATE_ABS_PATH="$(realpath "${SCRIPT_DIR}/logrotate.design-cloud-engine")"

embed_install_script() {
  local src="$1" dst="$2"
  {
    head -n 1 "${src}"
    cat "${PROJECT_ROOT}/deployment/lib/install-common.sh"
    tail -n +2 "${src}"
  } >"${dst}"
}

# --- Render install scripts with service identity in temporary staging dir ---
TMP_STAGING_DIR="$(mktemp -d /tmp/dce-pkg-staging.XXXXXX)"

embed_install_script "${SCRIPT_DIR}/pre-install.sh" "${TMP_STAGING_DIR}/pre-install.sh"
embed_install_script "${SCRIPT_DIR}/post-install.sh" "${TMP_STAGING_DIR}/post-install.sh"
embed_install_script "${SCRIPT_DIR}/pre-remove.sh" "${TMP_STAGING_DIR}/pre-remove.sh"
sed -i -e "s/__PKG_USER__/${PKG_USER}/g" -e "s/__PKG_GROUP__/${PKG_GROUP}/g" \
  "${TMP_STAGING_DIR}/pre-install.sh" "${TMP_STAGING_DIR}/post-install.sh"
cp "${SCRIPT_DIR}/post-trans.sh" "${TMP_STAGING_DIR}/post-trans.sh"
chmod +x "${TMP_STAGING_DIR}/pre-install.sh" "${TMP_STAGING_DIR}/post-install.sh" "${TMP_STAGING_DIR}/pre-remove.sh" "${TMP_STAGING_DIR}/post-trans.sh"
export PRE_INSTALL_SCRIPT="$(realpath "${TMP_STAGING_DIR}/pre-install.sh")"
export POST_INSTALL_SCRIPT="$(realpath "${TMP_STAGING_DIR}/post-install.sh")"
export PRE_REMOVE_SCRIPT="$(realpath "${TMP_STAGING_DIR}/pre-remove.sh")"
export POST_TRANS_SCRIPT="$(realpath "${TMP_STAGING_DIR}/post-trans.sh")"
TMP_CONFIG_DIR="$(mktemp -d /tmp/dce-pkg-config.XXXXXX)"
TMP_HOST_ENGINE="$(mktemp /tmp/dce-gen-config.XXXXXX)"
(
  cd "${PROJECT_ROOT}"
  # Clear cross-compile env so gen-config binary matches the build host.
  GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" CGO_ENABLED=0 \
    go build -o "${TMP_HOST_ENGINE}" ./cmd/engine
)
(
  cd "${TMP_CONFIG_DIR}"
  "${TMP_HOST_ENGINE}" gen-config
)
HEADER='# Design Cloud Engine configuration template (packaged).
# Edit topology / database / LDAP before: systemctl enable --now design-cloud-engine
# Secret files referenced by password_file paths are NOT shipped — create them yourself.
# Upgrades use %config(noreplace): your edits are kept; new templates land as .rpmnew.
#
'
{
  printf '%s\n' "${HEADER}"
  cat "${TMP_CONFIG_DIR}/config.example.toml"
} >"${TMP_STAGING_DIR}/config.toml"
chmod 0600 "${TMP_STAGING_DIR}/config.toml"
export CONFIG_ABS_PATH="$(realpath "${TMP_STAGING_DIR}/config.toml")"

echo "Building ${PACKAGE_NAME} v${VERSION}-${PKG_RELEASE} (${PKG_TYPE}) arch=${PKG_ARCH} as ${PKG_USER}:${PKG_GROUP} ..."
echo "  engine:   ${ENGINE_ABS_PATH}"
echo "  dc-exec: ${EXEC_BIN_ABS_PATH}"

build_nfpm() {
  local packager="$1"
  echo "--- nfpm packager=${packager} ---"
  TMP_NFPM_YAML="$(mktemp /tmp/nfpm-XXXXXX.yaml)"
  envsubst <"${SCRIPT_DIR}/nfpm.yaml" >"${TMP_NFPM_YAML}"
  (cd "${OUTPUT_DIR}" && nfpm pkg --config "${TMP_NFPM_YAML}" --packager "${packager}")
  rm -f "${TMP_NFPM_YAML}"
  TMP_NFPM_YAML=""
}

case "${PKG_TYPE}" in
  rpm) build_nfpm rpm ;;
  deb) build_nfpm deb ;;
  all)
    build_nfpm rpm
    build_nfpm deb
    ;;
  *)
    echo "ERROR: invalid pkg_type '${PKG_TYPE}' (rpm|deb|all)" >&2
    exit 1
    ;;
esac

echo ""
echo "Done. Packages in ${OUTPUT_DIR}/"
ls -lh "${OUTPUT_DIR}"/*.rpm "${OUTPUT_DIR}"/*.deb 2>/dev/null || ls -lh "${OUTPUT_DIR}"/
