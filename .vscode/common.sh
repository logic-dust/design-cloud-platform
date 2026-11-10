#!/bin/bash
# Common — 两条开发流程与隧道/清理/状态任务的唯一实现
#
#   run_flow push 本地编译(dc-exec+engine) → scp 上传 → 安装 /opt/dce → dlv 启动
#   run_flow build rsync 源码 → 远程编译      → 安装 /opt/dce → dlv 启动
#   cleanup_all / show_status
#
# push 与 build 只差“产物怎么来”，其余（安装、dc-exec 权限、配置预检、dlv 启动与 API 校验）完全共用。
#
# 关键约束（lab 实测，勿删）：
#   1) systemd 单元 design-cloud-engine 常驻占用 1234/52255，且被 kill 后自动重启 → 启动前必须 stop
#   2) dc-exec 需 setcap cap_setuid,cap_setgid+ep（须本地盘），属主须与 engine 一致
#   3) /opt 仅 root 可写：staging → root install；安装前须停进程，否则覆盖运行中二进制会 ETXTBSY
#   4) engine 以 hpcadmin 运行 → /opt/dce 及其 data/certs/logs 须属 hpcadmin
#   5) 密钥 /etc/design-cloud/* 属 dce 且 0600，hpcadmin 读不到 → 复制到 /opt/dce
#
# 本库不在 source 时做 SSH 前置校验：调用方按需检查，便于 clean 类本地任务照常运行。

################################
# SSH / scp / 安装
################################

ssh_remote() {
  local FLAGS=()
  while [ "$#" -gt 0 ] && [[ "$1" == -* ]]; do FLAGS+=("$1"); shift; done
  ssh -p "${SSH_PORT}" "${FLAGS[@]}" "${REMOTE_USER}@${REMOTE_HOST}" "$@"
}

ssh_root() {
  local FLAGS=()
  while [ "$#" -gt 0 ] && [[ "$1" == -* ]]; do FLAGS+=("$1"); shift; done
  ssh -p "${SSH_PORT}" "${FLAGS[@]}" "${ROOT_USER}@${REMOTE_HOST}" "$@"
}

# -C：调试二进制约 2x 压缩（44MB → ~19MB 等效），慢链路上收益明显
scp_to_remote() { scp -C -P "${SSH_PORT}" "$1" "${REMOTE_USER}@${REMOTE_HOST}:$2"; }

# 安装文件并归 REMOTE_USER（属组用 chown 的 "user:" 简写 = 该用户主组）
install_root() { ssh_root "install -m $3 '$1' '$2' && chown '${REMOTE_USER}:' '$2'"; }

require_ssh() { check_ssh_connection || exit 1; }

################################
# 产物来源（A/B 唯一差异）
################################

# push：本地编译 → 上传 staging
stage_push_artifacts() {
  print_step "[1] 本地编译 dc-exec + engine..."   # push 流程起始步
  if ! make -s -C "${SCRIPT_DIR}/.." \
    EXEC_BIN_OUT="${LOCAL_EXEC_BIN}" ENGINE_OUT="${LOCAL_ENGINE}" \
    ENGINE_GC_FLAGS="all=-N -l" dcexec-dev engine-dev -j2; then
    print_error "本地编译失败"; exit 1
  fi
  print_success "编译完成"
  scp_to_remote "${LOCAL_ENGINE}" "${STAGING_ENGINE}" || { print_error "engine 上传失败"; exit 1; }
  scp_to_remote "${LOCAL_EXEC_BIN}" "${STAGING_EXEC_BIN}" || { print_error "dc-exec 上传失败"; exit 1; }
  verify_md5 "${LOCAL_ENGINE}" "${STAGING_ENGINE}" "engine"
  verify_md5 "${LOCAL_EXEC_BIN}" "${STAGING_EXEC_BIN}" "dcexec"
  print_success "上传完成（md5 一致）"
}

verify_md5() {
  local L R
  L="$(md5sum "$1" | awk '{print $1}')"
  R="$(ssh_remote "md5sum '$2' 2>/dev/null | awk '{print \$1}'")"
  [ -n "${R}" ] && [ "${L}" = "${R}" ] || { print_error "$3 md5 不一致（本地 ${L} vs 远程 ${R:-空}）"; exit 1; }
}

# build：rsync 源码 → 远程编译 → 就地 cp 到 staging
stage_build_artifacts() {
  print_step "[1] rsync 源码 → ${REMOTE_SRC_DIR}..."   # build 流程起始步
  command -v rsync >/dev/null 2>&1 || { print_error "本地未安装 rsync"; exit 1; }
  rsync -az --delete \
    --include='*/' --include='*.go' --include='go.mod' --include='go.sum' \
    --include='Makefile' \
    --include='docs-site/static/images/logo.svg' \
    --include='docs-site/static/favicon.ico' \
    --include='internal/server/service/user/weakpasswords.txt' \
    --include='internal/server/portal/web/**' \
    --exclude='*' \
    -e "ssh -p ${SSH_PORT}" \
    "${SCRIPT_DIR}/../" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_SRC_DIR}/" \
    || { print_error "rsync 失败"; exit 1; }

  print_step "    远程编译 dc-exec + engine..."
  # 远程源码树无 .git（rsync 不同步元数据）→ 版本信息由本地计算后传入
  local COMMIT DIRTY
  COMMIT="$(git -C "${SCRIPT_DIR}/.." rev-parse --short HEAD 2>/dev/null || true)"
  DIRTY="$(git -C "${SCRIPT_DIR}/.." status --porcelain 2>/dev/null || true)"
  ssh_remote "cd ${REMOTE_SRC_DIR} && make dcexec-dev engine-dev -j2 GIT_COMMIT='${COMMIT}' GIT_DIRTY='${DIRTY}'" \
    || { print_error "远程编译失败"; exit 1; }
  ssh_remote "cp -f '${REMOTE_SRC_DIR}/build/dc-engine-dev' '${STAGING_ENGINE}' &&
              cp -f '${REMOTE_SRC_DIR}/build/dc-exec-dev' '${STAGING_EXEC_BIN}' &&
              chmod 0755 '${STAGING_ENGINE}' '${STAGING_EXEC_BIN}'" \
    || { print_error "产物整理失败"; exit 1; }
  print_success "远程编译完成"
}

################################
# systemd 单元 / 远程进程
################################

# 单元 enabled 时 kill 进程会被自动拉起（表现为“端口仍被占用 / 刚起就退”），故启动前显式 stop
stop_engine_unit() {
  ssh_root "systemctl is-active ${ENGINE_UNIT} >/dev/null 2>&1" || return 0
  print_step "停止 systemd 单元 ${ENGINE_UNIT}（占用 ${PORTAL_PORT}/52255）..."
  ssh_root "systemctl stop ${ENGINE_UNIT}" || { print_error "停止 ${ENGINE_UNIT} 失败"; exit 1; }
  print_success "已停止 ${ENGINE_UNIT}"
  ssh_root "systemctl is-enabled ${ENGINE_UNIT} >/dev/null 2>&1" \
    && print_warning "${ENGINE_UNIT} 仍开机自启；重启主机后需重跑本流程或手动 systemctl stop"
}

# 用 root：进程属主可能不是 SSH 用户；排除远程 shell 自身 PID 避免自杀
kill_remote_procs() {
  ssh_root "for pat in 'dlv exec.*engine' '^${REMOTE_BINARY}( |\$)'; do
    for p in \$(pgrep -f \"\$pat\" | grep -vx \"\$\$\"); do kill -9 \"\$p\" 2>/dev/null; done
  done"
}

################################
# 安装 + 配置 + 密钥 + dcexec 权限
################################

install_and_configure() {
  print_step "[2] 安装 → ${REMOTE_PATH}（属主 ${REMOTE_USER}）..."
  # 整目录归 hpcadmin：data_dir 回退 BaseDir 时 engine 需在本目录建 certs/
  ssh_root "mkdir -p '${REMOTE_PATH}' ${REMOTE_PATH}/data ${REMOTE_PATH}/logs &&
            chown -R ${REMOTE_USER}: '${REMOTE_PATH}' &&
            chmod 0755 '${REMOTE_PATH}'" || { print_error "准备 ${REMOTE_PATH} 失败"; exit 1; }

  kill_remote_procs # 覆盖运行中二进制会 ETXTBSY
  install_root "${STAGING_ENGINE}" "${REMOTE_BINARY}" 0755 || { print_error "engine 安装失败"; exit 1; }
  install_root "${STAGING_EXEC_BIN}" "${REMOTE_EXEC_BIN}" 0755 || { print_error "dc-exec 安装失败"; exit 1; }
  print_success "已安装 engine + dcexec"

  # 配置（0600 hpcadmin）：配置中的相对密钥路径即指向 ${REMOTE_PATH}
  scp_to_remote "${LOCAL_CONFIG}" "${REMOTE_TMP}/dc-config.$$" || { print_error "配置上传失败"; exit 1; }
  install_root "${REMOTE_TMP}/dc-config.$$" "${REMOTE_CONFIG}" 0600 || { print_error "配置安装失败"; exit 1; }
  print_success "配置已安装: ${REMOTE_CONFIG}"

  # SECURITY-REVIEW: 实验室开发机上的有意复制，不是漏洞。
  # /etc/design-cloud 属 dce 且 0600，hpcadmin 读不到；复制到 /opt/dce
  # 并改属主后，以 hpcadmin 运行的 engine 才能读相对路径密钥。已存在则不覆盖。
  local f
  for f in ${REMOTE_SECRET_FILES}; do
    ssh_root "test -f '${REMOTE_PATH}/${f}'" 2>/dev/null && continue
    if ssh_root "test -f '${REMOTE_SECRET_SRC}/${f}'" 2>/dev/null; then
      install_root "${REMOTE_SECRET_SRC}/${f}" "${REMOTE_PATH}/${f}" 0600 \
        && print_success "已复制密钥 ${f}"
    else
      print_warning "密钥缺失: ${REMOTE_SECRET_SRC}/${f}（如引擎需要请手工放置到 ${REMOTE_PATH}/${f}）"
    fi
  done

  print_step "设置 dc-exec 权限（属主 + setcap）..."
  local OUT CAPS
  # setcap 失败不中止：部分文件系统/受限环境不支持 file capabilities
  OUT="$(ssh_root "chown ${REMOTE_USER}: '${REMOTE_EXEC_BIN}' && chmod 0700 '${REMOTE_EXEC_BIN}' &&
    (setcap cap_setuid,cap_setgid+ep '${REMOTE_EXEC_BIN}' 2>/dev/null ||
     /sbin/setcap cap_setuid,cap_setgid+ep '${REMOTE_EXEC_BIN}' 2>/dev/null ||
     /usr/sbin/setcap cap_setuid,cap_setgid+ep '${REMOTE_EXEC_BIN}' 2>/dev/null || echo SETCAP_UNAVAILABLE)" 2>&1)" \
    || { print_error "dc-exec 权限设置失败: ${OUT}"; exit 1; }
  if printf '%s' "${OUT}" | grep -q SETCAP_UNAVAILABLE; then
    print_warning "setcap 不可用（文件系统不支持或受限环境）——dc-exec 将拿不到特权能力"
    return 0
  fi
  CAPS="$(ssh_root "getcap '${REMOTE_EXEC_BIN}' 2>/dev/null" | tr -d '\r')"
  print_success "dc-exec ${REMOTE_USER} 0700 ${CAPS:-（capabilities 未确认）}"
}

################################
# 配置预检（唯一事实来源：engine check-config）
################################

# 运行 engine check-config 并回显输出（失败返回 1）
check_config_output() {
  ssh_remote "cd '${REMOTE_PATH}' && '${REMOTE_BINARY}' check-config --config '${REMOTE_CONFIG}'" 2>&1
}

# 从 check-config 输出解析生效 portal 端点：PORTAL_HOST_RESOLVED / PORTAL_PORT_RESOLVED
# （空 / 0.0.0.0 = 监听所有网卡 → 远程自测与隧道目标都用回环）
parse_portal() {
  local LINE
  LINE="$(printf '%s\n' "$1" | sed -n 's/^ *portal=//p' | head -1)"
  LINE="${LINE%% *}"
  PORTAL_PORT_RESOLVED="${LINE##*:}"
  PORTAL_HOST_RESOLVED="${LINE%:*}"
  case "${PORTAL_HOST_RESOLVED}" in '' | 0.0.0.0) PORTAL_HOST_RESOLVED="127.0.0.1" ;; esac
  [ -n "${PORTAL_PORT_RESOLVED}" ]
}

# 解析生效 portal 端点与 data_dir；data_dir 不可写则中止
preflight_config() {
  print_step "[3] 预检配置（check-config）..."
  local OUT
  if ! OUT="$(check_config_output)" || ! parse_portal "${OUT}"; then
    print_error "配置校验失败（engine 会启动即退出）:"
    printf '%s\n' "${OUT}" | tail -5
    exit 1
  fi
  DATA_DIR_RESOLVED="$(printf '%s\n' "${OUT}" | sed -n 's/^ *data_dir=//p' | head -1)"
  print_success "配置通过: portal=${PORTAL_HOST_RESOLVED}:${PORTAL_PORT_RESOLVED} data_dir=${DATA_DIR_RESOLVED:-（未输出）}"
  [ "${PORTAL_PORT_RESOLVED}" = "${PORTAL_PORT}" ] \
    || print_warning "实际 portal.port=${PORTAL_PORT_RESOLVED} ≠ env.sh ${PORTAL_PORT}（后续探测/隧道以实际值）"

  # engine 启动时要在 <data_dir>/certs/cluster 建目录；不可写则启动即退出
  if [ -n "${DATA_DIR_RESOLVED}" ]; then
    ssh_remote "d='${DATA_DIR_RESOLVED}/certs/cluster'; while [ ! -e \"\$d\" ] && [ \"\$d\" != / ]; do d=\$(dirname \"\$d\"); done; [ -w \"\$d\" ]" \
      || { print_error "data_dir 不可写: ${DATA_DIR_RESOLVED}/certs/cluster（以 ${REMOTE_USER} 运行 engine，须该用户可写）"; exit 1; }
  fi
}

################################
# dlv 启动 + 就绪校验
################################

ensure_dlv() {
  local VER=""
  ssh_remote "test -x '${REMOTE_DLV_CMD}'" 2>/dev/null \
    && VER="$(ssh_remote "'${REMOTE_DLV_CMD}' version 2>/dev/null | awk '/Version:/{print \$2}'" 2>/dev/null || true)"
  if [ -n "${VER}" ] && [ "${VER}" = "${DLV_VERSION#v}" ]; then
    print_success "复用 dlv ${REMOTE_DLV_CMD} (${VER})"
    return 0
  fi

  print_step "     安装 dlv ${DLV_VERSION}..."
  local STAGING="${REMOTE_TMP}/dlv.$$" OUT
  if OUT="$(ssh_remote "GOBIN='${REMOTE_TMP}' go install github.com/go-delve/delve/cmd/dlv@${DLV_VERSION} 2>&1" 2>&1)" \
    && install_root "${REMOTE_TMP}/dlv" "${REMOTE_DLV_CMD}" 0755; then
    print_success "dlv 已安装（远程 go install）"
    return 0
  fi
  echo "    → go install 失败: $(printf '%s' "${OUT}" | tail -2 | tr '\n' ' ')"

  print_info "回退本地交叉编译上传..."
  local CAND SRC="" INFO
  for CAND in "$(command -v dlv 2>/dev/null)" "$(go env GOPATH)/bin/dlv" "$(go env GOBIN)/dlv"; do
    [ -n "${CAND}" ] && [ -x "${CAND}" ] || continue
    INFO="$(file "${CAND}" 2>/dev/null || true)"
    if printf '%s' "${INFO}" | grep -q "x86-64" && printf '%s' "${INFO}" | grep -q "statically linked"; then SRC="${CAND}"; break; fi
  done
  if [ -z "${SRC}" ]; then
    [ -x "${LOCAL_DLV}" ] || GOBIN="$(dirname "${LOCAL_DLV}")" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
      go install "github.com/go-delve/delve/cmd/dlv@${DLV_VERSION}" 2>&1 | tail -3 || { print_error "dlv 交叉编译失败"; exit 1; }
    SRC="${LOCAL_DLV}"
  fi
  scp_to_remote "${SRC}" "${STAGING}" || { print_error "dlv 上传失败"; exit 1; }
  install_root "${STAGING}" "${REMOTE_DLV_CMD}" 0755 || { print_error "dlv 安装失败"; exit 1; }
  print_success "dlv 已安装: ${REMOTE_DLV_CMD}"
}

# dlv 是独立进程：engine 崩溃后它仍监听，故“dlv 监听”≠“engine 活着”，必须实测 API。
start_dlv_and_verify() {
  local LABEL="$1" i PROBE ALIVE=0
  print_step "[4] dlv 启动 engine（以 ${REMOTE_USER} 运行）..."
  # setsid + & + </dev/null：脱离 ssh 会话
  # 登录 shell 继承实验室 module/LSF PATH；非交互 dlv 启动否则 JOB_SUBMIT 找不到 bsub 或卡在超时。
  ssh_remote -f "cd '${REMOTE_PATH}' && setsid bash -lc 'exec \"${REMOTE_DLV_CMD}\" exec \"${REMOTE_BINARY}\" \
    --listen=${DLV_BIND}:${REMOTE_DEBUG_PORT} --headless=true --api-version=2 --accept-multiclient --continue \
    -- --config \"${REMOTE_CONFIG}\"' > ${REMOTE_DLV_LOG} 2>&1 < /dev/null &"

  for i in $(seq 1 15); do
    ssh_remote "pgrep -f 'dlv exec' >/dev/null 2>&1 && (netstat -ltn 2>/dev/null | grep -q ':${REMOTE_DEBUG_PORT} ' || ss -ltn 2>/dev/null | grep -q ':${REMOTE_DEBUG_PORT} ')" && break
    [ "${i}" = "15" ] && { print_error "dlv 启动超时"; ssh_remote "tail -30 ${REMOTE_DLV_LOG}"; exit 1; }
    sleep 1
  done
  print_success "dlv 已监听 ${DLV_BIND}:${REMOTE_DEBUG_PORT}"
  # 既有的内网调机做法（0.0.0.0 便于直连附加）；保留提示以便换网段/多人共用时能想起收紧
  [ "${DLV_BIND}" = "0.0.0.0" ] && print_info "dlv 绑定 0.0.0.0（无认证，内网可 attach）；需要收紧见 DEBUG_GUIDE"

  for i in $(seq 1 25); do
    if ssh_remote "pgrep -f '^${REMOTE_BINARY}( |\$)' >/dev/null 2>&1"; then
      ALIVE=1
    elif [ "${ALIVE}" -eq 1 ] || [ "${i}" -gt 6 ]; then
      print_error "engine 进程已退出（dlv 仍在监听）"
      ssh_remote "tail -40 ${REMOTE_DLV_LOG}" 2>/dev/null || true
      exit 1
    fi
    PROBE="$(ssh_remote "curl -s --max-time 5 http://${PORTAL_HOST_RESOLVED}:${PORTAL_PORT_RESOLVED}/api/version" 2>/dev/null || true)"
    if printf '%s' "${PROBE}" | grep -q '"version"'; then
      print_success "Portal API 已就绪: ${PROBE}"
      echo ""
      print_separator
      print_success "${LABEL} 就绪：engine(hpcadmin) + dlv:${REMOTE_DEBUG_PORT} + API:${PORTAL_PORT_RESOLVED}"
      print_separator
      print_info "API（本机直连）: curl http://${REMOTE_HOST}:${PORTAL_PORT_RESOLVED}/api/version"
      print_info "附加调试: VSCode 选 Remote ${LABEL} 按 F5（${REMOTE_HOST}:${REMOTE_DEBUG_PORT}）"
      print_info "日志: ssh -p ${SSH_PORT} ${REMOTE_USER}@${REMOTE_HOST} 'tail -f ${REMOTE_DLV_LOG}'"
      echo ""
      return 0
    fi
    echo "    → 等待 Portal API... (${i}s/25s)"
    sleep 1
  done
  print_error "Portal API 未就绪"
  ssh_remote "tail -40 ${REMOTE_DLV_LOG}" 2>/dev/null || true
  exit 1
}

################################
# 主流程
################################

# run_flow <push|build>
run_flow() {
  local FLOW="$1" LABEL
  require_ssh

  # staging：push 上传到此、build 就地 cp 到此，安装步骤统一从这里取
  STAGING_ENGINE="${REMOTE_TMP}/dc-engine.$$"
  STAGING_EXEC_BIN="${REMOTE_TMP}/dc-exec.$$"

  print_separator
  case "${FLOW}" in
    push)  LABEL="Push"
           echo -e "${COLOR_BLUE}push: 本地编译 → 推送产物 → 安装 ${REMOTE_PATH} → dlv 启动（engine 以 ${REMOTE_USER} 运行）${COLOR_NC}" ;;
    build) LABEL="Build"
           echo -e "${COLOR_BLUE}build: rsync 源码 + 远程编译 → 安装 ${REMOTE_PATH} → dlv 启动（engine 以 ${REMOTE_USER} 运行）${COLOR_NC}" ;;
    *)     print_error "用法: run_flow push|build"; exit 2 ;;
  esac
  echo -e "${COLOR_BLUE}目标: ${REMOTE_USER}@${REMOTE_HOST}${COLOR_NC}"
  print_separator

  stop_engine_unit   # 先释放 1234/52255
  case "${FLOW}" in
    push)  stage_push_artifacts ;;
    build) stage_build_artifacts ;;
  esac
  install_and_configure
  preflight_config
  start_dlv_and_verify "${LABEL}"
}

cleanup_all() {
  print_separator
  echo -e "${COLOR_BLUE}清理调试环境 → ${REMOTE_HOST}${COLOR_NC}"
  print_separator
  # 用 root：进程属主可能不是 SSH 用户；无需 SSH 用户侧前置校验
  ssh -p "${SSH_PORT}" "${ROOT_USER}@${REMOTE_HOST}" "for pat in 'dlv exec.*engine' '^${REMOTE_BINARY}( |\$)'; do
    for p in \$(pgrep -f \"\$pat\" | grep -vx \"\$\$\"); do kill -9 \"\$p\" 2>/dev/null; done
  done" 2>/dev/null || print_warning "远程清理失败（root SSH 不可用？）"
  print_success "远程 dlv/engine 已清理"
}

show_status() {
  print_separator
  echo -e "${COLOR_BLUE}远程状态 → ${REMOTE_USER}@${REMOTE_HOST}${COLOR_NC}"
  print_separator

  echo "=== 直连可达性（本机 → 远程）==="
  local P OK
  for P in "${REMOTE_DEBUG_PORT}" "${PORTAL_PORT}"; do
    if timeout 3 bash -c "exec 3<>/dev/tcp/${REMOTE_HOST}/${P}" 2>/dev/null; then OK="可直达"; else OK="不可达"; fi
    echo "  ${REMOTE_HOST}:${P} ← ${OK}"
  done

  # 远程命令一律写成单行：多行字符串在本环境下可能被折叠成一行，导致语法错误。
  # pgrep 模式用 '[d]lv' 而非 'dlv'，避免把执行本检查的远程 shell 自身也匹配进来。
  echo "=== 远程进程 ==="
  ssh_remote "pgrep -fa '[d]lv exec' || echo '  (dlv 未运行)'" 2>/dev/null | head -3 || echo "  (SSH 失败)"

  echo "=== 远程端口 ==="
  ssh_remote "(netstat -ltn 2>/dev/null || ss -ltn 2>/dev/null) | grep -E ':${REMOTE_DEBUG_PORT} |:${PORTAL_PORT} |:52255 ' || echo '  (无)'" 2>/dev/null || echo "  (SSH 失败)"

  echo "=== Portal API ==="
  ssh_remote "curl -s --max-time 5 http://127.0.0.1:${PORTAL_PORT}/api/version || echo '  (无响应)'" 2>/dev/null || echo "  (SSH 失败)"

  echo "=== dlv 监听 ==="
  if [ "${DLV_BIND}" = "0.0.0.0" ]; then
    echo "  ${DLV_BIND}:${REMOTE_DEBUG_PORT}（0.0.0.0 = 本机可直连附加；无认证，内网可 attach）"
  else
    echo "  ${DLV_BIND}:${REMOTE_DEBUG_PORT}（仅回环，需 ssh -L 才能附加）"
  fi

  echo "=== dc-exec L1 约束（属主=engine 用户 / 0700 / cap_setuid+setgid / 非硬链接）==="
  local STAT
  STAT="$(ssh_root "stat -c '%U %G %a %h' '${REMOTE_EXEC_BIN}' 2>/dev/null" 2>/dev/null | tr -d '\r')"
  if [ -z "${STAT}" ]; then
    echo "  ⚠️  未安装或 root SSH 不可用: ${REMOTE_EXEC_BIN}"
  else
    local OU OG MODE NLINK CAPS
    read -r OU OG MODE NLINK <<<"${STAT}"
    CAPS="$(ssh_root "getcap '${REMOTE_EXEC_BIN}' 2>/dev/null" 2>/dev/null | tr -d '\r')"
    echo "  ${REMOTE_EXEC_BIN}  ${OU}:${OG}  ${MODE}  Nlink=${NLINK}"
    [ "${OU}" = "${REMOTE_USER}" ] || echo "    ⚠️  属主须为 ${REMOTE_USER}（checkCaller 校验父进程 realUID == 属主）"
    [ "${MODE}" = "700" ] || echo "    ⚠️  权限须为 700（dc-exec 拒绝 group/other 有任何权限）"
    [ "${NLINK}" = "1" ] || echo "    ⚠️  存在硬链接（Nlink=${NLINK}），dc-exec 会拒绝运行"
    printf '%s' "${CAPS}" | grep -q cap_setuid || echo "    ⚠️  缺少 cap_setuid（L0 校验会失败）"
    printf '%s' "${CAPS}" | grep -q cap_setgid || echo "    ⚠️  缺少 cap_setgid（L0 校验会失败）"
  fi
}
