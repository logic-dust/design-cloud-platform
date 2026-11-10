#!/usr/bin/env bash
# Design Cloud Engine — PostgreSQL 阶梯式 SQL 迁移执行器
# 从指定源版本 (--from) 开始，按序号依次串联执行迁移脚本，实现一步步递进升级。
unalias -a
set -euo pipefail

SCRIPT_CMD="$0"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FROM_VERSION=""
PG_DSN=""
PGPASS_ARG=""
PASSWORD_MODE="auto" # auto | prompt | never
DRY_RUN=0

usage() {
  cat <<EOF
用法:
  ${SCRIPT_CMD} --from <version> [选项]

选项:
  --from <version>   当前数据库所处源版本，例如 0.1.x 或 0.1.21（必填）
  --dsn <pg-dsn>     PostgreSQL 连接串（省略时沿用 psql 标准环境变量 PGHOST/PGPORT/PGUSER/PGDATABASE 等）
  --pgpass <file>    指定 pgpass 文件路径（默认读取 \$PGPASSFILE 或 ~/.pgpass，要求权限为 0600）
  -W, --password     强制交互式输入数据库密码（仅输入一次，供全部迁移步骤复用）
  -w, --no-password  禁止交互式密码提示（仅使用 PGPASSWORD、DSN 密码、~/.pgpass 或免密认证）
  --dry-run          仅打印将按序执行的迁移脚本，不实际执行
  -h, --help         显示帮助

认证方式（优先级：PGPASSWORD / DSN 内嵌密码 > ~/.pgpass / PGPASSFILE > 交互式输入）:
  1. ~/.pgpass（或 --pgpass / PGPASSFILE）：psql 原生支持，文件权限需为 0600
  2. 交互式输入：传入 -W/--password，或未配置 ~/.pgpass/PGPASSWORD 且服务端要求密码时自动提示一次
  3. 环境变量：PGPASSWORD=xxx

示例:
  # 使用 ~/.pgpass 或按需交互式输入密码，从 0.1.x 升级到 0.2.x
  ${SCRIPT_CMD} --dsn "postgresql://dce@localhost:5432/dce" --from 0.1.x

  # 强制交互式输入密码（使用 PGHOST/PGUSER/PGDATABASE 环境变量）
  PGHOST=localhost PGUSER=dce PGDATABASE=dce ${SCRIPT_CMD} --from 0.1.21 -W

  # 指定自定义 pgpass 文件并禁止交互提示
  ${SCRIPT_CMD} --dsn "postgresql://dce@localhost:5432/dce" --from 0.1.x --pgpass ~/.pgpass -w
EOF
}

require_arg() {
  local opt="$1"
  local val="${2:-}"
  if [[ -z "${val}" ]]; then
    echo "ERROR: 选项 ${opt} 缺少参数值" >&2
    usage >&2
    exit 2
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --from)
      require_arg "$1" "${2:-}"
      FROM_VERSION="$2"
      shift 2
      ;;
    --dsn)
      require_arg "$1" "${2:-}"
      PG_DSN="$2"
      shift 2
      ;;
    --pgpass)
      require_arg "$1" "${2:-}"
      PGPASS_ARG="$2"
      shift 2
      ;;
    -W|--password)
      PASSWORD_MODE="prompt"
      shift
      ;;
    -w|--no-password)
      PASSWORD_MODE="never"
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: 未知参数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${FROM_VERSION}" ]]; then
  echo "ERROR: 必须指定 --from <version>" >&2
  usage >&2
  exit 2
fi

FROM_VERSION="${FROM_VERSION#v}"

mapfile -t ALL_SCRIPTS < <(find "${SCRIPT_DIR}" -maxdepth 1 -type f -name '[0-9][0-9][0-9]_from_*_to_*.sql' | sort)
if [[ ${#ALL_SCRIPTS[@]} -eq 0 ]]; then
  echo "ERROR: 未在 ${SCRIPT_DIR} 找到迁移脚本" >&2
  exit 1
fi

matches_step_from() {
  local requested="$1"
  local step_from="$2"
  if [[ "${requested}" == "${step_from}" ]]; then
    return 0
  fi
  if [[ "${step_from}" == *.x ]]; then
    local prefix="${step_from%.x}."
    if [[ "${requested}" == "${prefix}"* ]]; then
      return 0
    fi
  fi
  return 1
}

TO_RUN=()
FOUND=0
for f in "${ALL_SCRIPTS[@]}"; do
  base="$(basename "$f")"
  step_from="${base#*_from_}"
  step_from="${step_from%%_to_*}"
  if [[ ${FOUND} -eq 1 ]] || matches_step_from "${FROM_VERSION}" "${step_from}"; then
    FOUND=1
    TO_RUN+=("$f")
  fi
done

if [[ ${FOUND} -eq 0 ]]; then
  echo "ERROR: 未找到源版本 '${FROM_VERSION}' 对应的迁移起点（可用起点如下）：" >&2
  for f in "${ALL_SCRIPTS[@]}"; do
    echo "  - $(basename "$f")" >&2
  done
  exit 1
fi

echo "=== PostgreSQL Migration Plan (from ${FROM_VERSION}) ==="
for f in "${TO_RUN[@]}"; do
  echo "  -> $(basename "$f")"
done

if [[ ${DRY_RUN} -eq 1 ]]; then
  echo "(dry-run mode, no SQL executed)"
  exit 0
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "ERROR: 未找到 psql 命令，请先安装 postgresql 客户端工具" >&2
  exit 1
fi

check_pgpass_permissions() {
  local pgpass_file="$1"
  [[ -f "${pgpass_file}" ]] || return 0
  local perm=""
  perm="$(stat -c '%a' "${pgpass_file}" 2>/dev/null || stat -f '%Lp' "${pgpass_file}" 2>/dev/null || true)"
  if [[ -n "${perm}" ]] && (( (8#${perm} & 8#077) != 0 )); then
    echo "WARNING: ${pgpass_file} 权限为 ${perm}，psql 将忽略组/其他用户可读的 pgpass 文件；建议执行: chmod 600 ${pgpass_file}" >&2
  fi
}

if [[ -n "${PGPASS_ARG}" ]]; then
  if [[ ! -f "${PGPASS_ARG}" || ! -r "${PGPASS_ARG}" ]]; then
    echo "ERROR: pgpass 文件不存在或不可读: ${PGPASS_ARG}" >&2
    exit 1
  fi
  export PGPASSFILE="${PGPASS_ARG}"
  check_pgpass_permissions "${PGPASSFILE}"
else
  effective_pgpass="${PGPASSFILE:-${HOME:-}/.pgpass}"
  if [[ -n "${effective_pgpass}" ]]; then
    check_pgpass_permissions "${effective_pgpass}"
  fi
fi

has_dsn_password() {
  local dsn="$1"
  [[ "${dsn}" =~ ^[a-zA-Z0-9+.-]+://[^/:@]+:[^@]*@ ]] || [[ "${dsn}" =~ (^|[[:space:]])password= ]]
}

prompt_password() {
  local pw=""
  if [[ -r /dev/tty ]]; then
    printf '请输入 PostgreSQL 密码: ' >&2
    if ! IFS= read -r -s pw </dev/tty; then
      printf '\n' >&2
      echo "ERROR: 读取密码被取消" >&2
      exit 1
    fi
    printf '\n' >&2
  elif [[ -t 0 ]]; then
    printf '请输入 PostgreSQL 密码: ' >&2
    if ! IFS= read -r -s pw; then
      printf '\n' >&2
      echo "ERROR: 读取密码被取消" >&2
      exit 1
    fi
    printf '\n' >&2
  else
    echo "ERROR: 当前为非交互终端，无法提示输入密码；请使用 PGPASSWORD 或配置 ~/.pgpass (chmod 600)" >&2
    exit 1
  fi
  export PGPASSWORD="${pw}"
}

run_psql() {
  if [[ -n "${PG_DSN}" ]]; then
    psql "${PG_DSN}" "$@"
  else
    psql "$@"
  fi
}

case "${PASSWORD_MODE}" in
  prompt)
    prompt_password
    if ! err_out="$(run_psql -w -v ON_ERROR_STOP=1 -c '\q' 2>&1)"; then
      echo "${err_out}" >&2
      exit 1
    fi
    ;;
  never)
    if ! err_out="$(run_psql -w -v ON_ERROR_STOP=1 -c '\q' 2>&1)"; then
      echo "${err_out}" >&2
      exit 1
    fi
    ;;
  auto)
    if [[ -z "${PGPASSWORD:-}" ]] && ! has_dsn_password "${PG_DSN}"; then
      if ! err_out="$(run_psql -w -v ON_ERROR_STOP=1 -c '\q' 2>&1)"; then
        if grep -qiE 'no password supplied|password authentication failed|fe_sendauth' <<<"${err_out}"; then
          echo "INFO: 未通过环境变量或 ~/.pgpass 完成认证，转为交互式输入密码。" >&2
          prompt_password
          if ! err_out2="$(run_psql -w -v ON_ERROR_STOP=1 -c '\q' 2>&1)"; then
            echo "${err_out2}" >&2
            exit 1
          fi
        else
          echo "${err_out}" >&2
          exit 1
        fi
      fi
    fi
    ;;
esac

for f in "${TO_RUN[@]}"; do
  echo "[RUNNING] $(basename "$f") ..."
  run_psql -w -v ON_ERROR_STOP=1 -f "$f"
done

echo "=== All ${#TO_RUN[@]} migration step(s) completed successfully! ==="
