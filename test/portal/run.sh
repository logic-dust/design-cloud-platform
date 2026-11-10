#!/usr/bin/env bash
# Portal API 端到端：默认 .vscode/remote.sh build；--push 为本地编译上传；再跑 run_http_tests.py。
#
#   test/portal/run.sh              # remote.sh build + 全量 API 测试（默认 env）
#   test/portal/run.sh --push       # 本地编译上传：remote.sh push
#   test/portal/run.sh --no-build   # 仅测试（远程已就绪）
#   test/portal/run.sh --no-build -- --domain 16-settings-system-admin
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

DEFAULT_ENV="dev-ldap-hpcadmin"
# deploy: build (default) | push | skip
DEPLOY_MODE="build"
ENV_NAME="${DEFAULT_ENV}"
PY_ARGS=()

PIPELINE_START=0
PIPELINE_ACTIVE=0
BUILD_STATUS="skipped"
HTTP_EXIT=""

usage() {
  cat <<'EOF'
用法: test/portal/run.sh [选项] [-- run_http_tests.py 额外参数...]

选项:
  (默认)           bash .vscode/remote.sh build（远程编译，同 VS Code「remote: build」）
  --push           bash .vscode/remote.sh push（本地编译 dc-exec+engine 再上传）
  --build          同默认，显式 remote.sh build
  --no-build       跳过部署，直接测 API
  --env NAME       http-client.env.json 中的环境名（默认: dev-ldap-hpcadmin）
  -h, --help       显示本帮助

随后对 http-client.env.json 里对应 addr 跑 python3 test/portal/run_http_tests.py。
结束时打印 pipeline 汇总；HTTP 详情见 run_http_tests.py 的 RUN SUMMARY。
EOF
}

print_pipeline_summary() {
  if [ "${PIPELINE_ACTIVE}" -eq 0 ]; then
    return
  fi
  local end_ts wall http_line
  end_ts="$(date +%s)"
  wall=$((end_ts - PIPELINE_START))
  if [ -n "${HTTP_EXIT}" ]; then
    http_line="exit ${HTTP_EXIT}"
  else
    http_line="not run"
  fi

  echo ""
  echo "================================================================================"
  echo "  Portal API tests — pipeline summary"
  echo "================================================================================"
  echo "  Environment : ${ENV_NAME}"
  echo "  Remote deploy: ${BUILD_STATUS}"
  echo "  HTTP runner : ${http_line}"
  echo "  Wall time   : ${wall}s (build + tests)"
  echo "================================================================================"
  if [ "${BUILD_STATUS}" = "failed" ]; then
    echo "  Result: FAIL (remote deploy)"
  elif [ -n "${HTTP_EXIT}" ] && [ "${HTTP_EXIT}" -ne 0 ]; then
    echo "  Result: FAIL (see RUN SUMMARY above for failed requests)"
  elif [ -n "${HTTP_EXIT}" ]; then
    echo "  Result: PASS"
  else
    echo "  Result: FAIL (HTTP suite did not run)"
  fi
  echo "================================================================================"
  echo ""
}

while [ $# -gt 0 ]; do
  case "$1" in
    --build)
      DEPLOY_MODE="build"
      shift
      ;;
    --push)
      DEPLOY_MODE="push"
      shift
      ;;
    --no-build|--skip-deploy)
      DEPLOY_MODE="skip"
      shift
      ;;
    --env)
      ENV_NAME="${2:?--env 需要环境名}"
      shift 2
      ;;
    -h|--help)
      usage
      trap - EXIT
      exit 0
      ;;
    --)
      shift
      PY_ARGS=("$@")
      break
      ;;
    *)
      PY_ARGS+=("$1")
      shift
      ;;
  esac
done

PIPELINE_START="$(date +%s)"
PIPELINE_ACTIVE=1
trap print_pipeline_summary EXIT

if [ "${DEPLOY_MODE}" != "skip" ]; then
  echo ""
  echo "======== Portal API tests: remote.sh ${DEPLOY_MODE} ========"
  if bash "${REPO_ROOT}/.vscode/remote.sh" "${DEPLOY_MODE}"; then
    BUILD_STATUS="ok (${DEPLOY_MODE})"
  else
    BUILD_STATUS="failed (${DEPLOY_MODE})"
    exit 1
  fi
else
  BUILD_STATUS="skipped (--no-build)"
fi

echo ""
echo "======== Portal API tests: HTTP suite (env=${ENV_NAME}) ========"
set +e
python3 "${REPO_ROOT}/test/portal/run_http_tests.py" --env "${ENV_NAME}" "${PY_ARGS[@]}"
HTTP_EXIT=$?
set -e
exit "${HTTP_EXIT}"
