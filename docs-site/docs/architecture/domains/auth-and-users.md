---
title: 用户认证、三员分权与会话体系
linkTitle: 认证与用户
weight: 10
description: 用户模型、三员分权与权限委托机制、多认证后端同步、服务端 Session 与物化上下文
aliases:
  - /docs/development/user-system
  - /docs/development/session
---

Design Cloud Engine（DCE）提供了一套企业级、严格合规的身份认证与访问控制体系。支持标准 Linux/LDAP 账号同步、国密与等保要求的三员分权治理、服务端持久化 Session 以及面向前端渲染的高性能物化上下文。

---

## 1. 用户类型模型

系统将用户分为两类，定义于 `internal/model/user.go` 的 `User.Type` 字段中（JSON 序列化为数字枚举）：

| 枚举值 | 常量名 | 业务语义 | 认证与存储方式 |
|:---|:---|:---|:---|
| `1` | `UserTypeSystem` | **系统用户**：研发、工程师等真实终端用户 | 映射自 Linux 本机账号或企业 LDAP 目录；首次登录成功自动在 DB 中补齐元数据记录 |
| `2` | `UserTypeApp` | **平台用户**：管理员、系统服务内置特权账号 | 凭证由平台数据库本地管理，密码使用强加盐 `bcrypt` 哈希存储，不参与 LDAP 同步 |

---

## 2. 角色体系与管理模式

角色枚举定义于 `internal/model/role.go`，在数据库与 API 交互中均采用**数字枚举**传输：

| 枚举值 | 常量 | 文本标识 | 职责定位 |
|:---|:---|:---|:---|
| `0` | `RoleNormal` | `normalUser` | 普通研发人员，仅可访问自身授权的应用、文件与桌面 |
| `1` | `RoleAuditAdmin` | `auditAdmin` | **审计管理员**：查阅系统审计、安全审计日志与报表（只读） |
| `2` | `RoleSecurityAdmin` | `securityAdmin` | **安全管理员**：Session 管控、密码策略、IP 黑名单、文件安全策略 |
| `3` | `RoleSystemAdmin` | `systemAdmin` | **系统管理员**：节点注册、调度器对接、应用模板、系统设置与监控 |
| `4` | `RoleSuperAdmin` | `superAdmin` | **超级管理员**：单管理员模式下的全权管理账号（默认 `dceadmin`） |

### 2.1 三员管理模式 (Three-Role Mode)

通过 `config.toml` 中 `[admin].enable_three_roles = true` 开启。

- **内置名占坑**：`dceadmin`、`auditadmin`、`systemadmin`、`securityadmin` 四个用户名在 `internal/model/builtin_admin.go` 中常量定义，首次引导即写入 `type=app` 用户行（无初始口令），与当前是否启用三员无关。
- **动态排他管控**：开启三员时，超级管理员（`RoleSuperAdmin`）账号在启动时会被强制置为禁用（`UserDisabled`）；反之若关闭三员，三个三员特权账号会被置为禁用，从底层存储杜绝越权。目录/LDAP 首登不会创建与内置名相同的 `type=system` 用户。
- **强制账号名前缀**：为防止越权与方便审计，三员模式下新指派的管理员必须遵循严格命名规范：
  - 系统管理员：`systemadmin_*`
  - 安全管理员：`securityadmin_*`
  - 审计管理员：`auditadmin_*`
- **密码与引导**：四个内置账号创建时**不写入口令**（`UserPasswordExpired`），须使用 `dc-engine user set-password --username <name>` 后再登录 Portal；与 `[Admin]` 说明一致，**不**在启动时向控制台打印随机密码。

### 2.2 权限委托机制 (单管理员模式 AdminScopes)

在单管理员模式下（`enable_three_roles = false`），超级管理员可通过位掩码向特定普通用户授予特定模块的管理权限，生成**子管理员**。

实现位于 `internal/model/admin_scope.go`：

| 位值 | 常量 | schema `key` | 对应路由域 | 赋权能力 |
|:---|:---|:---|:---|:---|
| `1 << 0` | `ScopeNodes` | `node` | `/api/admin/system/nodes`、`/api/admin/system/settings/{agent,cert}` | 节点接入、状态查看、启停及 Agent/证书全局配置 |
| `1 << 1` | `ScopeConnections` | `connection` | `/api/admin/system/connections`、`/api/admin/system/settings/connection` | 远程桌面连接会话与连接策略管理 |
| `1 << 2` | `ScopeSchedulers` | `scheduler` | `/api/admin/system/scheduler`、`/api/admin/system/settings/scheduler` | 外部作业调度器管理、作业控制与调度配置 |
| `1 << 3` | `ScopeApps` | `app` | `/api/admin/system/apps` | 应用中心模板发布与配额 |
| `1 << 4` | `ScopeLicenses` | `license` | `/api/admin/system/licenses`、`/api/admin/system/settings/license` | FlexLM 许可证服务、特性与许可配置管理 |
| `1 << 5` | `ScopeFiles` | `file` | `/api/admin/security/files`、`/api/admin/system/settings/file` | 文件下载审核与全局文件策略配置 |
| `1 << 6` | `ScopeSettings` | `settings` | `/api/admin/system/settings`、`/api/admin/system/alerts` | 全量平台运行时设置（全 12 域）与告警历史 |
| `1 << 7` | `ScopeTickets` | `ticket` | `/api/admin/system/tickets` | 运维工单全流程管理 |
| `1 << 8` | `ScopeUsers` | `user` | `/api/admin/system/users`、`/api/admin/system/groups`、`/api/admin/system/id-mappings`、`/api/admin/system/settings/auth` | 门户账号、目录组、ID 映射缓存与认证策略（不含角色/Scope 授权） |
| `1 << 9` | `ScopeSessions` | `session` | `/api/admin/security/sessions`、`/api/admin/system/settings/auth` | 在线会话查看、强制下线及会话策略配置 |
| `1 << 10` | `ScopeBans` | `ban` | `/api/admin/security/bans`、`/api/admin/system/settings/auth` | IP/用户封禁、白名单及防爆破策略配置 |
| `1 << 11` | `ScopeStorages` | `storage` | `/api/admin/system/storage`、`/api/admin/system/settings/storage` | 共享存储状态查看与探测配置 |
| `1 << 12` | `ScopeStatuses` | `status` | `/api/admin/system/status` | 系统运行状态查看 |
| `1 << 13` | `ScopeDesktop` | `desktop` | `/api/admin/system/desktop`、`/api/admin/system/settings/desktop` | 桌面客户端、插件发布管理与桌面发布配置 |
| `-1` | `ScopeAll` | `systemadmin` | 全部管理端点 | 全量授权（不展开为粒化 scope） |

**目标保护**：管理员目标校验（`checkAdminTargetProtection`）统一禁止操作自己的账号、禁止修改/删除 `dceadmin`；单管理员模式下子管理员不能管理其他被授予 AdminScope 的账号（三员模式下 SystemAdmin 仍可管理其他管理员账号，仅 SuperAdmin 不可变）。审计路由在所有模式下只读。

> [!CAUTION]
> 在三员管理模式下，调用 `SetScopes` 赋予非零权限会被直接拒绝，禁止绕过三员分权隔离原则。

---

## 3. 多源认证后端与用户同步

认证逻辑由 `internal/server/service/user/user.go` 与 `internal/server/service/user/provider/` 驱动，支持两级 fallback 链：

1. **特权账号本地认证**：所有管理员账号（App User）强制走 DB + bcrypt 认证，完全绕过外部 LDAP/PAM，防止因外部网络故障导致管理员无法登录系统。
2. **普通系统用户认证**：
   - 当 `auth_mode = "ldap"` 时：优先走 LDAP 协议绑定验证；若 LDAP 无法连接且配置了降级策略，可回退至本地 Linux PAM。
   - 当 `auth_mode = "pam"` 时：直接通过 `dc-exec` 委托以 root 身份调用 PAM 验证操作系统密码。
3. **用户首次登录同步 (JIT Provisioning)**：系统用户首次通过 LDAP/PAM 验证成功后，服务层自动在数据库中为其物化一条 `User` 记录，并拉取其所属的 Linux GID/用户组，写入初始默认偏好设置。

---

## 4. 服务端 Session 模型

系统不采用无状态 JWT，而是采用**服务端持久化 Session**。客户端通过 HTTP Header 携带 opaque token 访问：
`Authorization: Bearer <opaque-token>`。

```go
type Session struct {
    ID                 uint
    UserID             uint
    Username           string
    Token              string      // 32 字节高熵随机十六进制字串
    IP                 string
    Role               Role        // 缓存角色枚举，避免逐请求联表
    UserType           UserType
    AdminScopes        AdminScopes // 缓存委派权限位
    CreatedAt          time.Time
    UpdatedAt          time.Time
    ExpiresAt          time.Time
    PasswordMustChange bool        // 标记是否为强制改密受限会话
}
```

### 4.1 会话配额与生命周期

- **多端与单点登录配额**：通过 `auth.maxSessionsPerUser` 控制（默认 `1`）。大于 0 时，若新登录超配，自动销毁最旧会话以腾出配额（实现单端登录顶号）；设置为 0 则不限制。
- **内存二级缓存加速**：为了避免每个 API 请求都查库，Server 在内存中维护 `Token → *Session` 的高并发缓存，并通过数据库全局缓存版本号与写时失效机制保持多节点一致。
- **动态限流器**：每个活跃 Session 在内存中绑定一个独立的令牌桶限流器（默认 50 req/s，突发 burst 150），拦截异常脚本刷接口。

---

## 5. Session 物化上下文 (Materialized Context)

为减轻前端 SPA 页面加载时的级联请求与轮询开销，在登录 `POST /api/sessions` 及查询 `GET /api/sessions/current` 时，后端会随 Session 一同下发已完成预计算的只读物化上下文：

```json
{
  "code": 0,
  "data": {
    "id": 12,
    "username": "zhangsan",
    "role": "normalUser",
    "ui": {
      "adminConsole": {
        "enabled": false,
        "mode": "allow",
        "modules": []
      },
      "userPortal": {
        "enabled": true,
        "mode": "deny",
        "modules": ["billing", "advanced_hpc"]
      }
    },
    "fileLocations": [
      { "key": "home", "path": "/home/zhangsan", "readOnly": false },
      { "key": "shared", "path": "/data/shared", "readOnly": true }
    ]
  }
}
```

- **统一角色 Badge 标签**：直接提供 `superAdmin` / `systemAdmin` / `securityAdmin` / `auditAdmin` / `normalUser` 小驼峰字符串，前端直接渲染对应的指示徽章。
- **门户开关**：`ui.adminConsole.enabled` 在角色 ≥ 审计管理员或持有任意 AdminScope 时为真；`ui.userPortal.enabled` 仅对普通用户（含 AdminScope 子管理员）为真。**`superAdmin`（`dceadmin`）与三员角色同为仅管理台**（`userPortal.enabled=false`），客户端应直接进入管理区，不提供用户工作台切换。
- **模块权限预计算裁切**：将平台设置 `settings.platform.disabledModules` 中值为 `true` 的工作台模块 ID，与用户个人偏好 `user.preference.disabledModules` 合并后，通过 `ui.userPortal.modules` 下发为 deny 列表。
- **文件存储位置预裁切**：根据用户身份和系统配置，自动计算出允许该用户访问的根目录位置列表，并标记只读状态。

---

## 6. 核心接口与管理端点

| 方法 | 路径 | 鉴权要求 | 描述 |
|---|---|---|---|
| `POST` | `/api/sessions` | 匿名 | 用户登录并建立会话 |
| `GET` | `/api/sessions/current` | 已登录 | 获取当前会话信息及物化上下文 |
| `POST` | `/api/sessions/current/renew` | 已登录 | 刷新会话过期时间（Token 保持不变） |
| `DELETE`| `/api/sessions/current` | 已登录 | 退出当前登录 |
| `GET` | `/api/sessions` | 已登录 | 获取当前账号在所有设备上的活跃会话 |
| `GET` | `/api/admin/security/sessions` | 安全管理员 | 查看全集群所有用户的有效活跃会话 |
| `DELETE`| `/api/admin/security/sessions/:id` | 安全管理员 | 强制下线指定用户的特定会话 |
