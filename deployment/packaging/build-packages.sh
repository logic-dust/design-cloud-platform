#!/bin/bash
set -euo pipefail

# ==============================================================================
# build-packages.sh — build design-cloud-engine RPM/DEB (engine + launcher lockstep)
#
# Usage:
#   ./build-packages.sh <engine-bin> <launcher-bin> [version] [pkg_type] [user] [group] [arch]
#   pkg_type: rpm | deb | all  (default: rpm — production is RHEL/CentOS)
#
# Note: gen-config always uses a host-native `go build` of cmd/engine so the
# packaged binary may be cross-compiled (GOARCH≠host) without exec-format errors.
# ==============================================================================

ENGINE_PATH="${1:?engine binary path required}"
LAUNCHER_PATH="${2:?launcher binary path required}"
export VERSION="${3:-0.1.0}"
PKG_TYPE="${4:-rpm}"
export PKG_USER="${5:-design-cloud}"
export PKG_GROUP="${6:-design-cloud}"
export PKG_ARCH="${7:-amd64}"
PACKAGE_NAME="design-cloud-engine"

export ENGINE_ABS_PATH="$(realpath "${ENGINE_PATH}")"
export LAUNCHER_ABS_PATH="$(realpath "${LAUNCHER_PATH}")"
export SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
PROJECT_ROOT="$(realpath "${SCRIPT_DIR}/../..")"

TMP_NFPM_YAML=""
TMP_CONFIG_DIR=""
TMP_HOST_ENGINE=""
TMP_UNIT=""
TMP_TMPFILES=""

cleanup() {
  rm -f "${TMP_NFPM_YAML}" "${TMP_HOST_ENGINE}" "${TMP_UNIT}" "${TMP_TMPFILES}"
  rm -rf "${TMP_CONFIG_DIR}"
}
trap cleanup EXIT

if [[ ! -f "${ENGINE_ABS_PATH}" ]]; then
  echo "ERROR: engine binary not found: ${ENGINE_PATH}" >&2
  exit 1
fi
if [[ ! -f "${LAUNCHER_ABS_PATH}" ]]; then
  echo "ERROR: launcher binary not found: ${LAUNCHER_PATH}" >&2
  exit 1
fi

UNIT_SRC="${PROJECT_ROOT}/deployment/design-cloud-engine.service"
if [[ ! -f "${UNIT_SRC}" ]]; then
  echo "ERROR: systemd unit missing: ${UNIT_SRC}" >&2
  exit 1
fi

if ! command -v nfpm >/dev/null 2>&1; then
  echo "ERROR: nfpm is required (single packaging authority)." >&2
  echo "Install: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest" >&2
  exit 1
fi

OUTPUT_DIR="${PROJECT_ROOT}/build/packages"
mkdir -p "${OUTPUT_DIR}"

# --- Render install scripts with service identity ---
sed -e "s/__PKG_USER__/${PKG_USER}/g" -e "s/__PKG_GROUP__/${PKG_GROUP}/g" \
  "${SCRIPT_DIR}/pre-install.sh" >"${OUTPUT_DIR}/pre-install.sh"
sed -e "s/__PKG_USER__/${PKG_USER}/g" -e "s/__PKG_GROUP__/${PKG_GROUP}/g" \
  "${SCRIPT_DIR}/post-install.sh" >"${OUTPUT_DIR}/post-install.sh"
cp "${SCRIPT_DIR}/post-trans.sh" "${OUTPUT_DIR}/post-trans.sh"
chmod +x "${OUTPUT_DIR}/pre-install.sh" "${OUTPUT_DIR}/post-install.sh" "${OUTPUT_DIR}/post-trans.sh"
export PRE_INSTALL_SCRIPT="$(realpath "${OUTPUT_DIR}/pre-install.sh")"
export POST_INSTALL_SCRIPT="$(realpath "${OUTPUT_DIR}/post-install.sh")"
export POST_TRANS_SCRIPT="$(realpath "${OUTPUT_DIR}/post-trans.sh")"

# --- Render unit + tmpfiles for PKG_USER/GROUP (canonical sources stay design-cloud) ---
TMP_UNIT="$(mktemp /tmp/dce-unit.XXXXXX.service)"
sed -e "s/^User=design-cloud$/User=${PKG_USER}/" \
    -e "s/^Group=design-cloud$/Group=${PKG_GROUP}/" \
  "${UNIT_SRC}" >"${TMP_UNIT}"
export UNIT_ABS_PATH="$(realpath "${TMP_UNIT}")"

TMP_TMPFILES="$(mktemp /tmp/dce-tmpfiles.XXXXXX.conf)"
sed -e "s/__PKG_USER__/${PKG_USER}/g" -e "s/__PKG_GROUP__/${PKG_GROUP}/g" \
  "${SCRIPT_DIR}/design-cloud-engine.tmpfiles" >"${TMP_TMPFILES}"
export TMPFILES_ABS_PATH="$(realpath "${TMP_TMPFILES}")"

export CONFIGLESS_EXAMPLE_ABS_PATH="$(realpath "${SCRIPT_DIR}/configless.conf.example")"

# --- Generate /etc config template (host-native engine; may differ from PKG_ARCH) ---
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
} >"${OUTPUT_DIR}/config.toml"
chmod 0600 "${OUTPUT_DIR}/config.toml"
export CONFIG_ABS_PATH="$(realpath "${OUTPUT_DIR}/config.toml")"

echo "Building ${PACKAGE_NAME} v${VERSION} (${PKG_TYPE}) arch=${PKG_ARCH} as ${PKG_USER}:${PKG_GROUP} ..."
echo "  engine:   ${ENGINE_ABS_PATH}"
echo "  launcher: ${LAUNCHER_ABS_PATH}"

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
