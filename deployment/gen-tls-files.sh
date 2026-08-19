#!/bin/bash
set -euo pipefail

# =============================================================================
# 全局变量 - 可通过命令行参数覆盖
# =============================================================================

OUT_DIR="conf/certs/portal"
HOSTS="localhost,127.0.0.1"
DAYS=3650
COUNTRY="CN"
STATE="GuangDong"
LOCALITY="ShenZhen"
ORG="Design Cloud Engine"
ORG_UNIT="Web"
CA_PREFIX="portal.ca"
SIGNED_PREFIX="portal"
FORCE=0

# =============================================================================
# main() 函数 - 程序入口
# =============================================================================

main() {
  parse_args "$@"
  ensure_openssl
  ensure_output_dir
  ensure_not_overwriting

  echo "Generating portal TLS files in: ${OUT_DIR}"
  echo "Hosts: ${HOSTS}"

  generate_certs

  cat <<EOF

Done:
  CA cert    : ${OUT_DIR}/${CA_PREFIX}.crt
  CA key     : ${OUT_DIR}/${CA_PREFIX}.key
  Server cert: ${OUT_DIR}/${SIGNED_PREFIX}.crt
  Server key : ${OUT_DIR}/${SIGNED_PREFIX}.key

Use these paths with portal.cert / portal.key when portal.tls_enabled=true.
EOF
}

# =============================================================================
# 辅助函数
# =============================================================================

usage() {
  cat <<EOF
Usage: ./deployment/gen-tls-files.sh [options]

Options:
  --out-dir PATH        Output directory. Default: conf/certs/portal
  --hosts LIST          Comma-separated DNS/IP SAN list. Default: localhost,127.0.0.1
  --days N              Certificate validity days. Default: 3650
  --country CODE        Subject country. Default: CN
  --state NAME          Subject state/province. Default: GuangDong
  --locality NAME       Subject locality. Default: ShenZhen
  --org NAME            Subject organization. Default: Design Cloud Engine
  --org-unit NAME       Subject organizational unit. Default: Web
  --ca-prefix NAME      CA file prefix. Default: portal.ca
  --signed-prefix NAME  Server cert/key prefix. Default: portal
  --force               Overwrite existing files
  --help                Show this message

Example:
  ./deployment/gen-tls-files.sh \\
    --out-dir conf/certs/portal \\
    --hosts dc.example.com,10.0.0.10,localhost,127.0.0.1
EOF
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --out-dir)
      OUT_DIR="$2"
      shift 2
      ;;
    --hosts)
      HOSTS="$2"
      shift 2
      ;;
    --days)
      DAYS="$2"
      shift 2
      ;;
    --country)
      COUNTRY="$2"
      shift 2
      ;;
    --state)
      STATE="$2"
      shift 2
      ;;
    --locality)
      LOCALITY="$2"
      shift 2
      ;;
    --org)
      ORG="$2"
      shift 2
      ;;
    --org-unit)
      ORG_UNIT="$2"
      shift 2
      ;;
    --ca-prefix)
      CA_PREFIX="$2"
      shift 2
      ;;
    --signed-prefix)
      SIGNED_PREFIX="$2"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --help | -h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
    esac
  done
}

ensure_openssl() {
  command -v openssl >/dev/null 2>&1 || {
    echo "openssl not found" >&2
    exit 1
  }
}

ensure_output_dir() {
  mkdir -p "${OUT_DIR}"
  chmod 700 "${OUT_DIR}"
}

ensure_not_overwriting() {
  if [[ ${FORCE} -eq 1 ]]; then
    return
  fi

  local files=(
    "${OUT_DIR}/${CA_PREFIX}.key"
    "${OUT_DIR}/${CA_PREFIX}.crt"
    "${OUT_DIR}/${SIGNED_PREFIX}.key"
    "${OUT_DIR}/${SIGNED_PREFIX}.crt"
  )
  for file in "${files[@]}"; do
    if [[ -e "${file}" ]]; then
      echo "Refusing to overwrite existing file without --force: ${file}" >&2
      exit 1
    fi
  done
}

write_extfile() {
  local extfile="$1"
  local host_array
  IFS=',' read -r -a host_array <<<"${HOSTS}"

  {
    echo "authorityKeyIdentifier=keyid,issuer"
    echo "basicConstraints=CA:FALSE"
    echo "keyUsage=digitalSignature,nonRepudiation,keyEncipherment,dataEncipherment"
    echo "extendedKeyUsage=serverAuth"
    echo "subjectAltName=@alt_names"
    echo
    echo "[alt_names]"

    local dns_idx=1
    local ip_idx=1
    local host
    for host in "${host_array[@]}"; do
      if [[ "${host}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ || "${host}" =~ : ]]; then
        echo "IP.${ip_idx} = ${host}"
        ip_idx=$((ip_idx + 1))
      else
        echo "DNS.${dns_idx} = ${host}"
        dns_idx=$((dns_idx + 1))
      fi
    done
  } >"${extfile}"
}

generate_certs() {
  local extfile csr
  extfile="${OUT_DIR}/${SIGNED_PREFIX}.ext"
  csr="${OUT_DIR}/${SIGNED_PREFIX}.csr"
  local common_name="${HOSTS%%,*}"

  openssl genrsa -out "${OUT_DIR}/${CA_PREFIX}.key" 4096
  chmod 600 "${OUT_DIR}/${CA_PREFIX}.key"

  openssl req -utf8 -x509 -new -nodes \
    -key "${OUT_DIR}/${CA_PREFIX}.key" \
    -sha256 \
    -days "${DAYS}" \
    -out "${OUT_DIR}/${CA_PREFIX}.crt" \
    -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=${ORG} CA"
  chmod 600 "${OUT_DIR}/${CA_PREFIX}.crt"

  openssl genrsa -out "${OUT_DIR}/${SIGNED_PREFIX}.key" 4096
  chmod 600 "${OUT_DIR}/${SIGNED_PREFIX}.key"

  openssl req -new \
    -key "${OUT_DIR}/${SIGNED_PREFIX}.key" \
    -out "${csr}" \
    -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=${common_name}"

  write_extfile "${extfile}"

  openssl x509 -req \
    -in "${csr}" \
    -CA "${OUT_DIR}/${CA_PREFIX}.crt" \
    -CAkey "${OUT_DIR}/${CA_PREFIX}.key" \
    -CAcreateserial \
    -out "${OUT_DIR}/${SIGNED_PREFIX}.crt" \
    -days "${DAYS}" \
    -sha256 \
    -extfile "${extfile}"
  chmod 600 "${OUT_DIR}/${SIGNED_PREFIX}.crt"

  rm -f "${csr}" "${extfile}" "${OUT_DIR}/${CA_PREFIX}.srl"
}

# =============================================================================
# 程序入口
# =============================================================================

main "$@"
