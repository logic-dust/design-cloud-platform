---
title: 配置文件
linkTitle: 配置文件
weight: 20
description: 配置文件项、默认值与运行时角色判定
---

本文档描述 `config.toml` 的配置模型，内容以仓库中的 `internal/platform/config/` 实现为准。

TOML语法参看[TOML Language](https://toml.io)

## 配置边界原则

**配置文件**（`config.toml`）只放启动时必需的内容——网络监听、TLS、数据库连接、集群拓扑、日志目录。这些参数改动需要重启程序才能生效。

**数据库 settings 表**存放运行中可调整的业务参数——文件下载策略、协议代理配额等。这些参数通过管理 API 修改，无需重启。

判断标准：如果一个参数影响"程序怎么启动"，放配置文件；如果影响"程序运行中的行为"，放数据库。

当前运行时 settings domain（共 12 个）为：

- `platform`：平台名称、描述、技术支持链接、模块禁用与门户开关（`website.indexEnabled`/`website.overviewEnabled`）
- `auth`：Session 生命周期、单用户并发 Session 上限、Session 清理间隔、登录失败阈值与封禁时长、外部用户同步周期、LDAP 管理入口链接与 ID映射缓存 TTL
- `connection`：连接规格 `maxPerUserCap`（1–16）；嵌套 `proxy`：协议代理启停、本节点 ProxyConnection 容量、单用户会话上限与各协议开关（VNC/RDP/SSH）
- `file`：文件功能总开关（`enabled`）、下载/上传策略、审批控制、上传大小上限及额外定义的文件系统挂载位置（`locations[]`）
- `overview`：态势概览与监控聚合缓存刷新间隔、存储查询窗口，以及 `allowUserCluster`（普通用户是否可访问集群概览，默认 `true`）
- `storage`：集群级共享存储监控（存储配置、探测节点、采集间隔）
- `scheduler`：调度器类型（`type`）、调度器环境初始化脚本（`envScript`，支持节点覆盖）与作业列表缓存 TTL
- `license`：许可证服务状态与特性查询缓存 TTL，以及 FlexLM `lmutil` 默认可执行文件路径（支持节点级覆盖；单个许可证服务可再覆盖）
- `agent`：Agent 全局复合配置（dc-exec 路径、VNC 发行版/参数/扫描周期、硬件监控与额外磁盘监听列表；支持通过节点 Settings API 进行单节点差异化稀疏覆盖与在线热更推送）
- `alert`：系统与硬件资源告警策略（CPU、负载、内存、磁盘告警，支持节点级覆盖）及共享存储容量告警（`sharedStorage`）
- `desktop`：桌面客户端分发配置（分发功能启停、支持平台、最大保留资产数）
- `cert`：集群节点 mTLS 证书自动续期策略（续期模式、提前天数、重试与退避间隔）

## 配置来源与优先级

运行时配置按以下阶段加载，优先级由低到高：

1. 内部默认值
2. 配置文件（路径由 `--config` 指定，默认 `config.toml`）
3. 环境变量覆盖（白名单见下）

**启动模式判定**（两条互斥启动面）：

| 顺序 | 命中条件 | 判定 |
| :--- | :--- | :--- |
| 冲突 | `--config` 与 `--servers` 同时显式传入 | ❌ 报错退出（二选一） |
| 冲突 | `--port` / `--data-dir` 未搭配 `--servers` | ❌ 报错退出（Join 面专用） |
| 1 | 仅 `--config` | 配置文件模式 |
| 2 | 仅 `--servers` | Join / Configless Agent（纯内存装配，不读配置文件） |
| 3 | 两者皆无 | 默认 `config.toml` 存在则走配置文件模式；**不存在则报错退出**并打印两段用法 |

### Configless Agent（Join 面）引导参数

无 `config.toml` 的计算节点可仅用命令行引导（`--log-dir`/`--log-level` 为运行期覆盖项，两种模式都生效）：

```bash
./dc-engine \
    --servers mgt01,mgt02 \
    --port 52255 \
    --data-dir /var/lib/design-cloud \
    --log-dir /var/log/design-cloud \
    --log-level INFO
```

| 参数 | 说明 |
| :--- | :--- |
| `--servers <hosts>` | 集群 Server 主机名/IP 列表（`,` 分隔，不含端口）。不接受 `host:port` 或拓扑 pattern |
| `--port <int>` | 集群 gRPC 端口，缺省 52255 |
| `--data-dir <path>` | 证书基准目录（Linux 缺省 `/var/lib/design-cloud`，Windows 缺省 `%PROGRAMDATA%\DesignCloudEngine`） |
| `--log-dir <path>` | 审计/安全日志目录覆盖项（两种模式生效；应用/访问日志仍写 stdout） |
| `--log-level <level>` | 应用日志级别覆盖项 `DEBUG/INFO/WARN/ERROR`（两种模式生效，非法值报错退出） |

> `--port` / `--data-dir` **仅 Join 面合法**；与 `--config` 同场或单独出现 → Exit 1。
> 证书路径只认约定目录，无 `--cert/--key/--ca`。
> 命令行是唯一引导事实来源（无引导类环境变量）。
> 一键安装：Linux 用 `deployment/install.sh agent`（角色是位置参数，另有 `server`；见 install.md / node-registration.md），
> Windows 用 `deployment/install-win-agent-configless.ps1`，两者都无需 `config.toml`。
> 根命令：`--servers`（集群 Server 主机列表）+ `--port`（集群 gRPC 端口，缺省 52255）；
> `node register`：`--portal-url` + `--token` 必填，不读 config.toml。未知标志一律拒绝。

## 路径解析

路径根分两套布局，**ConfigDir 与 BaseDir 不得混用**：

### FHS 四根（RPM / 打包部署，写死）

| 代号 | 路径 | 内容 |
| :--- | :--- | :--- |
| **ConfigDir** | `/etc/design-cloud` | `config.toml`、密钥文件（`dbpwd` / `ldapbindpwd`）、默认 Portal TLS |
| **DataDir** | `/var/lib/design-cloud` | 集群 mTLS 证书、sqlite 等持久状态 |
| **LogDir** | `/var/log/design-cloud` | 审计/安全日志目录；app/access 走 stdout → journal |
| **RuntimeDir** | `/run/design-cloud` | PID flock、dc-exec token（开机重建） |

另有 **InstallPrefix** `/opt/dce`（二进制），不参与业务路径拼接。

打包示例与 unit 使用上述绝对路径；不运行时探测「是否 RPM」。

### BaseDir（无包同树部署）

**BaseDir** = `dirname(abs(--config))`，是配置文件模式下**相对路径的唯一拼接根**。
配置与二进制常放同一树（如 `/opt/dce/config.toml`），相对的 `dbpwd`、`logs`、`certs/portal` 等均 `Join(BaseDir, …)`。

打包时 `--config /etc/design-cloud/config.toml` 会使 BaseDir **数值上等于** ConfigDir，但概念仍是两回事：ConfigDir 是产品 FHS 约定；BaseDir 是解析器对本次所读配置文件的派生值。

### 集群证书与 `data_dir`

集群 mTLS 落在 **DataDir**（不是 BaseDir / ConfigDir）：

```text
<DataDir>/certs/cluster/
├── ca.crt                         # 集群 CA 公钥 (0644)
├── ca/
│   └── ca.key                     # 集群 CA 私钥 (0600，仅集群 Server 持有)
└── <nodeID>/
    ├── <nodeID>.crt               # 节点证书 (0644)
    └── <nodeID>.key               # 节点私钥 (0600)
```

| 模式 | DataDir 来源 |
| :--- | :--- |
| 配置文件 | 顶层 `data_dir`；**空则回退 BaseDir**（无包同树零配置） |
| Join / configless | CLI `--data-dir`；缺省 Linux `/var/lib/design-cloud`（Windows `%PROGRAMDATA%\DesignCloudEngine`） |

> **与内部默认的叠加**：`Load` 先应用 `getDefault()`（打包向，含绝对 `data_dir=/var/lib/design-cloud`），再解码用户文件。
> 源码树同树部署若希望证书跟配置目录走，必须在 toml 里**显式** `data_dir = ""`（见仓库根 `config.dev.toml`）；省略该键会继承默认的 `/var/lib/...`。

- `certs/cluster` 目录必须为 **0755**（共享存储上任意节点需可穿透读取 `ca.crt`）；
- `ca/` 与节点子目录为 **0700**，公钥文件 0644、私钥文件 0600；
- Portal TLS（未显式配置时）：`<BaseDir>/certs/portal/portal.{crt,key}`（打包示例可写绝对 ConfigDir 路径）；
- 相对的 `log.dir` / `password_file` / `database.path` 等 → 拼到 **BaseDir**；绝对路径原样尊重。

应用与访问日志**始终写 stdout**（生产由 journald / 容器日志驱动采集），**不**再落 `log.dir` 下的 `app.*` / `access.*` 文件。

审计与安全日志仍落文件，命名为 `audit.<hostname>.log`、`security.<hostname>.log`（目录由 `log.dir` / `--log-dir` 决定），供 Portal 管理端 tail / SSE。进程内只追加写入；控盘与归档交给 OS 侧 `logrotate`（RPM 样例见 `/etc/logrotate.d/design-cloud-engine`）。

TurboVNC 相关路径是目标 Agent 节点上的本地绝对路径，不按配置文件目录解析，也不会在 Server 节点做可访问性检查。

## 顶层路径字段

| 字段 | 默认值（示例 / 打包） | 说明 |
| :--- | :--- | :--- |
| `data_dir` | `/var/lib/design-cloud` | 持久状态根（集群证书等）；空则回退 BaseDir |

## 节点身份

节点身份（NodeID）严格等于 `os.Hostname()` 的返回值，不可通过配置文件覆盖。Windows 上该值是 Physical DNS 主机名，通常不等于 `$env:COMPUTERNAME`。

## 命令行参数与环境变量

| 配置项       | 命令行参数      | 环境变量                          | 说明                                  |
|-----------|------------|-------------------------------|-------------------------------------|
| 日志级别      | `--log-level` | `DCE_LOG_APP_LEVEL`           | `DEBUG` / `INFO` / `WARN` / `ERROR`（覆盖 `log.app_level`；环境变量非法值被静默忽略，命令行非法值报错退出） |
| Portal 端口 | —          | `DCE_PORTAL_PORT`             | 覆盖监听端口                              |
| 数据库主机     | —          | `DCE_DATABASE_HOST`           | —                                   |
| 数据库端口     | —          | `DCE_DATABASE_PORT`           | —                                   |
| 数据库用户     | —          | `DCE_DATABASE_USER`           | —                                   |
| 数据库库名     | —          | `DCE_DATABASE_NAME`           | —                                   |
| 数据库密码     | —          | `DCE_DATABASE_PASSWORD`       | 推荐用 `password_file` 替代              |
| 数据库密码文件   | —          | `DCE_DATABASE_PASSWORD_FILE`  | 覆盖配置文件中的 `password_file` 路径         |
| LDAP 绑定密码 | —          | `DCE_USER_LDAP_BIND_PASSWORD` | —                                   |
| LDAP 绑定密码文件 | —       | `DCE_USER_LDAP_BIND_PASSWORD_FILE` | 覆盖配置中的 `bind_password_file` 路径  |

> `--config` 默认指向工作目录下的 `config.toml`；engine 另提供 `gen-config` 子命令，生成带注释的
> `config.example.toml` 模板供部署时复制修改。

### 配置预检（check-config）

升级二进制后、启动服务前，建议先校验运行期配置是否仍与新版本 schema 兼容：

```bash
./dc-engine check-config --config /etc/design-cloud/config.toml
```

该命令执行完整的加载流程（TOML 解析 → 运行时解析 → 业务校验），通过即表示 engine 能完成
配置加载阶段；失败时会精确指出问题字段与行号，例如字段重命名导致的 `unknown field(s)`：

```
Error: config check failed for config.toml: failed to parse config.toml: unknown field(s) in config file

41|   { name = "cent7" },
  |     ~~~~ unknown field
```

> **升级注意**：`config.toml` 是运行期配置，不会随二进制更新自动同步。当代码重构了配置字段名
> （如 `name` → `hosts`），旧配置会因严格解析而启动失败，需按报错同步修改后重启。
> `.vscode/remote-build-debug.sh` 已在启动 dlv 前自动执行此预检，失败即中止并提示。

节点角色由 `cluster.servers` 列表与当前节点名称自动推断，无需手动指定：

| 角色     | 判断条件                                               | 说明                   |
|--------|----------------------------------------------------|----------------------|
| Server | 节点主机名命中 `cluster.servers` 展开后的列表         | 运行 Portal、Control 服务 |
| Agent  | 所有启动 engine 的节点无条件运行                     | 运行执行器和本地能力，上报遥测与心跳 |

规则说明：

- `servers` 是字符串数组，支持主机名范围展开（如 `servers = ["mgt01", "mgt02"]` 或 `["mgt[01-02]"]`）
- 本机 hostname 出现在 `servers` 中则启用 Server 角色；未出现则为纯 Agent 角色
- 无论是否担任 Server，所有节点均无条件运行 Agent（统一全链路本地自监控与就近调度能力）

## 配置项参考

### `[Log]`

| 字段             | 默认值    | 说明                        |
|----------------|--------|---------------------------|
| `dir`          | `/var/log/design-cloud` | 审计/安全日志目录（相对路径则相对 BaseDir；可用 `--log-dir` 覆盖）。应用与访问日志始终写 stdout |
| `app_level`    | `INFO` | 应用日志级别                    |
| `access_level` | `INFO` | 访问日志级别                    |

### `[Portal]`

| 字段                     | 默认值     | 说明                                                            |
|------------------------|---------|---------------------------------------------------------------|
| `host`                 | `""`    | 监听地址，留空表示绑定全部接口                                               |
| `port`                 | `"443"` | 监听端口（字符串）                                                     |
| `tls`                  | `false` | 是否启用 TLS（HTTPS）                                               |
| `cert`                 | `""`    | TLS 证书路径，留空使用约定路径                                             |
| `key`                  | `""`    | TLS 私钥路径，留空使用约定路径                                             |
| `behind_reverse_proxy` | `false` | Portal 前是否有反向代理                                               |
| `trusted_proxy_cidr`   | `""`    | 可信反向代理网段（CIDR，如 `10.0.0.0/8`）。`behind_reverse_proxy=true` 时**必填**，启动时校验格式。只有该网段内的直连来源才会被当作代理跳解析 `X-Forwarded-For`；请勿配置过宽 |
| `read_header_timeout`  | `8`     | 请求头读取超时，秒（范围 5-15）                                            |
| `read_timeout`         | `60`    | 请求体读取超时，秒（0=禁用；范围 30-300）                                     |
| `idle_timeout`         | `60`    | 空闲连接超时，秒（范围 10-180）                                           |
| `rate_limit`           | `200`   | 公共 API IP 级速率限制，req/s（范围 1-10000）                             |
| `rate_burst`           | `600`   | 公共 API IP 级突发上限（范围 >= rate_limit 且 <= 20000）                  |
| `transfer_rate_limit`  | `10`    | 文件传输发起速率限制，req/s（范围 1-500）                                 |
| `transfer_rate_burst`  | `30`    | 文件传输突发上限（范围 >= transfer_rate_limit 且 <= 1000） |
| `ws_rate_limit`        | `10`    | WebSocket 握手独立速率限制，req/s（范围 1-500），与文件传输限流相互独立             |
| `ws_rate_burst`        | `30`    | WebSocket 握手突发上限（范围 >= ws_rate_limit 且 <= 1000）            |
| `websocket_host`       | `""`    | 协议代理 WebSocket 的 Origin/Host 校验（空=不校验）。非空时：无 `Origin` 的请求拒绝；允许 `wails://*`、`http://localhost*`、`http://127.0.0.1*`；或 `Origin: https://<websocket_host>` 且请求 `Host` 同值。用于防浏览器 DNS rebinding，不是「客户端目标地址覆盖」 |

> [!NOTE]
> 门户介绍页（`/`）与集群概览大屏（`/overview`）的启闭开关已收敛至 **Settings 系统设置（`domain: platform` -> `website`）** 动态管理，支持管理员在线热更新，无需在配置文件中维护。

`Cert` 和 `Key` 必须同时设置或同时留空。

#### `BehindReverseProxy` 说明

当 Portal 前面有 **Nginx、HAProxy、云负载均衡（ALB/CLB）** 等反向代理时，将此项设为 `true`。
服务器将从 `X-Forwarded-For` 请求头提取客户端真实 IP，用于 IP 限速、IP 封禁等安全功能。

**必须同步在反向代理侧配置转发头**，否则该头为空或可被客户端伪造：

```nginx
# Nginx
proxy_set_header X-Forwarded-For $remote_addr;
```

```haproxy
# HAProxy
option forwardfor
```

> AWS ALB / GCP Load Balancer 会自动注入 `X-Forwarded-For`，无需额外配置。

⚠️ **没有反向代理时请勿启用此选项**：客户端可自行构造 `X-Forwarded-For` 头绕过 IP 封禁等安全控制。

⚠️ **启用时必须配置 `trusted_proxy_cidr`**：仅信任来自该网段的代理跳。解析 `X-Forwarded-For` 时，系统从右向左跳过位于可信网段内的代理 IP，取最近一个**不可信**地址作为客户端真实 IP——客户端伪造的私网/环回前缀会被忽略，无法借此伪装成内网地址绕过封禁。网段应只覆盖真实代理（如 `10.0.0.0/8` 中的代理子网），配置过宽会重新引入伪造风险。

#### 超时说明

| 字段                    | 说明                                                                                                                                                                                                                              |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `read_header_timeout` | 从 TCP 连接建立到读完 HTTP 请求头的超时。防护慢速头部攻击（Slowloris），底层 Go `http.Server` 默认 0（不限制），此处设 5-15s 提供基本防护。                                                                                                                                   |
| `read_timeout`        | 从连接建立到读完整个请求体（含 body）的超时。常规 API 在此时间内应完成请求体读取；文件上传/下载路由通过 `disableReadTimeout` 中间件豁免该超时，见 `internal/server/portal/middleware/route_policy.go`（`PolicyFileTransfer`）。 |
| `idle_timeout`        | keep-alive 连接上等待下一个请求的最长时间。超时后关闭空闲连接，释放连接池资源。建议与前置反向代理的 `keepalive_timeout` 保持一致（如 Nginx 默认 60s）。                                                                                                                               |

#### 限流说明

系统维护三层限流：

| 层级   | 粒度      | 默认速率                    | 作用域                | 说明                                          |
|------|---------|-------------------------|--------------------|---------------------------------------------|
| IP 级 | 请求来源 IP | 200 req/s（`rate_limit`） | 公共 API（登录/版本/节点注册） | 熔断器级别，仅拦截 DDoS 攻击                           |
| 用户级  | 已认证用户名  | 50 req/s                | 所有已认证 API          | 绑定到 session 内存缓存，无独立 store，随 session 过期自动回收 |
| 传输级  | 已认证用户名  | 10 req/s（`transfer_rate_limit`） | 文件传输启动             | 防止过度发起传输请求                                  |

`rate_limit` / `rate_burst` 仅控制第一层（IP 级公共 API）。用户级限制在 `internal/server/service/session/cache.go` 的
`defaultSessionRateLimit` 常量中定义（50 req/s）；传输级限制由 `transfer_rate_limit` / `transfer_rate_burst`
配置项控制（默认 10 req/s / 30 burst），在 `internal/server/portal/middleware/route_policy.go` 中生效。

### `[cluster]`

| 字段                       | 默认值     | 说明                                          |
|--------------------------|---------|---------------------------------------------|
| `port`                   | `52255` | gRPC 通信端口（范围 1024-65535）                                   |
| `heartbeat_interval`     | `60`    | 心跳周期，秒（范围 10-300；系统会自动加上 ±20% 的 Jitter 防止羊群效应）        |
| `connect_timeout`        | `30`    | 连接超时，秒（3–60）                                |
| `node_heartbeat_timeout` | `180`   | 最后心跳后多久判定节点离线，秒（30-600；必须 >= heartbeat_interval，否则启动校验失败）                     |
| `node_check_interval`    | `120`   | 服务端扫描过期节点状态的周期，秒（10-300）                    |
| `lease_ttl`              | `0`（即 30） | 控制面 Leader 租约有效期，秒（0 使用默认 30；可选 5-300）。调小可加快故障接管，代价是 DB 写频率上升 |
| `renew_interval`         | `0`（即 15） | 租约续约间隔，秒（0 使用默认 15；可选 1-120，实际节拍带 ±20% 抖动）。必须小于 `lease_ttl`，否则启动校验失败 |
| `execution_retention_days` | `0`（即 7） | 终态执行记录保留天数（0 使用默认 7）。超期记录由每小时的低频清理任务（单活租约）批量删除，防表无界膨胀 |
| `tls_server_name`        | `""`    | 覆盖 mTLS 的 ServerName 校验（支持多节点共享 Cluster 证书） |


### `servers` (字符串数组)

`servers` 是字符串数组，定义集群管理节点列表：

**示例：**

```toml
# 单台管理节点（全节点均运行 Agent）
servers = ["mgt01"]

# 多台高可用管理节点（支持表达式展开）
servers = ["mgt01", "mgt02"]
# 或：
servers = ["mgt[01-02]"]
```

> 只要节点不在 `servers` 列表中，Engine 启动时就自动将其识别为纯 Agent 角色。
> 纯 Agent 节点无需额外任何配置项来声明自己是 Agent。

### `[Database]`

| 字段                  | 默认值             | 说明                                                |
|---------------------|-----------------|---------------------------------------------------|
| `type`              | `postgres`      | `postgres` 或 `sqlite`                             |
| `path`              | `/var/lib/design-cloud/dce.sqlite.db` | SQLite 文件路径（相对路径则相对 BaseDir） |
| `host`              | `127.0.0.1`     | PostgreSQL 主机                                     |
| `port`              | `5432`          | PostgreSQL 端口                                     |
| `user`              | `dc`            | 用户名                                               |
| `password`          | `""`            | 密码（优先级高于 `password_file`）                         |
| `password_file`     | `/etc/design-cloud/dbpwd` | 密码文件路径（相对则相对 BaseDir）。若 `password` 为空则读取此文件；权限须为 owner-only |
| `name`              | `dce`           | 数据库名                                              |
| `ssl_mode`          | `disable`       | PostgreSQL SSL 模式：`disable` / `allow` / `prefer` / `require` / `verify-ca` / `verify-full`（选 `disable` 时启动会输出明文连接告警） |
| `max_idle_conns`    | `10`            | 空闲连接数上限（逻辑约束：必须 `<=` `max_open_conns`）            |
| `max_open_conns`    | `25`            | 打开连接数上限（必填：`> 0` 且 `<= 500`）                      |
| `conn_max_lifetime` | `0`             | 连接最大复用时间，秒（`0`=不限；如需设置则 `>= 600` 且 `<= 86400`）    |
| `ssl_ca_file`       | `""`            | PostgreSQL SSL CA 证书路径                                 |

### `[Admin]`

| 字段                   | 默认值     | 说明                                                                      |
|----------------------|---------|-------------------------------------------------------------------------|
| `enable_three_roles` | `false` | `true`：启用三员模式（auditadmin/systemadmin/securityadmin）；`false`：单一管理员模式（默认） |

- **`false`（默认）**：单一管理员模式。启动时创建 `dceadmin` 账号（Role=4，superadmin），通吃所有管理 API 端点。在此模式下，三员角色（
  `auditadmin`/`systemadmin`/`securityadmin`）不可登录。
- **`true`**：三员模式。启动时创建三个平行管理员账号——`auditadmin`（审计管理员，Role=1）、`systemadmin`（系统管理员，Role=2）、
  `securityadmin`（安全管理员，Role=3）。各角色只能访问对应的管理 API 端点。超级管理员 `dceadmin`(4) 在该模式下不可登录。
    - **强制账号前缀**：为彻底防止越权并方便审计，在三员模式下赋予管理员角色时，系统会在服务层强制校验账号名前缀。如果不符合规定，将拒绝赋权操作：
        - 系统管理员账号名必须以 `systemadmin_` 开头
        - 安全管理员账号名必须以 `securityadmin_` 开头
        - 审计管理员账号名必须以 `auditadmin_` 开头
- **关于管理员作用域（AdminScope）**：普通用户可通过授权机制被授予 `sysadmin` 路由的临时权限。目前系统在**三员模式下严格禁止设置管理员作用域
  **，所有相关接口的调用将会被拦截并返回错误。只有在单一管理员模式下，才允许超级管理员对普通用户进行角色权限设置。

#### 初始管理员密码

内置管理员账号的**初始密码在每次启动时随机生成**，通过启动日志输出。部署者应在首次启动后从日志中获取密码并立即登录修改。

初始管理员账号状态为 **PasswordExpired**，首次登录后强制要求修改密码。若账号已存在（非首次启动），不会重新生成密码。

日志输出格式示例：

```
[bootstrap] Created admin user "dceadmin" (role=superadmin) with temporary password: <随机密码>
```

### `[User]`

| 字段          | 默认值    | 说明                                |
|-------------|--------|-----------------------------------|
| `auth_mode` | `ldap` | 用户认证后端：`local`（本机 shadow）或 `ldap` |

### `[User.Ldap]`

`auth_mode = "ldap"` 时生效。

| 字段                         | 说明                                          |
|----------------------------|---------------------------------------------|
| `server`                   | LDAP 地址，**必须带协议前缀**，如 `ldap://ldap.example.com:389` 或 `ldaps://...`（缺失前缀会在启动校验中被拒绝） |
| `user_base_dn`             | 用户搜索基点（必填）                                  |
| `bind_dn`                  | 服务账户 DN                                     |
| `bind_password`            | 服务账户密码（优先级高于 `bind_password_file`）          |
| `bind_password_file`       | 默认 `/etc/design-cloud/ldapbindpwd`；相对路径则相对 BaseDir。若 `bind_password` 为空则读取此文件 |
| `id_attribute`             | 用户唯一标识属性，默认 `uid`                           |
| `user_filter`              | 用户过滤器，默认 `(objectClass=posixAccount)`       |
| `group_base_dn`            | 组搜索基点。留空时运行时回落为 `user_base_dn`；代码默认值为开发环境示例 `cn=groups,dc=dev,dc=vm`，生产环境应显式配置 |
| `group_filter`             | 组过滤器，默认 `(objectClass=posixGroup)`          |
| `password_modify`          | 是否启用 RFC 3062 密码修改扩展                        |
| `tls_ca_path`              | LDAP CA 文件路径                                |

> **TLS 说明**：不提供 `tls_insecure_skip_verify` 配置项（刻意不暴露），`ldaps://` 连接一律严格校验证书。目录服务器（FreeIPA、AD 等）对密码修改（RFC 3062）强制要求安全连接——配置 `ldap://` 明文时改密请求会被服务器以 `Confidentiality Required (13)` 拒绝。**改密场景必须配置 `ldaps://`**。

## 数据库运行时配置

以下参数不在配置文件中，而是存储在数据库 `settings` 表中，通过管理 API（如 `PUT /api/admin/system/settings/:domain`）修改，*
*无需重启**：

- **文件 (file)**：文件功能总开关（`enabled`）、下载与上传策略、审批控制、上传大小上限及额外文件系统挂载位置（`locations[]`，其读写权限由文件总开关、操作开关、用户权限、审批策略和底层操作系统权限共同裁决，不在配置中做“只读”假定）。
- **认证 (auth)**：Session TTL、单用户最大 Session 数、最大登录失败次数（用户默认 5 / IP 默认 30）、自动封禁时长（`userBanDuration` 默认 300s、`ipBanDuration` 默认 86400s，与 schema 默认一致）、用户同步周期与 ID 映射缓存 TTL 等。
- **协议代理 (proxy)**：ProxyConnection 启停、本节点并发容量（`maxProxyConnections`）、单用户会话上限（`maxProxyConnectionsPerUser`）与允许协议列表等。
- **连接 (connection)**：每用户最大持久连接记录配额与连接列表缓存 TTL。
- **Agent 配置 (agent)**：Agent 执行时必需的本地依赖与能力配置（支持单节点个性化覆盖）：
    - **DcExec**: dc-exec 可执行文件路径（默认 `/opt/dce/dc-exec`）。
    - **VNC**: TurboVNC 的安装目录（`binDir`）、扫描周期与启动参数。
    - **Monitor**: `nvidia-smi` 路径、额外磁盘监听挂载点列表（`diskMounts`）及资源指标上报间隔（`metricsInterval`）。
- **共享存储 (storage)**：定义的集群共享存储挂载路径，以及监控探针的下发。
    - **probeNodes 配置原则**：共享存储由指定节点探测容量并上报到 `storage_reports` 表。
      同一存储配置多个 `probeNodes` 可形成冗余互备（一个节点宕机不影响采样连续性），
      但每个节点都会独立执行 `statfs`。为兼顾冗余与效率，建议：
        1. **优先只用 server 节点**作为 probe。管理节点常驻、网络稳定、必然挂载共享存储，不会随用户作业启停而断流。
        2. `probeNodes` 配 2~3 个 server 节点即可冗余互备。
        3. 计算节点仅在所有 server 节点都不挂载该存储时才加入。
        4. 不要把所有节点都配为 probe——冗余过度只会增加重复的 `statfs` 调用，无额外收益。
- **桌面客户端分发 (desktop)**：分发功能启停、支持平台及保留资产上限。
- **概览 (overview)**：各模块态势刷新间隔、存储容量历史查询窗口，以及普通用户集群概览可见性（`allowUserCluster`，默认允许）。
- **调度器 (scheduler)**：调度器类型（`type`：`lsf`/`slurm`/`openlava`，默认 `lsf`；运行中修改即时生效，驱动与元数据随之切换，作业列表缓存同时清空）、调度器环境初始化脚本（`envScript`，可选绝对路径；Agent 执行调度器命令前 `source`，可经节点设置覆盖）与作业列表缓存 TTL。
- **告警 (alert)**：CPU、负载、内存、磁盘告警阈值（节点可覆盖）及共享存储容量告警（`sharedStorage`）。系统负载使用 `load.maxLoadPercent`（load1/核数×100，**50–200**，默认 **110**）；`load.enabled` 为子开关。
- **证书 (cert)**：集群 mTLS 证书自动续期策略。
- **许可证 (license)**：`statusCacheTTL` / `featuresCacheTTL`（**60–900** 秒，默认 **300**）及默认 `lmutil` 绝对路径（节点可覆盖；单个许可证服务的 `lmutilPath` 可再覆盖）。

## Linux XRDP 设计节点（EDA，节点侧配置）

以下由**部署/镜像**维护，不由控制面写入；本节即节点侧配置事实源，相关运维待办见 `todo/README.md`。

| 项 | EDA 要求 |
|----|----------|
| `/etc/xrdp/sesman.ini` → `KillDisconnected` | **必须为 `false`**（断线保桌面；禁止用断线超时杀会话） |
| XRDP 版本 | 建议 **≥ 0.10** |
| `/etc/xrdp/startwm.sh`（或等价） | 在**正常 Logout 结束**路径追加 `loginctl terminate-session`（必要时 `kill-session`），减轻 Logout 后 session 卡在 `closing` |
| `/etc/systemd/logind.conf` → `KillUserProcesses` | 共享设计机**默认保持 `no`**；与 tmux/后台仿真冲突 |

控制面通过 `connection-probe` 对托管 XRDP 连接做 Stop 失败占号（`terminating`）与 Logout 残留收尸（`session_ending_confirmed`）；不替代上述节点配置。

## 安全与资源约束

为保障系统在高负载下的稳定性，Engine 对关键资源配置项具有**强制正数约束**。不允许通过设置为 `0`
或负数来寻求“无限资源”，必须由运维人员根据物理环境填入明确的自然约束：

- **日志**：应用/访问走 stdout（journald）；审计/安全文件由 OS 侧轮转控盘，进程内不再做分卷。
- **数据库连接防耗尽**：`Database.MaxOpenConns` 必须 `> 0` 且 `<= 500`；同时 `MaxIdleConns` 必须 `<=` `MaxOpenConns`。
- **ConnMaxLifetime**：可选配置。设为 `0` 表示不限制连接生存时间（信任 Go database/sql 自动检测死连接）。如需设置，允许范围
  `600`~`86400` 秒。

此外，系统还具备以下安全防御机制：

- **配置文件权限**：权限过宽时（group/other 有任意权限），程序自动修正为 `0600`，修正失败则拒绝启动。
- **敏感文件检查**：`database.password_file` 必须是 owner-readable 且 group/other 无任何权限的常规文件。
- **运行身份限制**：不建议以 root 运行，程序启动时会检查并拒绝。

## 最小示例

```toml
data_dir = "/var/lib/design-cloud"

[log]
dir = "/var/log/design-cloud"

[portal]
port = "8080"

[cluster]
port = 15051
heartbeat_interval = 60
connect_timeout = 30
node_heartbeat_timeout = 180
node_check_interval = 120
lease_ttl = 30            # 0 = 默认 30；调小可加快故障接管
renew_interval = 15       # 0 = 默认 15；必须小于 lease_ttl
execution_retention_days = 7  # 0 = 默认 7；终态执行记录保留天数
servers = [
  "mgt01",
]

[database]
type = "postgres"
host = "127.0.0.1"
user = "dce"
name = "dce"
password_file = "/etc/design-cloud/dbpwd"

[user]
auth_mode = "local"
```
