#!/usr/bin/env bash
# Design Cloud Engine - PostgreSQL 数据库与用户初始化脚本
unalias -a
set -euo pipefail

db_host=${db_host:-localhost}
db_port=${db_port:-5432}
db_name=${db_name:-dc}
db_user=${db_user:-dc}
user_password=${user_password:-}

if [ -z "${user_password}" ]; then
  echo "ERROR: 未设置数据库用户口令，请通过 user_password=实际密码 传入后重试。" >&2
  exit 1
fi

psql_bin="$(command -v psql || echo "/usr/bin/psql")"

case $(whoami) in
postgres)
  su_prefix=""
  ;;
root)
  su_prefix="su postgres -c"
  ;;
*)
  echo "ERROR: 请以 root 或 postgres 用户身份运行此脚本。" >&2
  exit 1
  ;;
esac

echo "PostgreSQL CLI: ${psql_bin}"
echo "正在创建/配置数据库用户 '${db_user}'..."

# 创建用户（如果不存在）
if [ -n "${su_prefix}" ]; then
  ${su_prefix} "createuser -d -l ${db_user}" 2>/dev/null || true
  ${su_prefix} "${psql_bin} -c \"ALTER USER ${db_user} WITH PASSWORD '${user_password}';\""
  ${su_prefix} "createdb -O ${db_user} ${db_name}" 2>/dev/null || true
else
  createuser -d -l "${db_user}" 2>/dev/null || true
  "${psql_bin}" -c "ALTER USER ${db_user} WITH PASSWORD '${user_password}';"
  createdb -O "${db_user}" "${db_name}" 2>/dev/null || true
fi

echo ""
echo "=== 数据库初始化完成 ==="
if [ -n "${su_prefix}" ]; then
  ${su_prefix} "${psql_bin} -c \"\\du ${db_user}\""
  ${su_prefix} "${psql_bin} -c \"\\l ${db_name}\""
else
  "${psql_bin}" -c "\\du ${db_user}"
  "${psql_bin}" -c "\\l ${db_name}"
fi

echo ""
echo "--- 生成 ~/.pgpass 文件条目建议 ---"
echo "${db_host}:${db_port}:${db_name}:${db_user}:${user_password}"
