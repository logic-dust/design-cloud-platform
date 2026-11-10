---
title: Portal API
linkTitle: Portal API
weight: 900
description: Portal API 架构总览、基础约定、权限模型与领域导航
---

## 文档导航

Design Cloud Engine 的 Portal API 按调用者**访问面**划分为三部分：

- 🔓 **公开 API（无需认证）**：登录、版本检查、节点自助注册等免认证端点，集中列于本文档[公开端点](#公开端点无需认证)一节；
- 👤 **[Portal API · 用户 API](./user.md)**：登录用户可调用的端点（会话、个人资料、连接、应用、调度作业、文件浏览与上传下载等）；
- 🛡️ **[Portal API · 管理 API](./admin.md)**：三员与系统管理员专属端点（System Admin、Security Admin、Audit Admin）。

> 公开端点是用户 API 中带"鉴权：否"标注的端点，在各自业务域下保持完整上下文；此处集中列出便于整体检索。

---

## 基本约定

### 服务地址

- **Portal REST API**: `http(s)://<host>:<port>/api`
- **Portal WebSocket / 代理**: `ws(s)://<host>:<port>/api`

### 认证方式

平台将 Portal API 划分为三种访问面：

| 访问面 | 适用对象 | 承载文档 |
|---|---|---|
| **公开 API** | 外部调用方（桌面客户端、计算节点、浏览器），免认证 | 本文档[公开端点](#公开端点无需认证)汇总 + [用户 API](./user.md) 域内标注 |
| **用户 API** | 登录用户（桌面客户端、前端开发者） | [用户 API](./user.md) |
| **管理 API** | System / Security / Audit Admin | [管理 API](./admin.md) |

除下方[公开端点](#公开端点无需认证)外，所有 API 默认均需认证：

1. **HTTP 请求**：在 HTTP Header 中携带 Bearer Token：
   ```http
   Authorization: Bearer <session-token>
   ```
2. **协议代理 WebSocket**：在 `GET /api/connections/:id/proxy-connection`（VNC/RDP/XRDP）或 `GET /api/connections/ssh?resourceId=`（SSH）握手 Header 中携带 Portal Session（仅 Bearer，无子协议兼容）：
   ```http
   Authorization: Bearer <session-token>
   ```

### 统一返回格式

所有 REST API 统一采用标准化 JSON 包装结构：

```json
{
  "message": "error details or success summary",
  "data": {}
}
```

- 成功时：`data` 包含返回的业务数据对象/数组，`message` 选填或为空；
- 失败时：`data` 为 `null` 或空对象，`message` 包含精确的人类可读错误原因。

### 列表分页（limit / offset）

多数 Portal **列表**共用页大小上下界：**默认 500**、**最大 2000**。按端点对非法参数的处理分三种风格：

| 风格 | 典型端点 | `limit` / `offset` 行为 |
|---|---|---|
| **静默截断** | 多数 Admin / 用户目录列表（sessions、apps、licenses、nodes…） | 缺省 `limit=500`；超出 1～2000 或非法数字时压到合法范围；`offset` 负值按 0 |
| **严格 422** | 工单列表及评论 / 事件 / 项目列表 | 越界或非法数字返回 `422 invalid limit` / `invalid offset`；`offset` 允许 0～10000 |
| **缺省由服务端补全** | 审计 `GET /api/admin/audit/executions` | 参数可省略；省略后服务端按默认 500、上限 2000 生效；非法数字返回 `400` |

- **静默截断**类响应：`{ items, total }`（**不回显**生效的 `limit`/`offset`）。见 [管理 API](./admin.md) 与 [用户 API](./user.md) 对应章节。
- **工单**响应：`{ items, totalCount, limit, offset, hasMore, nextAfterSort?, nextAfterId? }`（见 [用户 API · Tickets](./user.md#tickets)）。**续页**（尤其默认 `sort=sortOrder`）应使用 keyset 游标 `afterSort` / `afterId`（与 offset 二选一，游标优先）；`sortOrder` 下 `offset ≥ 2000` 且无游标时返回 `422`；keyset 请求的 `totalCount` 为 **`-1`**（不重算总数）。
- **审计执行记录**响应：`{ items, total, limit, offset }`（见 [管理 API · GET /api/admin/audit/executions](./admin.md)）。
- **不适用**此约定：调度作业列表、文件目录窗口、告警 history、下载审批列表等（各有独立上限或本地分页）。

### JSON 字段命名

发版前对外契约按**单轨**演进，不为旧客户端保留并行 key。

| 范围 | 约定 |
|---|---|
| **引擎定义的 `data` 载荷** | 统一 **camelCase**（如 `nodeId`、`resourceId`、`lastAccessedAt`）。请求 Body 与响应字段同名。 |
| **调度器作业 / 主机 / 队列** | **例外**：`jobs` / `hosts` / `queues` 记录保留调度器 CLI/JSON 的**原生字段名**（Slurm 多为 `snake_case`，LSF 多为大写）；建议展示列 `fields.job` / `fields.host` / `fields.queue`、作业核心语义键 `keys` 与动作状态集合 `actions` 由 `GET /api/scheduler/meta` 提供。定位用户/状态列用 `keys.user` / `keys.status`，不要假定固定下标。服务端不对原生键做大小写或命名转换。详见 [用户 API · Scheduler](./user.md#scheduler)。 |
| **数据库** | 表/列仍为 snake_case；不通过 Portal JSON 暴露列名。 |

### 路径命名约定

- **`me`**：当前登录用户本人的资源，如 `/api/users/me`、`/api/scheduler/jobs/me`、`/api/overview/me`。
- **`current`**：当前这一个会话实例，如 `GET /api/sessions/current`（当前会话详情）、`DELETE /api/sessions/current`（登出当前设备）。
- **本人全部会话**：`GET /api/sessions` 返回当前用户在所有设备上的会话列表；`DELETE /api/sessions/:id` 下线指定设备，`DELETE /api/sessions` 登出全部设备。
- **SSE 流**：实时流使用独立路径并以 `/stream` 结尾（如 `/api/overview/cluster/stream`），与同名 JSON 快照路径分离；`/api/events` 为全局事件总线。

### HTTP 状态码规范

| 状态码 | 名称 | 业务语义 |
|---|---|---|
| `200` | OK | 请求成功，返回响应数据 |
| `201` | Created | 资源创建成功（如创建连接、签发令牌） |
| `202` | Accepted | 异步任务已受理（如应用启动、作业控制、节点状态采集） |
| `204` | No Content | 操作成功且无返回包体（如删除、续期、修改密码） |
| `400` | Bad Request | 请求参数格式错误、缺失必填项或未通过 Schema 校验 |
| `401` | Unauthorized | 未提供有效鉴权凭证或 Token 已过期 |
| `403` | Forbidden | 权限不足（角色不符、缺少 AdminScope、审计只读）、账号未激活或被封禁 |
| `404` | Not Found | 目标资源不存在或未知动作动词 |
| `409` | Conflict | 状态冲突（如部分批量操作失败、节点重复注册、资源锁定） |
| `410` | Gone | 资源已过期或已失效（如过期的下载审批申请） |
| `413` | Content Too Large | 文件体积超出上传上限 |
| `422` | Unprocessable Entity | 请求语法正确但业务语义非法（如改密新旧密码相同） |
| `428` | Precondition Required | 密码已过期的受限会话访问白名单外端点（`password change required`），须先改密 |
| `429` | Too Many Requests | 请求速率超出限流阈值（IP 级或用户级限流） |
| `500` | Internal Server Error | 服务端内部异常 |
| `502` | Bad Gateway | 无法连接底层执行节点或目标代理后端 |
| `503` | Service Unavailable | 服务处于维护或不可用状态 |

---

## 公开端点（无需认证）

以下端点**不要求 Session**，供桌面客户端、计算节点与浏览器直接访问。完整请求/响应见 [用户 API](./user.md) 对应业务域章节：

| 方法 | 路径 | 用途 | 归属域 |
|---|---|---|---|
| `POST` | `/api/sessions` | 用户登录（创建会话） | [Sessions](./user.md#sessions) |
| `POST` | `/api/nodes/:id/registration` | 计算节点 OTP 自助注册（预共享 Token） | [Nodes](./user.md#nodes) |
| `GET` | `/api/version` | Engine 构建信息、桌面发布是否开启与桌面最低支持版本 | [Version](./user.md#version) |
| `GET` | `/api/desktop/releases/latest` | Desktop 客户端最新版本（自动更新检查） | [Version](./user.md#version) |
| `GET` | `/api/desktop/releases/installer` | 首次安装 Setup.exe 元数据（`fileName` / `sha256` / `size`） | [Version](./user.md#version) |
| `GET` | `/api/desktop/releases/bootstrap-installer` | Windows 在线安装器元数据（`fileName` / `sha256` / `size`） | [Version](./user.md#version) |
| `GET` | `/api/desktop/releases/download/latest` | 下载最新 Desktop 更新包 | [Version](./user.md#version) |
| `GET` | `/api/desktop/releases/download/:filename` | 下载指定 Desktop 安装包 | [Version](./user.md#version) |
| `GET` | `/api/desktop/plugins/:name/latest` | Desktop sidecar 插件最新版本 | [Version](./user.md#version) |
| `GET` | `/api/desktop/plugins/:name/download/latest` | 下载最新插件包 | [Version](./user.md#version) |
| `GET` | `/api/desktop/plugins/:name/download/:filename` | 下载指定插件包 | [Version](./user.md#version) |

> **可选访客面**：当 Settings `platform.website.overviewEnabled` 为 `true` 时，携带 `PublicOverviewToken` 的 `GET /api/overview/cluster/stream` 支持免登录访客只读访问；该开关只作用于网页大屏：关闭时访客 Token 返回 `404 Not Found`（`overview guest access is disabled`），嵌入页 `/overview` 返回 `404 Not Found`；已登录用户不受影响。集群快照 `GET /api/overview/cluster` 与个人概览 `GET /api/overview/me`、`/me/stream` 走常规会话鉴权（访客 Token 返回 `401`，受限会话返回 `428`）；裸 `GET /api/overview` 不提供数据。普通用户访问集群概览还受 Settings `overview.allowUserCluster`（默认 `true`）约束。见 [Overview](./user.md#overview)。

---

## 权限与访问模型

### 1. 角色体系 (Roles)

平台支持两种管理模式，由配置文件（`[admin].enable_three_roles`）决定：

- **单管理员模式**（默认）：
  - 超级管理员 `dceadmin`（`RoleSuperAdmin`）：通吃所有管理端点；
  - 普通用户 `normalUser`（`RoleNormal`）：可被超级管理员授予指定的 **AdminScope** 成为子管理员。
- **三员管理模式**：
  - 系统管理员（`RoleSystemAdmin`）：负责应用、节点、存储、调度等计算资源运维（`/api/admin/system/*`）；
  - 安全管理员（`RoleSecurityAdmin`）：负责用户角色、权限、密码策略、会话与安全审计（`/api/admin/security/*`）；
  - 审计管理员（`RoleAuditAdmin`）：负责审计日志与安全事件查询（`/api/admin/audit/*`）；
  - 普通用户（`RoleNormal`）：无任何管理权限，三员模式下严格禁止设置 AdminScope。

### 2. 管理员作用域 (Admin Scope)

在单管理员模式下，超级管理员可将管理能力细粒度委托给普通用户：

| Scope 常量 | 位权 | 对应的管理领域 | 路由前缀 |
|---|---|---|---|
| `ScopeNodes` | `1 << 0` | 节点管理 | `/api/admin/system/nodes` |
| `ScopeConnections` | `1 << 1` | 连接管理 | `/api/admin/system/connections` |
| `ScopeSchedulers` | `1 << 2` | 调度作业管理 | `/api/admin/system/scheduler` |
| `ScopeApps` | `1 << 3` | 应用管理 | `/api/admin/system/apps` |
| `ScopeLicenses` | `1 << 4` | 许可服务管理 | `/api/admin/system/licenses` |
| `ScopeFiles` | `1 << 5` | 文件审批 | `/api/admin/security/files` |
| `ScopeSettings` | `1 << 6` | 平台设置与告警 | `/api/admin/system/settings`, `/api/admin/system/alerts` |
| `ScopeTickets` | `1 << 7` | 工单管理 | `/api/admin/system/tickets` |
| `ScopeUsers` | `1 << 8` | 用户管理 | `/api/admin/system/users` |
| `ScopeSessions` | `1 << 9` | 在线会话管理 | `/api/admin/security/sessions` |
| `ScopeBans` | `1 << 10` | IP与用户封禁 | `/api/admin/security/bans` |
| `ScopeStorages` | `1 << 11` | 存储管理 | `/api/admin/system/storage` |
| `ScopeStatuses` | `1 << 12` | 系统状态 | `/api/admin/system/status` |
| `ScopeAll` | `-1` | 全量管理权限 | 全部管理端点放行 |

### 3. 密码过期与受限会话

当登录返回 `passwordMustChange: true` 时，该会话进入**受限模式**（TTL 临时缩短为 5 分钟）：
- 仅放行白名单端点：`/api/users/me/password`（改密）、`/api/users/me`（个人信息）、`/api/sessions/current` 与 `/api/sessions`（登出）；
- 访问其他任何端点均拦截并返回 `428 Precondition Required`（`password change required`）。

---

## 领域端点速查矩阵

下表列出所有业务领域及对应接口。点击详细文档链接可直接跳转到完整请求参数与响应定义：

| 业务领域 | 核心端点路径 | 主要功能 | 详细文档直达 |
|---|---|---|:---:|
| **Sessions** | `/api/sessions`<br>`/api/admin/security/sessions` | 用户登录、会话列表、续期、批量登出、强制下线 | [用户](./user.md#sessions) · [管理](./admin.md#在线会话sessions) |
| **Users** | `/api/users/me`<br>`/api/users/id-mappings`<br>`/api/admin/system/users`<br>`/api/admin/security/roles`<br>`/api/admin/security/scopes` | 修改个人密码、UID/GID → 名字解析（文件属主展示）、用户管理、LDAP 同步、重置密码、删除用户、角色与 AdminScope 授权 | [用户](./user.md#users) · [管理](./admin.md#用户管理users) |
| **Connections** | `/api/connections`<br>`/api/admin/system/connections` | 从规格创建实例（VNC/RDP/XRDP；SSH 不建实例）、规格与授权管理、启停、VNC OTP、批量删除、用户侧 VNC 状态刷新 | [用户](./user.md#connections) · [管理](./admin.md#连接管理connections) |
| **Connection Proxy** | `/api/connections/:id/proxy-connection`<br>`/api/connections/ssh?resourceId=`（SSH，仅 Upgrade） | 协议代理数据面 | [用户](./user.md#connection-proxy) |
| **Apps** | `/api/apps`<br>`/api/admin/system/apps` | 应用列表、启动应用（作业创建）、应用模板 CRUD 与可访问用户配置 | [用户](./user.md#apps) · [管理](./admin.md#应用管理apps) |
| **Scheduler** | `/api/scheduler/meta`<br>`/api/scheduler/hosts`<br>`/api/scheduler/queues`<br>`/api/scheduler/jobs`<br>`/api/scheduler/jobs/me`<br>`/api/admin/system/scheduler/*` | 元数据（作业/主机/队列推荐列、`keys`、动作矩阵）；主机与队列原生记录；全集群/当前用户作业列表与控制；管理员可按 `user` 过滤 | [用户](./user.md#scheduler) · [管理](./admin.md#调度管理scheduler) |
| **Licenses** | `/api/licenses`<br>`/api/admin/system/licenses` | FlexLM 许可证服务监控、功能查询、许可证服务配置 CRUD | [用户](./user.md#licenses) · [管理](./admin.md#许可管理licenses) |
| **Files** | `/api/files/locations`<br>`/api/files/downloads`<br>`/api/files/download-requests`<br>`/api/admin/security/files` | 远程目录浏览、流式上传下载、回收站、下载审批申请（表 `download_requests`）与安全管理员审批 | [用户](./user.md#files) · [管理](./admin.md#文件审批files) |
| **Nodes** | `/api/nodes/:id/registration`<br>`/api/admin/system/nodes`<br>`/api/admin/system/nodes/:id/settings` | 计算节点 OTP 自助注册、节点列表、集群证书签发与吊销、节点级 Agent 差异化配置覆盖与热更推送 | [节点](./user.md#nodes) · [管理](./admin.md#节点管理nodes) |
| **Tickets** | `/api/tickets`<br>`/api/admin/system/tickets` | 工单增删改查、看板移动、评论与变更记录、统计；管理员的项目管理、恢复与永久删除 | [用户](./user.md#tickets) · [管理](./admin.md#工单管理tickets) |
| **Settings** | `/api/admin/system/settings/:domain` | 动态系统设置热更新、Schema 元数据自发现、设置域枚举列表（12 个活跃域：platform/auth/connection/file/overview/storage/scheduler/license/agent/alert/desktop/cert；`connection.proxy` 为嵌套协议代理配置） | [管理](./admin.md#平台设置settings) |
| **Alerts** | `/api/admin/system/alerts/history` | 资源告警历史全量记录查询（阈值触发与恢复留痕） | [管理](./admin.md#系统告警历史alerts-history) |
| **Overview** | `/api/overview/me`<br>`/api/overview/me/stream`<br>`/api/overview/cluster`<br>`/api/overview/cluster/stream` | 个人/集群态势快照与 SSE（大屏访客见公开端点说明） | [用户](./user.md#overview) |
| **Realtime (SSE)** | `/api/events` | 客户端全局实时事件总线（作业、工单、节点、会话通知） | [用户](./user.md#realtime-sse) |
| **Version** | `/api/version`<br>`/api/desktop/releases/*`<br>`/api/desktop/plugins/*` | Engine 运行版本与桌面最低支持版本、桌面客户端发布与 sidecar 插件下载 | [用户](./user.md#version) |
| **Storage** | `/api/admin/system/storage` | 共享存储容量快照、历史监控采样趋势与即时探测触发 | [管理](./admin.md#存储管理storage) |
| **System Status** | `/api/admin/system/status` | 系统整体健康与服务运行快照 | [管理](./admin.md#系统状态system-status) |
| **Audit** | `/api/admin/audit`<br>`/api/admin/audit/logs/:name/stream` | 主审计日志与安全事件查询、实时日志增量流式推送 | [管理](./admin.md#审计日志logs) |
| **Bans** | `/api/admin/security/bans`<br>`/api/admin/security/bans/allowlist` | 查看登录失败封禁记录、手动封禁/解封单个 IP/用户或清空封禁、封禁放行名单独立增删查 | [管理](./admin.md#封禁管理bans) |
