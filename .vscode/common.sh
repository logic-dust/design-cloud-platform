#!/bin/bash
# Common - 远程部署/调试公共函数库
# 被 remote-run.sh / remote-debug.sh / remote-build-debug.sh 复用
#
# 命名约定:
#   *_local            在本地执行（如本地编译 engine）
#   *_to_remote        从本地上传到远程（上传方向明确）
#   remote_* / *_remote 在远程主机上执行（如远程清理/启动/等待）
#
# 调用方必须先 source env.sh（提供 SSH/路径变量与 print_* 系列 helper），
# 本库不再重复 source，避免二次加载。

set -e

# 前置检查：SSH 连通性
check_ssh_connection || exit 1

################################
# SSH / scp 封装
################################

# 在远程执行命令：ssh_remote [-f] <命令...>
#   示例: ssh_remote "pwd"; ssh_remote -f "setsid ... &"
ssh_remote() {
  local FLAGS=()
  while [ "$#" -gt 0 ] && [[ "$1" == -* ]]; do
    FLAGS+=("$1")
    shift
  done
  ssh -p "${SSH_PORT}" "${FLAGS[@]}" "${REMOTE_USER}@${REMOTE_HOST}" "$@"
}

# 上传文件到远程：scp_to_remote <本地路径> <远程目标路径>
scp_to_remote() {
  scp -P "${SSH_PORT}" "$1" "${REMOTE_USER}@${REMOTE_HOST}:$2"
}

################################
# 远程进程管理
################################

# 远程判断是否存在匹配进程（排除执行检查的 shell 自身）：remote_has_process <pgrep 模式>
remote_has_process() {
  ssh_remote "pgrep -f '$1' | grep -vx \"\$\$\" | grep -q ."
}

# 远程 kill 匹配进程（排除自身 PID）：remote_kill_pattern <pgrep 模式>
remote_kill_pattern() {
  ssh_remote "for p in \$(pgrep -f '$1' | grep -vx \"\$\$\"); do kill -9 \"\$p\" 2>/dev/null; done"
}

# 通用等待循环：wait_for_remote <描述> <最大秒数> <远程检查命令>
#   远程检查命令返回 0 视为就绪
wait_for_remote() {
  local DESC="$1" MAX_WAIT="$2" CHECK_CMD="$3" i
  echo "    → 等待 ${DESC}..."
  for i in $(seq 1 "${MAX_WAIT}"); do
    if ssh_remote "${CHECK_CMD}"; then
      return 0
    fi
    echo "    → 继续等待... (${i}s/${MAX_WAIT}s)"
    sleep 1
  done
  return 1
}

# 清理远程旧进程（dlv + engine，不删二进制；供部署/调试前调用）
cleanup_remote_processes() {
  print_step "[2/5] 清理远程旧进程..."
  echo "    → 远程 engine / dlv 进程..."
  # 用 pgrep 收集 PID 再 kill，不用 pkill（安全机制会拦截 pkill，且误杀面大）
  # 注意：pgrep -f 会把执行本清理命令的远程 shell 自身也匹配进来（其命令行含匹配串），
  #       必须先排除 $$（远程 shell 自身 PID），否则清理会先 kill 掉自己而中断。
  remote_kill_pattern "dlv exec.*engine" || true
  remote_kill_pattern "design-cloud-engine" || true
  sleep 2
  print_success "远程进程已清理"
}

################################
# engine 本地编译 → 推送到远程
################################

# 本地编译 engine（走 Makefile engine-dev 目标，脚本传 ENGINE_OUT 覆盖输出路径）
build_engine_local() {
  print_step "[1/5] 编译 engine..."
  local MODE="${1:-debug}"
  local MAKE_ARGS=("ENGINE_OUT=${LOCAL_BINARY}")
  if [ "${MODE}" = "debug" ]; then
    MAKE_ARGS+=("ENGINE_GC_FLAGS=all=-N -l")
    print_info "使用调试编译（-gcflags=all=-N -l）"
  elif [ "${MODE}" = "opt" ]; then
    print_info "使用优化编译（关闭 -N -l）"
  else
    # 自定义标志：覆盖分支传任意 go build 标志（如 "-race"），需按空格分词成数组
    read -r -a EXTRA_FLAGS <<<"${MODE}"
    MAKE_ARGS+=("ENGINE_EXTRA_FLAGS=${EXTRA_FLAGS[*]}")
    print_info "使用自定义编译标志: ${MODE}"
  fi
  if make -s -C "${SCRIPT_DIR}/.." "${MAKE_ARGS[@]}" engine-dev; then
    print_success "编译成功: ${LOCAL_BINARY}"
  else
    print_error "编译失败"
    exit 1
  fi
}

# 上传二进制到远程 + md5 校验：upload_binary_to_remote
upload_binary_to_remote() {
  print_step "[3/5] 上传二进制到远程..."
  echo "    → ${LOCAL_BINARY} → ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_BINARY}"
  if scp_to_remote "${LOCAL_BINARY}" "${REMOTE_BINARY}"; then
    print_success "上传成功"
  else
    print_error "上传失败"
    exit 1
  fi
  # md5 校验：kill 后 SSH 会话 exit 255 可能导致 scp 静默失败，必须对比两侧 md5 确认
  local LOCAL_MD5 REMOTE_MD5
  LOCAL_MD5="$(md5sum "${LOCAL_BINARY}" | awk '{print $1}')"
  REMOTE_MD5="$(ssh_remote "md5sum ${REMOTE_BINARY} 2>/dev/null | awk '{print \$1}'")"
  if [ -n "${REMOTE_MD5}" ] && [ "${LOCAL_MD5}" = "${REMOTE_MD5}" ]; then
    print_success "md5 校验一致: ${LOCAL_MD5}"
  else
    print_error "md5 不一致! 本地=${LOCAL_MD5} 远程=${REMOTE_MD5}"
    exit 1
  fi
}

# 上传配置文件到远程（可选）：upload_config_to_remote
upload_config_to_remote() {
  print_step "[4/5] 上传配置文件（可选）..."
  if [ -f "${LOCAL_CONFIG}" ]; then
    echo "    → ${LOCAL_CONFIG} → ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_CONFIG_FILE}"
    if scp_to_remote "${LOCAL_CONFIG}" "${REMOTE_CONFIG_FILE}"; then
      print_success "配置已上传"
    else
      print_warning "配置上传失败，继续..."
    fi
  else
    print_warning "本地配置文件不存在，跳过"
  fi
}

# 组合：本地编译 → 远程清理 → 上传（不含远程启动）
deploy_engine_to_remote() {
  build_engine_local "$1"
  cleanup_remote_processes
  upload_binary_to_remote
  upload_config_to_remote
}

################################
# 远程启动
################################

# 远程直接启动 engine（不带 dlv）：start_engine_remote <engine 参数...>
start_engine_remote() {
  local ENGINE_ARGS="$*"
  ssh_remote -f "cd ${REMOTE_PATH} && setsid ${REMOTE_BINARY} ${ENGINE_ARGS} > /tmp/engine-remote.log 2>&1 < /dev/null &"
}

# 远程启动 dlv debug engine（headless）：start_dlv_debug <engine 参数...>
start_dlv_debug() {
  local ENGINE_ARGS="$*"
  ssh_remote -f "cd ${REMOTE_PATH} && setsid ${REMOTE_DLV_CMD} exec ${REMOTE_BINARY} \
    --listen=0.0.0.0:${REMOTE_DEBUG_PORT} --headless=true --api-version=2 --accept-multiclient --continue \
    -- ${ENGINE_ARGS} > ${REMOTE_DLV_LOG} 2>&1 < /dev/null &"
}

# 等待 dlv 启动并监听调试端口：wait_dlv_ready [最大秒数]
wait_dlv_ready() {
  local MAX_WAIT="${1:-15}"
  wait_for_remote "dlv 启动并监听端口 ${REMOTE_DEBUG_PORT}" "${MAX_WAIT}" \
    "pgrep -f 'dlv exec.*engine' | grep -vx \"\$\$\" | grep -q . && (netstat -ltnp 2>/dev/null | grep -q ':${REMOTE_DEBUG_PORT} ' || ss -ltn 2>/dev/null | grep -q ':${REMOTE_DEBUG_PORT} ')"
}

################################
# dlv 管理
################################

# 确保远程 dlv 可用：复用已存在 → 远程 go install → 回退本地编译上传
ensure_remote_dlv() {
  # 复用远程 dlv 前校验版本：Go 升级后旧 dlv（如 v1.27 对 go1.27）会报
  # "Version of Delve is too old" 而无法调试，需按 DLV_VERSION 重装
  local REMOTE_DLV_VER=""
  if ssh_remote "test -x '${REMOTE_DLV_CMD}'" 2>/dev/null; then
    REMOTE_DLV_VER="$(ssh_remote "${REMOTE_DLV_CMD} version 2>/dev/null | awk '/Version:/{print \$2}'" 2>/dev/null || true)"
  fi
  if [ -n "${REMOTE_DLV_VER}" ] && [ "${REMOTE_DLV_VER}" = "${DLV_VERSION#v}" ]; then
    print_success "复用远程 dlv: ${REMOTE_DLV_CMD} (${REMOTE_DLV_VER})"
    return 0
  fi
  if [ -n "${REMOTE_DLV_VER}" ]; then
    print_info "远程 dlv 版本 ${REMOTE_DLV_VER} ≠ 期望 ${DLV_VERSION#v}，重新安装..."
  fi
  print_info "远程安装 dlv (go install ${DLV_VERSION})..."
  if ssh_remote "GOBIN=${REMOTE_PATH} go install github.com/go-delve/delve/cmd/dlv@${DLV_VERSION}"; then
    print_success "远程 dlv 安装完成: ${REMOTE_DLV_CMD}"
    return 0
  fi
  print_warning "远程 go install dlv 失败，回退本地编译上传..."
  local LOCAL_DLV_SRC=""
  local cand DLV_INFO
  for cand in "$(command -v dlv 2>/dev/null)" "$(go env GOPATH)/bin/dlv" "$(go env GOBIN)/dlv"; do
    [ -z "${cand}" ] && continue
    [ ! -x "${cand}" ] && continue
    DLV_INFO="$(file "${cand}" 2>/dev/null || true)"
    if printf '%s' "${DLV_INFO}" | grep -q "x86-64" && printf '%s' "${DLV_INFO}" | grep -q "statically linked"; then
      LOCAL_DLV_SRC="${cand}"
      break
    fi
  done
  if [ -z "${LOCAL_DLV_SRC}" ]; then
    if [ ! -x "${LOCAL_DLV}" ]; then
      print_info "本地交叉编译静态 dlv..."
      if ! GOBIN="$(dirname "${LOCAL_DLV}")" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
        go install "github.com/go-delve/delve/cmd/dlv@${DLV_VERSION}" 2>&1 | tail -5; then
        print_error "dlv 编译失败（可能需要网络拉取模块，或固定 DLV_VERSION 到已缓存版本）"
        return 1
      fi
    fi
    LOCAL_DLV_SRC="${LOCAL_DLV}"
  fi
  if ! scp_to_remote "${LOCAL_DLV_SRC}" "${REMOTE_DLV_CMD}"; then
    print_error "dlv 上传失败"
    return 1
  fi
  ssh_remote "chmod +x '${REMOTE_DLV_CMD}'"
  print_success "dlv 已上传并设为可执行: ${REMOTE_DLV_CMD}"
}

################################
# 配置与数据库设置同步
################################

# 同步远程数据库中 agent.launcherPath 配置：
# 开发/调试环境（如 /share/apps/design-cloud）与生产 RPM 环境（/opt/design-cloud）路径不同，
# engine 运行时从数据库读取 agent.launcherPath，必须保证数据库中的路径与当前部署环境匹配，
# 否则会触发 dc-launcher 的 L0/L1 校验失败（能力丢失、权限超标或 UID 不匹配）。
sync_remote_launcher_path() {
  local TARGET_LAUNCHER="${1:-${REMOTE_PATH}/dc-launcher}"
  print_info "同步远程数据库 agent.launcherPath 为 ${TARGET_LAUNCHER}..."

  local SQL_STATEMENTS="INSERT INTO settings (domain, value) VALUES ('agent', json_build_object('launcherPath', '${TARGET_LAUNCHER}', 'vnc', json_build_object('binDir', '/opt/TurboVNC/bin', 'scanEnabled', true, 'scanInterval', 1800), 'monitor', json_build_object('resourceReportEnabled', true, 'resourceReportInterval', 60, 'diskMounts', '[]'::json))) ON CONFLICT (domain) DO UPDATE SET value = jsonb_set(settings.value::jsonb, '{launcherPath}', to_jsonb('${TARGET_LAUNCHER}'::text))::text; UPDATE cache_versions SET version = version + 1 WHERE name = 'settings';"

  local SYNCED=false
  # 优先尝试以 REMOTE_USER 凭 dbpwd 访问本地 PostgreSQL
  if ssh_remote "test -f '${REMOTE_PATH}/dbpwd'" 2>/dev/null; then
    if printf '%s\n' "${SQL_STATEMENTS}" | ssh_remote "cd '${REMOTE_PATH}' && PGPASSWORD=\$(cat dbpwd) psql -h 127.0.0.1 -U dc -d dc" >/dev/null 2>&1; then
      SYNCED=true
    fi
  fi

  # 若以远程用户未成功，尝试以 root (su - postgres) 执行
  if [ "${SYNCED}" != "true" ]; then
    if printf '%s\n' "${SQL_STATEMENTS}" | ssh -p "${SSH_PORT}" "${ROOT_USER}@${REMOTE_HOST}" "su - postgres -c 'psql -d dc'" >/dev/null 2>&1; then
      SYNCED=true
    fi
  fi

  if [ "${SYNCED}" = "true" ]; then
    print_success "数据库 agent.launcherPath 已同步为 ${TARGET_LAUNCHER}"
  else
    print_warning "未能自动更新数据库 agent.launcherPath（数据库可能未启动或认证失败），请注意核对设置"
  fi
}

# 检测远程 engine 配置文件（优先 config.toml，其次 config.dev.toml）
detect_remote_config() {
  local CFG=""
  for cfg in "${REMOTE_PATH}/config.toml" "${REMOTE_PATH}/config.dev.toml"; do
    if ssh_remote "test -f '${cfg}'" 2>/dev/null; then
      CFG="${cfg}"
      break
    fi
  done
  [ -n "${CFG}" ] || CFG="${REMOTE_PATH}/config.toml"
  echo "${CFG}"
}
