---
title: Session 设计
linkTitle: 会话设计
weight: 140
description: 会话生命周期、校验与续期机制
---

## 概述

Design Cloud Engine 使用服务端持久化 `session`，而不是 JWT。
客户端持有一段随机 opaque token，访问 API 时通过 Bearer Header 携带：

- `Authorization: Bearer <token>`

服务端在内存中维护 `token → Session` 缓存以消除逐请求查库（带用户名反向索引）；缓存一致性通过
多节点共享的 15s 轮询的缓存版本号 + 写操作时的同步本地失效保证。

## 数据模型

当前 `internal/model/session.go` 中的 `Session` 结构如下：

```go
type Session struct {
	ID              uint
	UserID          uint
	Username        string
	Token           string
	IP              string
	Role            Role
	UserType        UserType
	AdminScopes     AdminScopes
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       time.Time
	PasswordMustChange bool  // 持久化字段：密码过期受限会话标记
}
```

要点：

- `Role`、`UserType`、`AdminScopes` 都直接保存在 Session 中，供中间件快速鉴权使用。
- 不存在 `DeviceName` 字段。
- 运行时每个会话还会挂载一个瞬时（不落库）令牌桶限流器：默认 50 req/s、burst 150。
- `UpdatedAt` 当前只会在创建和续期时更新，不会在每次请求中作为心跳刷新。

## Session 物化上下文 (Session Context Materialization)

为了降低前端组件轮询与高频文件操作时的 DB 检索开销，系统在 Session 状态接口（登录 `POST /api/sessions` 及查询
`GET /api/sessions/current`）中集成了**只读物化上下文**：

- **文件 Locations 自动裁切**：后端结合全局 `file` 设置中的目录配置（包含各可用位置及其 `builtIn`/`readOnly` 属性；extra locations 不设内建只读假定，读写由系统策略与操作系统底层权限共同裁决），在会话生成/查询时提前完成物化计算；
- **身份与角色 Tag 统一**：Session 响应顶层直接输出 `role` 小驼峰字符串 Tag（`superAdmin`, `systemAdmin`, `securityAdmin`,
  `auditAdmin`, `normalUser`），方便前端直接渲染对应 Badge 徽章与角色指示；
- **UI 路由与模块模式物化**：在 Session 响应中通过 `ui` 节点同时提供管理控制台首屏路由引导与模块白名单（
  `ui.adminConsole: { enabled: true, isWhitelist: true, modules: [...] }`），以及用户工作台配置与禁用模块（
  `ui.userPortal: { enabled: true, isWhitelist: false, modules: [...] }`）。前端可使用单句算法
  `config.isWhitelist ? modules.includes(k) : !modules.includes(k)` 完成路由跳转与侧边栏裁切。

## 用户偏好设置 (User Preference)

`User` 模型包含一个通用的 JSON 偏好设置字段 `Preference`（Go 类型 `UserPreference`，存储为 DB 字段 `preference`，使用
`serializer:json`）：

```go
type UserPreference struct {
    DisabledModules []string `json:"disabledModules,omitempty"`
}
```

### UI 模块禁用计算逻辑：

1. **全局平台层配置**：系统管理员通过 `PUT /api/admin/system/settings/platform` 配置全局禁用的工作台模块列表（存储在
   `platform` 域的 `disabledModules` 中）；
2. **个人偏好层配置**：用户可通过个人设置更新其账号的 `preference.disabledModules`；
3. **物化合成输出**：Session 响应中的 `ui.userPortal.modules` 为 **全局禁用模块列表** 与 **个人禁用模块列表** 的并集（
   `Union`）。前端组件统一根据 `isWhitelist: false` 与 `modules` 列表判断显隐。

## 生命周期与配额

数据模型支持一用户多 Session，但运行时还受 `auth.maxLoginSessionsPerUser` 限制：

- 默认值为 `1`
- 大于 `0` 时，登录前会检查当前活跃 Session 数量
- 达到上限时会删除最旧 Session，为新登录腾出名额
- 设为 `0` 时表示不限制数量

## 接口

- `POST /api/sessions`：登录并创建 Session
- `GET /api/sessions`：查看当前用户自己的 Session 列表
- `POST /api/sessions/batch-delete`：批量删除当前用户名下的 Session（body `{"ids":[...]}`）
- `POST /api/sessions/current/renew`：续期当前 Session，token 不变
- `DELETE /api/sessions/current`：注销当前 Session
- `DELETE /api/sessions/:id`：删除当前用户名下指定 Session
- `DELETE /api/sessions`：删除当前用户全部 Session
- `GET /api/admin/security/sessions`：安全管理员查看全部有效 Session
- `POST /api/admin/security/sessions/batch-delete`：安全管理员按 ID 列表批量强制下线
- `DELETE /api/admin/security/sessions/:id`：安全管理员按 Session ID 强制下线
- `DELETE /api/admin/security/sessions?username=:username`：安全管理员按用户名删除全部 Session
- `GET /api/admin/security/bans`：查看登录封禁记录
- `DELETE /api/admin/security/bans/:key`：解除单条封禁
- `DELETE /api/admin/security/bans`：清空全部封禁记录

> 续期采用**滑动窗口**语义：当剩余 TTL 大于总 TTL 的一半时，renew 不产生任何数据库写入；跨过多半阈值后续期才真正延长。
> 同时每个会话内置限流保护（默认 50 req/s，突发上限 150），超限请求会被限流中间件拒绝。

## 登录流程

`session.AuthManager` 统一编排登录认证，区分两类用户：

- **App 用户**：直接校验 DB 中的 bcrypt 哈希
- **System / LDAP 用户**：委托 `internal/server/service/user/provider` 做本地或 LDAP 密码验证

关键流程：

1. 先检查 IP / 用户名是否已被封禁
2. 明确拒绝 `root` 登录
3. 优先读取 DB 中的用户记录，识别 App 用户与已落库的 System 用户状态
4. 认证成功后调用 `SyncUserLogin` 保证用户记录存在并更新最后登录 IP / 时间
5. 创建 Session，并把 `Role` / `UserType` / `AdminScopes` 写入 Session

## 登录结果语义

`POST /api/sessions` 的错误与成功语义如下：

- `201 Created`
    - 用户名密码正确
    - 若密码已过期，不报错，而是返回 `passwordMustChange=true`
    - 此时会话 TTL 会被缩短为 5 分钟
- `401 Unauthorized`
    - 用户不存在
    - 密码错误
- `403 Forbidden`
    - 账户未激活（`ErrUserNotActive`）
    - 已被封禁
    - `root` 登录被拒绝
    - 当前管理员模式不允许该角色登录
- `500 Internal Server Error`
    - 认证成功后，在用户同步、Session 持久化等后续阶段失败

特别地：**未激活账号返回的是 `403`，不是 `401`。**

## 续期与登出

- `POST /api/sessions/current/renew`
    - 只更新当前 Session 的 `expiresAt` 与 `updatedAt`
    - 若当前用户状态为 `PasswordExpired`，返回 `403 password change required`
- `DELETE /api/sessions/current`
    - 删除当前 Session
- `DELETE /api/sessions/:id`
    - 只能删除属于当前用户自己的 Session
- `DELETE /api/sessions`
    - 按当前用户名删除全部 Session

## 认证与封禁边界

- 登录失败计数、封禁和解封由 `security` 服务负责
- 登录认证失败时，`AuthManager` 会同步记录失败次数
- 普通 API 携带伪造或无效 Session Token 时，鉴权中间件会记录 IP 维度失败计数
- 完全匿名、未携带 token 的请求不会触发这类登录失败计数

## 配置入口

Session 相关设置通过认证设置域 (`auth`) 叠加更新（未携带字段保持原值）：

```http
PUT /api/admin/system/settings/auth
Content-Type: application/json

{
  "sessionTTL": 86400,
  "sessionCleanupInterval": 600,
  "maxLoginSessionsPerUser": 1,
  "maxUserLoginAttempts": 5,
  "userBanDuration": 300,
  "maxIPLoginAttempts": 20,
  "ipBanDuration": 86400
}
```

其中：

- `maxLoginSessionsPerUser`：每用户活跃 Session 上限（默认 `1`，设为 `0` 不限制）
- `sessionCleanupInterval`：过期 Session 后台清理间隔（默认 `600` 秒 / 10 分钟）

> [!NOTE]
> 关于 `auth` 域的完整字段约束、校验规则与自省定义，请参阅 **[系统设置(Settings)架构](./settings.md)** 或调用 `GET /api/settings/auth/schema` 动态获取。
