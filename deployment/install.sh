#!/bin/bash
# Design Cloud Engine 安装脚本（无包/源码树路径）
#
#   install.sh server [选项]   # 读 config：物化二进制 + 拷贝 canonical unit + 生成配置
#   install.sh agent  [选项]   # configless：物化二进制 + 领证 + canonical unit + drop-in
#
# 生产环境请用 RPM：make package && rpm -Uvh build/design-cloud-engine-*.rpm
# 本脚本只服务开发机 / 无包环境；有 RPM 的机器以包为准。
#
# 制品源与执行位分离：二进制从共享制品目录（可为 NFS）物化到本地盘，
# ExecStart 只指向本地——systemd 不能从 NFS 执行；dc-exec capability 也不能落在 NFS。

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=lib/install-common.sh
. "${SCRIPT_DIR}/lib/install-common.sh"

# ============================================================
# 配置（按需修改）
# ============================================================
SERVER_NAME="dc-engine"
EXEC_BIN_NAME="dc-exec"
UNIT_TEMPLATE="${SCRIPT_DIR}/systemd/design-cloud-engine.service"
# 制品源与执行位分离（systemd 不得从 NFS 执行：挂载晚于启动、运行中缺页 SIGBUS、
# hard mount 不可达时进程进入 D 状态连 SIGKILL 都杀不掉）：
#   ARTIFACT_DIR 共享制品目录，一次上传、多节点取用（可为 NFS）
#   INSTALL_DIR  本地执行位，ExecStart 只指向这里
ARTIFACT_DIR="${DCE_ARTIFACT_DIR:-/share/apps/design-cloud}"
INSTALL_DIR="${DCE_INSTALL_DIR:-/opt/dce}"
CONFIG_DIR="${DCE_CONFIG_DIR:-/etc/design-cloud}"
LOG_DIR="${DCE_LOG_DIR:-/var/log/design-cloud}"
SERVICE_DIR="${DCE_SERVICE_DIR:-/etc/systemd/system}"
SERVICE_NAME="${DCE_SERVICE_NAME:-design-cloud-engine}"
BUILD_DIR="${DCE_BUILD_DIR:-${PROJECT_ROOT}/build}"
SERVICE_USER="${DCE_SERVICE_USER:-dce}"
SERVICE_GROUP="${DCE_SERVICE_GROUP:-dce}"
CONFIG_SAMPLE_NAME="config.example.toml"
CONFIG_NAME="config.toml"

# --- 角色（server | agent）---
# 注意：RPM 安装不分角色；这里的 server/agent 只影响无包引导流程
# （config 生成 vs OTP 领证 + configless drop-in）。
MODE="server"
AGENT_DATA_DIR="${DCE_DATA_DIR:-/var/lib/design-cloud}"
PORTAL_URL="${DCE_PORTAL_URL:-}"
TOKEN="${DCE_TOKEN:-}"
SERVERS="${DCE_SERVERS:-}"
PORT="${DCE_PORT:-52255}"
LOG_LEVEL="${DCE_LOG_LEVEL:-INFO}"
SKIP_REGISTER=0
INSECURE_HTTP="${DCE_INSECURE_HTTP:-0}"

# ============================================================
# 颜色 & 日志
# ============================================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# ============================================================
# 参数解析（命令行优先于环境变量）
# ============================================================
usage() {
  cat <<'EOF'
用法: install.sh <server|agent> [选项]

角色（位置参数，必填；仅影响无包引导流程，RPM 安装不分角色）:
  server   读 config.toml。流程：物化 engine+dc-exec → 生成配置 → 安装 unit。
  agent    configless。流程：物化 engine+dc-exec → node register →
           安装 canonical unit + configless drop-in 并启动。
           NodeID 取 os.Hostname()；--servers 缺省取 --portal-url 的主机名。

agent 选项:
  --portal-url <url>   Portal HTTP(S) URL
  --token <OTP>        一次性注册令牌（--skip-register 时可省）
  --servers <a,b>      集群 Server 主机名列表（不含端口）
  --port <n>           集群 gRPC 端口（缺省 52255）
  --data-dir <dir>     证书基准目录（须与 node register 一致）
  --log-dir <dir>      日志目录（缺省 /var/log/design-cloud）
  --log-level <lvl>    DEBUG|INFO|WARN|ERROR
  --skip-register      证书材料已就位，仅做原地升级
  --insecure-http      允许明文 HTTP Portal URL 注册（高危：明文传输 Token 与密钥）

通用选项:
  --install-dir <dir>   本地执行位（ExecStart 指向此处，默认 /opt/dce）
  --artifact-dir <dir>  共享制品源（一次上传多处取用，默认 /share/apps/design-cloud）
  --service-user/-group/-name/-dir <v>  -h|--help

环境变量: DCE_ARTIFACT_DIR DCE_INSTALL_DIR DCE_CONFIG_DIR DCE_LOG_DIR DCE_SERVICE_DIR
          DCE_SERVICE_NAME DCE_BUILD_DIR DCE_SERVICE_USER DCE_SERVICE_GROUP
          DCE_DATA_DIR DCE_PORTAL_URL DCE_TOKEN DCE_SERVERS DCE_PORT DCE_LOG_LEVEL
          DCE_INSECURE_HTTP
EOF
}

parse_args() {
  # 角色是位置参数（唯一），不用模式开关——角色决定整条安装流程，不是可选修饰
  case "${1:-}" in
    server|agent) MODE="$1"; shift ;;
    -h|--help) usage; exit 0 ;;
    "") log_error "缺少角色参数：server 或 agent"; usage >&2; exit 1 ;;
    *) log_error "未知角色: $1（期望 server 或 agent）"; usage >&2; exit 1 ;;
  esac

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --portal-url) PORTAL_URL="${2:-}"; shift 2 ;;
      --token) TOKEN="${2:-}"; shift 2 ;;
      --servers) SERVERS="${2:-}"; shift 2 ;;
      --port) PORT="${2:-}"; shift 2 ;;
      --data-dir) AGENT_DATA_DIR="${2:-}"; shift 2 ;;
      --log-dir) LOG_DIR="${2:-}"; shift 2 ;;
      --log-level) LOG_LEVEL="${2:-}"; shift 2 ;;
      --skip-register) SKIP_REGISTER=1; shift ;;
      --insecure-http) INSECURE_HTTP=1; shift ;;
      --service-user) SERVICE_USER="${2:-}"; shift 2 ;;
      --service-group) SERVICE_GROUP="${2:-}"; shift 2 ;;
      --service-name) SERVICE_NAME="${2:-}"; shift 2 ;;
      --service-dir) SERVICE_DIR="${2:-}"; shift 2 ;;
      --install-dir) INSTALL_DIR="${2:-}"; shift 2 ;;
      --artifact-dir) ARTIFACT_DIR="${2:-}"; shift 2 ;;
      -h|--help) usage; exit 0 ;;
      *) log_error "未知参数: $1"; usage >&2; exit 1 ;;
    esac
  done

  if [[ "${MODE}" == "agent" ]]; then
    if [[ -n "${PORTAL_URL}" && ! "${PORTAL_URL}" =~ ^https?:// ]]; then
      log_error "--portal-url 必须是完整的 http(s) URL（例如 https://mgt01:8443）；当前为 '${PORTAL_URL}'"
      exit 1
    fi
    if [[ -n "${PORTAL_URL}" && "${PORTAL_URL}" =~ ^http:// && "${INSECURE_HTTP}" != "1" && "${SKIP_REGISTER}" != "1" ]]; then
      log_error "SECURITY ERROR: --portal-url 使用了未加密的 http:// 协议（${PORTAL_URL}）！"
      log_error "明文传输 OTP 令牌与私钥易遭受中间人（MitM）窃听与篡改。"
      log_error "推荐：使用 https:// 及有效 TLS 证书。"
      log_error "若在隔离内网/开发环境中自担风险继续，请追加 --insecure-http（或配置 DCE_INSECURE_HTTP=1）。"
      exit 1
    fi
    if [[ -z "${SERVERS}" ]]; then
      if [[ -z "${PORTAL_URL}" ]]; then
        log_error "agent 形态需要 --portal-url（用于登记）；仅在证书材料已就位时才可省略并配 --skip-register"
        exit 1
      fi
      # 与 Windows 安装脚本同规则：默认取 portal URL 的主机名
      SERVERS="${PORTAL_URL#*://}"
      SERVERS="${SERVERS%%/*}"
      SERVERS="${SERVERS%%:*}"
    fi
    if [[ "${PORT}" -lt 1024 || "${PORT}" -gt 65535 ]]; then
      log_error "--port 必须在 1024..65535 之间；当前为 ${PORT}"
      exit 1
    fi
    if [[ "${SKIP_REGISTER}" -eq 0 && -z "${TOKEN}" ]]; then
      log_error "agent 首次安装需要 --token <OTP>（在控制节点用 node issue-token 签发）；已有证书时可用 --skip-register"
      exit 1
    fi
    case "${LOG_LEVEL}" in
      DEBUG|INFO|WARN|ERROR) ;;
      *) log_error "--log-level 只能是 DEBUG/INFO/WARN/ERROR；当前为 '${LOG_LEVEL}'"; exit 1 ;;
    esac
  fi
}

# ============================================================
# 前置检查
# ============================================================
check_root() {
  if [[ ${EUID} -ne 0 ]]; then
    log_error "请以 root 身份运行（使用 sudo）"
    exit 1
  fi
}

# 制品来源：优先共享制品目录（一次上传、多节点取用），回退到本地构建产物
artifact_src() {
  if [[ -f "${ARTIFACT_DIR}/${SERVER_NAME}" ]]; then
    echo "${ARTIFACT_DIR}"
  else
    echo "${BUILD_DIR}"
  fi
}

check_binaries() {
  log_info "检查构建产物..."

  local src
  src="$(artifact_src)"

  if [[ ! -f "${src}/${SERVER_NAME}" ]]; then
    log_error "未找到 Engine 二进制（制品源 ${ARTIFACT_DIR} 、构建目录 ${BUILD_DIR} 均无）"
    log_info "请先执行: make engine，或把产物上传到 ${ARTIFACT_DIR}"
    exit 1
  fi

  if [[ ! -f "${src}/${EXEC_BIN_NAME}" ]]; then
    log_error "未找到 dc-exec 二进制: ${src}/${EXEC_BIN_NAME}"
    log_info "请先执行: make dcexec"
    exit 1
  fi

  log_success "制品源: ${src}"
}

# ============================================================
# 用户 / 组
# ============================================================
ensure_service_user() {
  log_info "确保服务用户存在..."
  dce_ensure_service_user "${SERVICE_USER}" "${SERVICE_GROUP}"
  log_success "服务用户: ${SERVICE_USER}:${SERVICE_GROUP}"
}

# ============================================================
# 目录结构
# ============================================================
create_directories() {
  log_info "创建目录结构..."

  mkdir -p "${INSTALL_DIR}"

  if [[ "${MODE}" == "agent" ]]; then
    # 无配置文件形态：材料与日志都在数据目录下，引擎按 <data-dir>/certs/cluster 解析
    mkdir -p "${AGENT_DATA_DIR}" "${LOG_DIR}"
    log_success "数据目录已创建: ${AGENT_DATA_DIR}"
    return 0
  fi

  mkdir -p "${CONFIG_DIR}"
  mkdir -p "${LOG_DIR}"
  # engine 以「配置文件所在目录」为基准解析 certs/ 等相对路径，
  # 因此证书目录必须落在 $CONFIG_DIR 下（而不是 $INSTALL_DIR/conf）。
  mkdir -p "${CONFIG_DIR}/certs/cluster/ca" "${CONFIG_DIR}/certs/portal"

  log_success "目录结构已创建"
}

# ============================================================
# 停止已有服务（原地升级的前置条件）
# ============================================================
# 覆盖正在运行的二进制不会立刻报错：GNU install 先 unlink 再创建，旧进程继续持有已被删除的
# inode。于是脚本「安装成功」而服务仍在跑旧版本 —— `systemctl enable --now` 对运行中的 unit
# 是空操作（不重启）。因此替换二进制前必须先停服，安装完成后再重启。
# WAS_ACTIVE 记录停服前的状态，供控制节点模式复位。
WAS_ACTIVE=0
stop_existing_service() {
  command -v systemctl >/dev/null 2>&1 || return 0
  if systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
    log_info "停止已有服务 ${SERVICE_NAME}（原地升级需先释放二进制）..."
    systemctl stop "${SERVICE_NAME}"
    WAS_ACTIVE=1
  fi
}

# ============================================================
# 物化到本地执行位
# ============================================================
# 从制品源拷贝到本地：先写临时名再 rename（同目录内原子），旧版本保留为 .prev 以便回滚。
# 拷贝而非直接执行共享路径：NFS 上的可执行文件被 mmap 按需分页，服务端抖动会导致缺页
# 失败→进程 SIGBUS，且 hard mount 不可达时进程会卡在 D 状态无法被杀死。
materialize_binary() {
  local name="$1" mode="$2" src="$3"
  [[ -f "${src}/${name}" ]] || return 0
  install -m "${mode}" "${src}/${name}" "${INSTALL_DIR}/.${name}.new"
  if [[ -f "${INSTALL_DIR}/${name}" ]]; then
    cp -p "${INSTALL_DIR}/${name}" "${INSTALL_DIR}/${name}.prev" 2>/dev/null || true
  fi
  mv -f "${INSTALL_DIR}/.${name}.new" "${INSTALL_DIR}/${name}"
}

# ============================================================
# 安装 Engine
# ============================================================
install_engine() {
  log_info "物化 Engine 到本地执行位..."
  materialize_binary "${SERVER_NAME}" 0755 "$(artifact_src)"
  log_success "Engine 已安装: ${INSTALL_DIR}/${SERVER_NAME}"
}

# ============================================================
# 安装 DcExec（含 setcap；RPM 路径由 %post 负责）
# ============================================================
install_dcexec() {
  log_info "物化 dc-exec 到本地执行位..."
  materialize_binary "${EXEC_BIN_NAME}" 0700 "$(artifact_src)"
  if dce_apply_exec_setcap "${INSTALL_DIR}/${EXEC_BIN_NAME}"; then
    :
  else
    log_warning "setcap 不可用；dc-exec 启动时 L0 能力位自检会失败"
  fi
  log_success "dc-exec 已安装: ${INSTALL_DIR}/${EXEC_BIN_NAME}"
}

# ============================================================
# Systemd：拷贝 canonical unit；agent 再写 configless drop-in
# ============================================================
create_systemd_service() {
  log_info "安装 systemd 单元..."

  if [[ ! -f "${UNIT_TEMPLATE}" ]]; then
    log_error "缺少单元模板: ${UNIT_TEMPLATE}"
    exit 1
  fi

  local unit_dst="${SERVICE_DIR}/${SERVICE_NAME}.service"
  # 非默认 INSTALL_DIR / 用户时做路径与身份替换；默认布局与包内 unit 一致。
  sed \
    -e "s|/opt/dce|${INSTALL_DIR}|g" \
    -e "s|/etc/design-cloud|${CONFIG_DIR}|g" \
    -e "s|/var/log/design-cloud|${LOG_DIR}|g" \
    -e "s|^User=dce$|User=${SERVICE_USER}|" \
    -e "s|^Group=dce$|Group=${SERVICE_GROUP}|" \
    "${UNIT_TEMPLATE}" >"${unit_dst}"

  if [[ "${MODE}" == "agent" ]]; then
    local dropin_dir="${SERVICE_DIR}/${SERVICE_NAME}.service.d"
    mkdir -p "${dropin_dir}"
    cat >"${dropin_dir}/configless.conf" <<EOF
[Service]
ExecStartPre=
ExecStart=
ExecStart=${INSTALL_DIR}/${SERVER_NAME} --servers ${SERVERS} --port ${PORT} --data-dir ${AGENT_DATA_DIR} --log-dir ${LOG_DIR} --log-level ${LOG_LEVEL}
EOF
    log_success "configless drop-in: ${dropin_dir}/configless.conf"
  fi

  if [[ "${EUID}" -eq 0 ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
  fi
  dce_systemd_tmpfiles_create
  log_success "systemd 服务已安装: ${unit_dst}"
}

# ============================================================
# 配置
# ============================================================
# 生成初始配置（仅控制节点）。
# 模板的唯一事实源是 engine 自带的 config.GetExampleContent()（`gen-config` 子命令）：
# 不要在本脚本里重复维护默认值，也不要依赖仓库中并不存在的模板文件。
# engine 拒绝以 root 运行，故借服务用户身份在临时目录里生成后再落位。
create_config() {
  log_info "创建配置..."

  local config_file="${CONFIG_DIR}/${CONFIG_NAME}"
  if [[ -f "${config_file}" ]]; then
    log_warning "配置已存在，保持不变: ${config_file}"
    return 0
  fi

  local tmp_dir="${CONFIG_DIR}/.gen-config.$$"
  mkdir -p "${tmp_dir}"
  chown "${SERVICE_USER}:${SERVICE_GROUP}" "${tmp_dir}"
  chmod 0700 "${tmp_dir}"

  if ! run_as_service_user "cd '${tmp_dir}' && '${INSTALL_DIR}/${SERVER_NAME}' gen-config"; then
    rm -rf "${tmp_dir}"
    log_error "生成示例配置失败：请确认 ${SERVICE_USER} 可执行 ${INSTALL_DIR}/${SERVER_NAME}"
    exit 1
  fi

  # 日志目录改为绝对路径（生产 FHS）
  sed -i "s|^dir = 'logs'|dir = '${LOG_DIR}'|" "${tmp_dir}/${CONFIG_SAMPLE_NAME}"

  install -m 0600 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" \
    "${tmp_dir}/${CONFIG_SAMPLE_NAME}" "${config_file}"
  rm -rf "${tmp_dir}"

  log_success "初始配置已生成: ${config_file}"
  log_warning "请按实际环境修改（拓扑 servers、数据库、LDAP、调度器等）后再启动服务"
}

# ============================================================
# 以服务用户身份执行命令
# ============================================================
# engine 拒绝以 root 运行，任何需要 engine 配合的步骤都必须降权执行。
run_as_service_user() {
  local cmd="$1"
  if command -v runuser >/dev/null 2>&1; then
    runuser -u "${SERVICE_USER}" -- bash -c "${cmd}"
  else
    su -s /bin/bash -c "${cmd}" "${SERVICE_USER}"
  fi
}

# ============================================================
# 登记（仅无配置文件形态）
# ============================================================
# 用一次性 OTP 换取证书材料：node register 会生成节点密钥（本地生成、不外发）并落盘
# <data-dir>/certs/cluster/<host>/ 与 <data-dir>/certs/cluster/ca.crt。
register_agent() {
  if [[ "${MODE}" != "agent" || "${SKIP_REGISTER}" -eq 1 ]]; then
    return 0
  fi

  log_info "登记节点（node register）..."
  local reg_args="--portal-url '${PORTAL_URL}' --token '${TOKEN}' --data-dir '${AGENT_DATA_DIR}'"
  if [[ "${INSECURE_HTTP}" == "1" ]]; then
    reg_args="${reg_args} --insecure-http"
  fi
  if ! run_as_service_user "cd '${AGENT_DATA_DIR}' && '${INSTALL_DIR}/${SERVER_NAME}' node register ${reg_args}"; then
    log_error "登记失败：请确认 ${PORTAL_URL} 可达、OTP 未过期（控制节点 node issue-token 签发）"
    exit 1
  fi
  log_success "节点已登记，证书材料位于 ${AGENT_DATA_DIR}/certs/cluster/"
}

# ============================================================
# 权限收口
# ============================================================
# 控制节点：配置文件含数据库/LDAP 凭据，必须 0600 且归服务用户所有，否则 engine 启动时会
# 因权限校验失败而拒绝加载（见 internal/config/validate_perms_linux.go）。
# 计算节点：数据目录含节点私钥，目录 0700/密钥 0600（密钥权限由引擎在落盘时收口）。
apply_permissions() {
  log_info "设置权限..."

  # 只收口本脚本装进去的产物：INSTALL_DIR 可能是一个共享目录（例如 /usr/local/bin），
  # 对它整体 chown 会波及无关文件。
  chown "${SERVICE_USER}:${SERVICE_GROUP}" "${INSTALL_DIR}/${SERVER_NAME}"
  chmod 0755 "${INSTALL_DIR}/${SERVER_NAME}"
  if [[ -f "${INSTALL_DIR}/${EXEC_BIN_NAME}" ]]; then
    chown "${SERVICE_USER}:${SERVICE_GROUP}" "${INSTALL_DIR}/${EXEC_BIN_NAME}"
    chmod 0700 "${INSTALL_DIR}/${EXEC_BIN_NAME}"
    # Linux kernel clears security.capability on chown; always re-apply after ownership change.
    dce_apply_exec_setcap "${INSTALL_DIR}/${EXEC_BIN_NAME}" || true
  fi
  mkdir -p "${LOG_DIR}"
  chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${LOG_DIR}"
  chmod 0750 "${LOG_DIR}"

  if [[ "${MODE}" == "agent" ]]; then
    chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${AGENT_DATA_DIR}"
    chmod 0700 "${AGENT_DATA_DIR}"
    if [[ -d "${AGENT_DATA_DIR}/certs/cluster" ]]; then
      chmod 0755 "${AGENT_DATA_DIR}/certs/cluster"
      [[ -d "${AGENT_DATA_DIR}/certs/cluster/ca" ]] && chmod 0700 "${AGENT_DATA_DIR}/certs/cluster/ca"
      find "${AGENT_DATA_DIR}/certs/cluster" -mindepth 1 -maxdepth 1 -type d -exec chmod 0700 {} +
      find "${AGENT_DATA_DIR}/certs/cluster" -type f \( -name '*.key' -o -name '*.key.prev*' \) -exec chmod 0600 {} +
      find "${AGENT_DATA_DIR}/certs/cluster" -type f \( -name '*.crt' -o -name '*.crt.prev*' \) -exec chmod 0644 {} +
    fi
    log_success "权限设置完成"
    return 0
  fi

  if [[ -f "${CONFIG_DIR}/${CONFIG_NAME}" ]]; then
    chown "${SERVICE_USER}:${SERVICE_GROUP}" "${CONFIG_DIR}/${CONFIG_NAME}"
    chmod 0600 "${CONFIG_DIR}/${CONFIG_NAME}"
  fi

  log_success "权限设置完成"
}

# ============================================================
# 启动服务
# ============================================================
start_agent_service() {
  if [[ "${MODE}" != "agent" ]]; then
    # 控制节点：脚本不代为首次启动，但安装前若原本在跑，则复位回运行态
    if [[ "${WAS_ACTIVE}" -eq 1 ]]; then
      log_info "复位服务运行状态..."
      systemctl restart "${SERVICE_NAME}"
      sleep 2
      log_success "服务状态: $(systemctl is-active "${SERVICE_NAME}")"
    fi
    return 0
  fi

  log_info "启用并启动服务..."
  systemctl enable "${SERVICE_NAME}" >/dev/null 2>&1 || true
  # 用 restart 而不是 enable --now：后者对已在运行的 unit 不重启，
  # 会让已卸载（deleted）的旧二进制继续驻留到下次重启。
  systemctl restart "${SERVICE_NAME}"
  sleep 3
  log_success "服务状态: $(systemctl is-active "${SERVICE_NAME}")"
}

# ============================================================
# 摘要
# ============================================================
show_summary() {
  echo ""
  echo "========================================"
  echo "  安装完成"
  echo "========================================"
  echo ""
  log_success "组件:"
  echo "  • Engine:  ${INSTALL_DIR}/${SERVER_NAME}"
  if [[ "${MODE}" == "agent" ]]; then
    echo "  • 形态:    Agent（无配置文件）"
    echo "  • 数据目录: ${AGENT_DATA_DIR}"
    echo "  • 集群服务端: ${SERVERS}:${PORT}"
    echo ""
    log_info "服务已 enable 并 restart 启动；确认上线:"
    echo "  journalctl -u ${SERVICE_NAME} | grep -E 'Starting AGENT|Successfully registered'"
    echo "  控制节点侧应出现: Certificate fingerprint verified → Node marked online in DB"
  else
    echo "  • dc-exec: ${INSTALL_DIR}/${EXEC_BIN_NAME}"
    echo "  • 配置:    ${CONFIG_DIR}/${CONFIG_NAME}"
    echo ""
    log_info "下一步:"
    echo "  1. 编辑配置并放置密钥文件: vi ${CONFIG_DIR}/${CONFIG_NAME}"
    echo "  2. 预检: ${INSTALL_DIR}/${SERVER_NAME} check-config --config ${CONFIG_DIR}/${CONFIG_NAME}"
    echo "  3. 启动: systemctl enable --now ${SERVICE_NAME}"
  fi
  echo ""
}

# ============================================================
# 主流程
# ============================================================
main() {
  parse_args "$@"

  if [[ "${MODE}" == "agent" ]]; then
    echo "=== Design Cloud Engine 安装（Agent / 无配置文件）==="
  else
    echo "=== Design Cloud Engine 安装（Server）==="
  fi
  echo ""

  check_root
  check_binaries
  ensure_service_user
  # Agent 形态下默认沿用服务用户的主组（该用户在 IPA/系统里已有自己的组）
  if [[ "${MODE}" == "agent" && -z "${DCE_SERVICE_GROUP:-}" ]]; then
    SERVICE_GROUP="$(id -gn "${SERVICE_USER}")"
  fi
  create_directories
  stop_existing_service
  install_engine
  install_dcexec
  create_systemd_service
  if [[ "${MODE}" == "server" ]]; then
    create_config
  fi
  register_agent
  apply_permissions
  start_agent_service
  show_summary
}

# 直接执行时跑主流程；被 source 时只提供函数（便于测试在不取得 root 的情况下
# 逐个断言参数解析与 unit 生成结果）。
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
