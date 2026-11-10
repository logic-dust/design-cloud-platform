#!/bin/bash
# Shared utility functions for Design Cloud Engine test scripts
# Source this file with: source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

set -o pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# local_hostname 与引擎的 os.Hostname() 保持同源：Linux 上即内核 nodename（uname -n，POSIX、
# 无外部依赖）。hostname(1) 仅作兜底——它在 Arch 等未装 inetutils 的系统上不存在，且部分系统
# 会返回 FQDN，与 NodeID 语义不符（§3.10）。
local_hostname() {
  if uname -n 2>/dev/null; then return 0; fi
  if command -v hostname >/dev/null 2>&1; then hostname && return 0; fi
  cat /proc/sys/kernel/hostname 2>/dev/null || echo localhost
}

# Generate cluster CA + node certificate using openssl (replaces old gen-ca/gen-cert commands)
# Usage: prepare_cluster_certs <base_dir> [hostname]
# Output (permission matrix §3.2):
#   <base_dir>/certs/cluster/ca.crt                        (0755 dir / 0644 file)
#   <base_dir>/certs/cluster/ca/ca.key                     (0700 dir / 0600 file)
#   <base_dir>/certs/cluster/<hostname>/<hostname>.crt     (0700 dir / 0644 file)
#   <base_dir>/certs/cluster/<hostname>/<hostname>.key     (0700 dir / 0600 file)
prepare_cluster_certs() {
  local dir="$1"
  local hostname="${2:-$(local_hostname)}"

  local cluster_dir="${dir}/certs/cluster"
  local ca_dir="${cluster_dir}/ca"
  local node_dir="${cluster_dir}/${hostname}"
  mkdir -p "${ca_dir}" "${node_dir}"
  chmod 0755 "${cluster_dir}"
  chmod 0700 "${ca_dir}" "${node_dir}"

  # Generate CA (public key flat, private key nested)
  openssl req -new -newkey rsa:2048 -days 365 -nodes -x509 \
    -keyout "${ca_dir}/ca.key" -out "${cluster_dir}/ca.crt" \
    -subj "/CN=TestCA" 2>/dev/null
  chmod 0644 "${cluster_dir}/ca.crt"
  chmod 0600 "${ca_dir}/ca.key"

  # Generate node certificate into its own subdirectory
  openssl req -new -newkey rsa:2048 -days 365 -nodes \
    -keyout "${node_dir}/${hostname}.key" -out "${node_dir}/${hostname}.csr" \
    -subj "/CN=${hostname}" 2>/dev/null

  cat >"${node_dir}/ext.cnf" <<EOF
subjectAltName=DNS:${hostname},IP:127.0.0.1,DNS:localhost
EOF

  # -CAcreateserial 会按 CA 文件路径推导序列号文件，而 mktemp 目录名（/tmp/tmp.XXXX）含点号，
  # 推导结果会落到临时目录之外（/tmp/tmp.srl）——多用户/重复运行时互相抢该文件并永久泄漏到 /tmp。
  # 显式用 -CAserial 指定临时目录内的路径，避免跨用户冲突与 /tmp 污染。
  openssl x509 -req -days 365 -in "${node_dir}/${hostname}.csr" \
    -CA "${cluster_dir}/ca.crt" -CAkey "${ca_dir}/ca.key" -CAcreateserial \
    -CAserial "${ca_dir}/ca.srl" \
    -out "${node_dir}/${hostname}.crt" -extfile "${node_dir}/ext.cnf" 2>/dev/null

  chmod 0644 "${node_dir}/${hostname}.crt"
  chmod 0600 "${node_dir}/${hostname}.key"
  rm -f "${node_dir}/ext.cnf" "${node_dir}/${hostname}.csr"
}

# Generate a self-signed API/Portal certificate using openssl
# Usage: prepare_api_certs <base_dir>
# Output: <base_dir>/conf/certs/portal/{portal.crt,portal.key}
#
# SAN 走 -config 内的 x509_extensions，而不是 -addext：部署机（CentOS 7 / OpenSSL 1.0.2）没有
# -addext，会因参数不识别而静默不生成证书，导致启动报 portal.crt 缺失。这里与
# prepare_cluster_certs 用同一种"临时扩展配置"写法，1.0.2 与 3.x 通吃。
prepare_api_certs() {
  local dir="$1"
  local portal_dir="${dir}/conf/certs/portal"
  mkdir -p "${portal_dir}"

  local conf="${portal_dir}/gen.cnf"
  cat >"${conf}" <<EOF
[req]
prompt = no
distinguished_name = dn
x509_extensions = v3_req

[dn]
CN = 127.0.0.1

[v3_req]
subjectAltName = IP:127.0.0.1,DNS:localhost
basicConstraints = critical,CA:TRUE
EOF

  openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
    -keyout "${portal_dir}/portal.key" \
    -out "${portal_dir}/portal.crt" \
    -config "${conf}" >/dev/null 2>&1

  rm -f "${conf}"
}
