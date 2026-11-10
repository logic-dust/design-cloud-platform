---
title: 服务管理与日常运维
linkTitle: 运维管理
weight: 60
description: dc-exec 特权部署、RPM、systemd、Windows 服务、日志轮转与常用运维命令
aliases:
  - /docs/deployment/logging
---

本文档覆盖安装完成后的进程管理、特权组件配置、日志与审计运维以及日常排障命令。首次安装见 [安装与首次部署](./install.md)，系统升级见 [系统升级与迁移](./upgrade.md)。

---

## dc-exec 特权组件配置

`dc-exec` 是 Agent 调用的特权执行助手（`cap_setuid/setgid`），用于以目标用户身份执行命令（如用户认证、文件提权操作）。生产环境中的 Agent 节点必须配置 DcExec。

推荐特权模型如下：
- **Engine 继续以普通服务用户运行，严禁以 root 常驻运行**。
- **dc-exec 不使用 setuid-root，而是赋予 `cap_setuid,cap_setgid` Linux 文件权能（File Capabilities）**。
- **密码校验等少量特权 Action 在 dc-exec 内部显式切换到 root 账户后执行**。
- **业务操作 Action 一律降权切换到请求的目标普通用户执行**。

详细安全机制参见 [启动器安全](/docs/architecture/dcexec/security)。

### 一键打包与 RPM 部署（生产主路径）

每台机器安装**同一份** `design-cloud-engine` RPM（不分节点角色）。角色在进程启动后由 hostname 对照 `cluster.servers` 自动解析。Engine 与 dc-exec 版本完全锁步，避免协议错位。

```bash
make package                    # 正式包，默认 PKG_TYPE=rpm GOARCH=amd64
# make package-dev              # 开发快照（版本带 git hash）
# GOARCH=arm64 make package     # 交叉打 arm64 包
yum install -y build/design-cloud-engine-*.rpm

# 编辑配置与密钥文件后启动（安装后不会自动启动或自启）
vi /etc/design-cloud/config.toml
/opt/dce/dc-engine check-config --config /etc/design-cloud/config.toml
systemctl enable --now design-cloud-engine
```

RPM 脚本职责划分：
- `%pre`：创建 `dce` 系统用户/组（若已存在或来自 LDAP/IPA 则复用）。
- `%post`：执行 `setcap` 赋权、收敛已知数据目录权限、应用 tmpfiles 规则。
- `%posttrans`：执行 `daemon-reload`；仅当服务原本为 active 状态时触发 `try-restart`。

**轻量 Configless 计算节点**（避免将敏感数据库密码配置下发到全集群节点）：
使用同一份 RPM 安装后，通过 `node register` 自动完成注册，并使用 `systemctl edit design-cloud-engine` 覆盖 `ExecStart` 载入精简配置。

> [!CAUTION]
> 二进制程序与数据目录必须存放在**本地文件系统**上。严禁将 Engine 或 dc-exec 部署在 NFS 或 CIFS 等网络共享存储上（网络存储不支持文件能力扩展属性，且不满足 systemd 执行语义）。

### 手动部署与赋权步骤

若不通过 RPM 包安装，可按如下命令在节点本地磁盘手动配置：

```bash
groupadd -r dce || true
useradd -r -M -g dce -s /sbin/nologin dce || true

install -m 755 build/dc-engine /opt/dce/dc-engine
install -m 700 build/dc-exec /opt/dce/dc-exec
chown dce:dce /opt/dce/dc-engine /opt/dce/dc-exec

# 赋予必要的能力集
setcap "cap_setuid,cap_setgid+ep" /opt/dce/dc-exec
getcap /opt/dce/dc-exec
```

---

## 服务生命周期管理

### 使用 systemd 管理（Linux 生产推荐）

```bash
# 1. 复制 service 单元文件
cp deployment/systemd/design-cloud-engine.service /etc/systemd/system/

# 2. 按实际运行环境编辑 WorkingDirectory 和 ExecStart
# 若 Engine 需直接绑定 80/443 特权端口，单元文件已配置 AmbientCapabilities=CAP_NET_BIND_SERVICE
vi /etc/systemd/system/design-cloud-engine.service

# 3. 启动并配置开机自启
systemctl daemon-reload
systemctl enable --now design-cloud-engine
systemctl status design-cloud-engine
```

### 使用 Windows SCM 服务管理（Windows Agent 节点）

计算节点推荐 **configless** 一键脚本（见 [节点注册与引导](./node-registration.md)）。手动注册服务示例：

```powershell
# 以管理员权限运行 PowerShell（configless，无需 config.toml）
.\dc-engine.exe service install --servers mgt01,mgt02 --port 52255 --data-dir C:\ProgramData\DesignCloudEngine\data

.\dc-engine.exe service start
.\dc-engine.exe service status
.\dc-engine.exe service stop
.\dc-engine.exe service uninstall
```

带 `config.toml` 的控制节点/特殊布局仍可使用 `.\dc-engine.exe --config <path> service install`。

**Windows 节点运维注意事项**：
- **RDP 前置条件**：确保开启远程桌面（`fDenyTSConnections=0`）且 `TermService` 处于运行中；防火墙需放通入向 RDP 端口（默认 3389）。
- **文件 ACL 策略**：数据与日志落于 `%PROGRAMDATA%\DesignCloudEngine`，系统会自动剥离父级继承，仅放行 `SYSTEM` 与 `Administrators` 读写控制。
- **能力收敛**：Windows 节点仅注册直接探测动作（如 FlexLM 许可证探测），不支持 Linux PAM 认证及 Linux 提权脚本。

---

## 日志体系与审计运维

系统日志基于 Go 标准库 `log/slog` 构建，分流为应用日志和审计日志两大通道。

### 日志类型与分流

| 日志通道 | 落点 | 写入源 | 作用与特点 |
|---|---|---|---|
| **应用日志 (App)** | stdout → journald / 容器采集器 | `internal/platform/logger`、各子系统业务代码 | 记录系统启动、心跳、业务调度与组件报警等日常运行信息 |
| **访问日志 (Access)** | stdout → journald / 容器采集器 | `internal/server/portal` 中间件 | 记录 Portal HTTP 请求路径、响应耗时、状态码与客户端 IP（>500ms 自动标记） |
| **业务审计 (Audit)** | `log.dir`/`audit.<node>.log` | `internal/audit` 审计引擎 | 关键业务事件留痕；Portal `GET /api/admin/audit/logs/audit` 由此文件尾读/SSE |
| **安全审计 (Security)**| `log.dir`/`security.<node>.log` | `internal/audit` 审计引擎 | 敏感安全事件留痕；Portal 同源 tail/stream |

### 轮转与保留策略

应用与访问日志**不在进程内轮转**：打包单元已配置 `StandardOutput=journal`，由 journald（或容器日志驱动）负责落盘、轮转与保留。

审计 / 安全日志仍落本地文件，供 Portal 管理端实时尾读；进程内只做追加写入，**不做**应用内分卷。需要控盘时由 OS 侧 `logrotate` / 运维策略接管；RPM 安装样例为 `/etc/logrotate.d/design-cloud-engine`（源码 `deployment/packaging/logrotate.design-cloud-engine`），仅覆盖 `audit.*.log` / `security.*.log`。

- **安全脱敏**：日志序列化前置拦截器对 `password`、`token`、`secret`、`otp` 等敏感字段自动掩码（`***`）。

```toml
[log]
dir = "/var/log/design-cloud"   # 仅审计/安全文件目录；app/access 走 stdout
app_level = "INFO"
access_level = "INFO"
```

---

## 常用日常运维命令

```bash
# 查看服务状态与版本
./dc-engine service status
./dc-engine --version

# 生成标准默认配置文件
./dc-engine gen-config

# 校验配置文件语法与必填项
./dc-engine check-config --config /etc/design-cloud/config.toml

# 服务端生成并签发节点一次性注册 Token
./dc-engine node issue-token --hostname compute-node-01

# 计算节点使用 Token 自动拉取证书并完成入网注册
./dc-engine node register --token <token> --portal-url https://portal.example.com:8443
```

故障现象与诊断请参考 [常见故障排查](./troubleshooting.md)；客户端软件交付参考 [发布与制品交付](./release.md)。
