---
title: 用户系统
linkTitle: 用户系统
weight: 170
description: 用户、角色、认证后端与同步机制
---

本文档描述当前代码中的用户模型、角色与委托、认证后端以及用户同步机制。

---

## 用户类型

系统中有两类用户，存储在 `internal/model/user.go` 的 `User.Type` 字段中。
JSON 输出是**数字枚举**，不是字符串。

| JSON / DB 值 | 常量               | 说明                           |
|:------------|:-----------------|:-----------------------------|
| `1`         | `UserTypeSystem` | 系统用户：来自本机账号或 LDAP 目录         |
| `2`         | `UserTypeApp`    | 平台内部用户：密码以 bcrypt 哈希存储在 DB 中 |

说明：

- `UserTypeApp` 用户不参与系统/LDAP 同步。
- `UserTypeSystem` 用户在首次成功登录后会自动补齐 DB 记录。

---

## 角色（Role）

角色定义在 `internal/model/role.go`。`User.Role` / `Session.Role` 的 JSON 输出同样是**数字枚举**。

| JSON / DB 值 | 常量                  | 字符串形式           | 说明            |
|:------------|:--------------------|:----------------|:--------------|
| `0`         | `RoleNormal`        | `normal`        | 普通用户          |
| `1`         | `RoleAuditAdmin`    | `auditadmin`    | 审计管理员         |
| `2`         | `RoleSecurityAdmin` | `securityadmin` | 安全管理员         |
| `3`         | `RoleSystemAdmin`   | `systemadmin`   | 系统管理员         |
| `4`         | `RoleSuperAdmin`    | `superadmin`    | 单管理员模式下的超级管理员 |

### 三员管理模式

系统通过配置文件（`config.toml`）的 `[admin].enable_three_roles` 字段切换管理员模式。启用后（`enable_three_roles = true`
），系统进入**三员管理模式**：

- 启动时初始化三个平行管理员角色，各自只能访问对应的管理路由：
    - 审计管理员 `auditadmin`（`RoleAuditAdmin`，读权限）
    - 系统管理员 `systemadmin`（`RoleSystemAdmin`）
    - 安全管理员 `securityadmin`（`RoleSecurityAdmin`）
- **动态启停管控**：引擎启动时会根据模式动态更新底层数据库状态。开启三员时，超级管理员（`RoleSuperAdmin`）账号会被强制置为
  `UserDisabled` (禁用)；若关闭三员，则三个三员管理员账号会被置为禁用，从底层数据层面保证绝对的隔离。
- **密码安全**：无论哪种模式，管理员密码的初始生成逻辑取决于当前的环境状态：
  > [!NOTE]
  > - **生产模式**（`config.VERSION != "dev"`）：密码是 16 位强随机数（通过 `crypto.GenSecret`
      生成）。如果在生产模式下检测到数据库中依然使用了开发期硬编码的弱密码（密码 = 账号名），系统会自动触发安全升级，强制重新随机化密码并打印到日志中，消除安全隐患。
  > - **开发模式**（`config.VERSION == "dev"`）：为了方便本地测试，初始密码会被固定为**账号名本身**（如 `dceadmin` → `dceadmin`）。

  初始密码生成后，账号状态默认置为 `UserPasswordExpired`，强制要求首次登录时修改密码。若账号已存在且密码符合安全要求，不会重新生成密码。启动日志示例：

  ```
  [bootstrap] Created admin user "dceadmin" (role=superadmin) with temporary password: aB3xK9mR2qL7vY1w
  ```
- **三员模式下禁止设置管理员作用域**：`SetScopes` 在传入非零 scopes 时一律失败，避免绕过三员分权体系；传入 `0`
  （清空作用域）是唯一被允许的调用。

#### 强制账号前缀

为防止越权并便于审计，在三员模式下赋予管理员角色时，服务层强制校验账号名前缀（初始账号 `systemadmin`/`securityadmin`/
`auditadmin` 作为例外豁免）：

| 目标角色                       | 账号名前缀            |
|:---------------------------|:-----------------|
| 系统管理员（`RoleSystemAdmin`）   | `systemadmin_`   |
| 安全管理员（`RoleSecurityAdmin`） | `securityadmin_` |
| 审计管理员（`RoleAuditAdmin`）    | `auditadmin_`    |

#### 特权独立控制

不论哪种管理模式，系统初始化时的所有特权账号均被代码强制锁定为 `App User` 类型。它们的认证完全由本地数据库（DB + bcrypt）接管，
**不受 `auth_mode` 配置影响**，避免因外部 LDAP 目录服务异常引发安全风险或管理员失联。

### 权限委托机制（单管理员模式）

在**单管理员模式**（`enable_three_roles = false`）下，系统仅有一个超级管理员 `dceadmin`（`RoleSuperAdmin`
）。为满足日常运维中普通用户需要部分管理能力的场景，引入**权限委托**机制——通过位掩码将特定管理路由组授权给普通用户，使其成为
**子管理员**。

实现位于 `internal/model/admin_scope.go`，每个 scope 对应一个 Admin 路由组：

| 位值        | 常量                 | Admin 路由组 | 路由前缀                            |
|:----------|:-------------------|:----------|:--------------------------------|
| `1 << 0`  | `ScopeNodes`        | System    | `/api/admin/system/nodes`       |
| `1 << 1`  | `ScopeConnections` | System    | `/api/admin/system/connections` |
| `1 << 2`  | `ScopeSchedulers`   | System    | `/api/admin/system/scheduler`   |
| `1 << 3`  | `ScopeApps`        | System    | `/api/admin/system/apps`        |
| `1 << 4`  | `ScopeLicenses`    | System    | `/api/admin/system/licenses`    |
| `1 << 5`  | `ScopeFiles`       | Security  | `/api/admin/security/files`     |
| `1 << 6`  | `ScopeSettings`     | System    | `/api/admin/system/settings`    |
| `1 << 7`  | `ScopeTickets`     | System    | `/api/admin/system/tickets`     |
| `1 << 8`  | `ScopeUsers`       | System    | `/api/admin/system/users`       |
| `1 << 9`  | `ScopeSessions`    | Security  | `/api/admin/security/sessions`  |
| `1 << 10` | `ScopeBans`        | Security  | `/api/admin/security/bans`      |
| `1 << 11` | `ScopeStorages`    | System    | `/api/admin/system/storage`     |
| `1 << 12` | `ScopeStatuses`    | System    | `/api/admin/system/status`      |
| `-1`（全 1） | `ScopeAll`         | 全部        | 所有 Admin 路由组放行                  |

#### 鉴权流程

每个 Admin 路由组都有对应的 `RequireScope(scope)` 中间件，在 Admin 角色校验通过后按位检查：

```
请求 → Admin 路由组中间件（校验 Role）
     → 子路由中间件 RequireScope(ScopeXxx)
       → AdminScopes.Has(ScopeXxx) ?
         ✅ 通过
         ❌ 403 Forbidden
```

`ScopeAll` 用户跳过所有 scope 检查。

#### 行为

| AdminScopes 值    | 行为                          |
|:-----------------|:----------------------------|
| `0`（无委托）         | 前端不展现管理入口，所有 Admin 路由组 403  |
| 局部掩码组合           | 前端按位渲染对应菜单，后端 scope 中间件按位检查 |
| `-1`（`ScopeAll`） | 全量子管理员，所有 Admin 路由组放行       |

#### 约束条件

- 委托只能授予 `RoleNormal` 用户
- 委托保存在 `User.AdminScopes` 中，并在登录时复制到 Session
- 更新委托后会删除该用户现有 Session，重新登录后才会生效
- **仅单管理员模式**下允许委托；三员模式下严格禁止（非零 `SetScopes` 调用返回错误，仅允许传 `0` 清空）

---

## 账户状态

`User.Status` 当前只有 5 个状态：

| 状态码 | 常量                    | 名称                |
|:----|:----------------------|:------------------|
| `1` | `UserActive`          | `Active`          |
| `2` | `UserDisabled`        | `Disabled`        |
| `3` | `UserPasswordExpired` | `PasswordExpired` |
| `4` | `UserPasswordNotSet`  | `PasswordNotSet`  |
| `5` | `UserLocked`          | `Locked`          |

补充说明：

- 安全管理员更新用户状态时，合法值就是 `1..5`
- 状态变为非 `Active` 时，现有 Session 会被删除
- 用户同步任务会在 system/LDAP 用户消失时把状态改为 `Locked`，重新出现时恢复为 `Active`

---

## 用户访问开关

除角色外，用户还有两个独立开关：

| 字段                | 默认值    | 说明                |
|:------------------|:-------|:------------------|
| `download_access` | `true` | 是否允许下载文件          |
| `terminal_access` | `true` | 是否允许终端访问          |
| `upload_access`   | `true` | 是否允许上传文件          |
| `max_connections` | `null` | 最大连接数（null 表示无限制） |

这四个默认值由 `internal/model/user.go` 的 GORM 默认值定义。

---

## 认证后端

`internal/config/config.go` 中 `user.auth_mode` 只支持两种值：

| 配置值     | 说明              |
|:--------|:----------------|
| `local` | 使用本机用户目录与本地认证能力 |
| `ldap`  | 使用 LDAP 目录服务    |

补充说明：

- 默认配置值来自 `internal/config/default.go`，当前默认是 `ldap`
- `App` 用户始终走 DB + bcrypt，与 `auth_mode` 无关

---

## 登录认证边界

登录入口是 `POST /api/sessions`，由 `session.AuthManager` 编排认证：

- App 用户：直接校验 DB 中密码哈希
- System / LDAP 用户：委托 `user/provider` 校验密码
- 用户不存在时只累计 IP 维度失败次数，避免用户枚举
- `root` 登录始终被拒绝
- 账户未激活时返回 `ErrUserNotActive`，Portal API 对应 HTTP `403`
- 密码过期时登录仍可成功，但会返回 `passwordMustChange=true`，会话 TTL 缩短为 5 分钟
- **密码过期判定**：
  - App 用户：DB `status = passwordExpired`（首次创建/管理员重置后）
  - LDAP 用户：读取 `krbPasswordExpiration` 属性（FreeIPA）或 `pwdExpirationTime`（OpenLDAP ppolicy），已过当前时间即判定过期；属性缺失则不判定
- **受限会话**：`passwordMustChange=true` 的会话只能访问 `PATCH /api/users/me/password`、`GET /api/users/me`、登出端点，其他请求一律 `403 password change required`（由 `sessionAuth` 中间件强制）
- 登录响应与 `GET /api/users/me` 返回 `passwordExpiresAt`（LDAP 用户的密码过期时间，App 用户无此字段）

---

## 用户域与会话域边界

### `internal/server/service/user`

负责：

- 用户查询
- 用户组查询
- 密码修改 / 重置
- 用户角色与委托更新
- 用户同步
- UID/GID → 名字映射与缓存

### `internal/server/service/session`

负责：

- 登录认证编排
- Session 创建、续期、删除、查询
- 登录失败与封禁流程接入

因此：

- 登录入口属于 Session 域，不属于 User 域
- 登录封禁属于 Security 域
- 用户资料管理与密码管理属于 User 域

---

## 用户同步（User Sync）

用户同步逻辑位于 `internal/server/service/user/sync.go`。

### 行为

同步对象仅限 **System 用户**：

| 场景                                  | 动作          |
|:------------------------------------|:------------|
| DB 中存在、但系统/LDAP 中已不存在，且状态为 `Active` | 改为 `Locked` |
| DB 中状态为 `Locked`，且系统/LDAP 中重新出现     | 改回 `Active` |

`App` 用户不会被同步任务修改。

### 调度方式

- 服务启动后会启动后台调度器
- 每轮执行前读取 `auth.userSyncInterval`
- 当值 `<= 0` 时，同步调度器停止，表示禁用同步
- 多 Server 场景通过 lease `user_sync` 保证同一时刻只有一个节点执行

### 配置入口

用户同步间隔由运行时设置域 `auth` 中的 `userSyncInterval` 配置：

```http
PUT /api/admin/system/settings/auth
Content-Type: application/json

{
  "userSyncInterval": 86400
}
```

约束：

- `0`：禁用同步
- 最小值：300 秒
- 默认值：86400 秒
- 最大值：604800 秒

---

## UID/GID 名字映射

文件属主以数字 ID 存储，界面展示需要名字。解析入口为 `POST /api/users/id-mappings`（契约与错误码见 [API · user](../api/user.md)）；管理员强制刷新为 `POST /api/admin/system/users/id-mappings/refresh`（见 [API · admin](../api/admin.md)）。实现位于 `internal/server/service/user/id_mapping_cache.go`，底层缓存为 `TTLCache`（见 [缓存体系](cache.md)）。

### 请求边界

| 约束 | 值 | 说明 |
|:---|:---|:---|
| 去重后 `uids`+`gids` 合计 | ≤ 5000 | 超出 → `400` |
| 去重前单数组长度 | ≤ 8192 | 防止海量重复项在去重前耗资源 |
| 空请求（去重后无 ID） | `200` 空结果 | **不**检查平台是否具备身份源（分页末页可能为空） |
| 无本机 passwd/group 且未启用 LDAP | `503` | 仅在确有 ID 需要解析时触发 |

### 解析策略：本机打底 + LDAP 覆盖

| 步骤 | 动作 |
|:---|:---|
| 1 | 读本机 passwd/group → `localMap`（含系统账号，不划 UID 段） |
| 2 | `auth_mode=ldap` 时再查 LDAP → `ldapMap`（**本机已命中的 ID 也要查**，否则无法保证覆盖语义） |
| 3 | 同 ID 以 **LDAP 名为准**覆盖本机；仅本机有的保留本机名 |

`notFound`（确认不存在）的前提是**所有存在的身份源都成功且都没有该 ID**；任一源报错则不得断言不存在。

LDAP 点查：单次 Search 分块 25 个 ID、墙钟总预算 15s（含建连/Bind/全部 Search）；建连单次超时 2s、最多 2 次尝试。目录变慢时在预算内 fail-fast，不拖到 HTTP 请求超时。

### 缓存分级与失败回退

| 类别 | TTL | 配置 |
|:---|:---|:---|
| 已确认存在 | 86400s (24h) | `auth.idMappingFoundCacheTTL`（min 600） |
| 已确认不存在 | 600s (10min) | `auth.idMappingNotFoundCacheTTL`（min 300，且 ≤ found） |
| 失败回退正结果 | 300s (5min) | 代码常量 `IDMappingDegradedCacheTTL`，不进设置 |

> 本表解释**三档 TTL 的语义**（为什么需要它们）。缓存的权威清单（原语/上限/失效方式/全部 TTL）
> 见 [缓存体系 · 缓存实例清单](cache.md#缓存实例清单唯一事实源)，改动 TTL 时以那里为准并同步本节。

失败回退链：**stale 正缓存 → 本机命中 → 均无则整请求 503**。失败路径不写负缓存，且 stale 负缓存绝不充当 `notFound`。

用户/组各一个命名空间，单命名空间上限 **10 万**条（含负缓存）；超限时写入路径**采样淘汰**（样本内最早过期者，过期项优先）。`Invalidate`/`Clear` 递增代次，在途回填经 `SetIfGeneration` 丢弃，避免 refresh/sync 后旧名字写回。

### 失效时机

| 时机 | 动作 |
|:---|:---|
| 管理员 `POST /api/admin/system/users/id-mappings/refresh` | 本机清空（代次 +1，丢弃在途回填）并广播 `cache_versions` 主题 `id_mappings`，其它节点 ≤15s 同步清空；审计动作名 `ID_MAPPING_CACHE_REFRESH` |
| `idMappingFoundCacheTTL` / `idMappingNotFoundCacheTTL` 变更 | 各节点自行检测并清空本机（随 `settings` 主题传播，**不**广播） |
| 用户同步成功 | 对本轮 `ListUsers` 中可解析的 uid/gid 做**本机**单键失效（不广播；单键失效同样递增代次，打断在途批量回填） |

---

## LDAP 配置

当 `user.auth_mode=ldap` 时，LDAP 相关配置位于 `user.ldap`：

| TOML Key                                         | 说明                                     |
|:-------------------------------------------------|:---------------------------------------|
| `user.ldap.server`                               | LDAP URL，必须以 `ldap://` 或 `ldaps://` 开头 |
| `user.ldap.user_base_dn`                         | 用户搜索基准 DN                              |
| `user.ldap.bind_dn`                              | 服务账户 DN                                |
| `user.ldap.bind_password` / `bind_password_file` | 服务账户密码                                 |
| `user.ldap.id_attribute`                         | 用户标识属性                                 |
| `user.ldap.user_filter`                          | 用户过滤器                                  |
| `user.ldap.group_base_dn`                        | 组搜索基准 DN                               |
| `user.ldap.group_filter`                         | 组过滤器                                   |
| `user.ldap.password_modify`                      | 是否启用 LDAP Password Modify              |
| `user.ldap.tls_ca_path`                          | 自定义 CA 路径                              |

> **TLS 说明**：
>
> - **不提供 `tls_insecure_skip_verify` 配置项**（刻意不暴露）。`ldaps://` 连接一律严格校验服务器证书（`InsecureSkipVerify=false` 硬编码，TLS ≥ 1.2），证书不受系统信任链认可时使用 `tls_ca_path` 显式信任。
> - **密码修改必须走 TLS**：目录服务器（FreeIPA/389-ds、AD、OpenLDAP 同此标准）对 RFC 3062 Password Modify 扩展操作强制要求安全连接。配置 `ldap://`（明文）时，改密请求会被服务器以 `Confidentiality Required (13)` 拒绝——这是服务器策略，不是客户端库的限制。**改密场景必须配置 `ldaps://`**（或等效 TLS）。

> **已知缺口**：`ldapWebAdminUrl` 目前只是 auth 域的一个配置项（随管理端 settings
> schema / 域 GET 暴露，管理员可填写，校验限定为 http(s) URL），但 Portal **没有**
> 按 `auth_mode` 条件向普通前端暴露它的逻辑，也没有任何消费方读取该字段——即“从门户
> 跳转到目录管理界面”这个入口尚未实现。

---

## 数据模型速览

```go
type User struct {
	ID              uint
	Name            string
	Password        string
	Status          UserStatusCode
	Type            UserType
	LastOnlineIP    string
	LastOnlineTime  time.Time
	DownloadAccess  bool
	TerminalAccess  bool
	UploadAccess    bool
	MaxConnections  uint
	Role            Role
	AdminScopes AdminScopes
	Preference      UserPreference // JSON 序列化存储于 preference 列（如 disabledModules）
	Apps            []App
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
```
