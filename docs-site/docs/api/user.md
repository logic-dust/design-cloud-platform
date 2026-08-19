---
title: Portal API · 用户 API
linkTitle: 用户 API
weight: 901
description: 登录用户可用的 Portal API 端点一览（会话/用户/连接/文件/调度等）
---

## 文档导航

- 首页（总体约定、权限模型与公开端点）：[Portal API 总览](./_index.md)
- 管理 API（System/Security/Audit Admin）：[Portal API · 管理 API](./admin.md)

> 本文件面向**登录用户**与公开 API 调用者（桌面客户端、前端开发者），包含非 `/api/admin/*` 端点的完整定义。
> 其中带"鉴权：否"标注的**公开端点**（登录、版本、节点注册等）在首页[公开端点](./_index.md#公开端点无需认证)集中汇总。
> 鉴权要求、请求/响应格式与状态码语义见首页[基本约定](./_index.md#基本约定)。

## Sessions

### 接口列表

| 方法       | 路径                                    | 用途            | 鉴权                                |
|---|---|---|---|
| `POST`   | `/api/sessions`                       | 用户登录          | 否                                 |
| `GET`    | `/api/sessions`                       | 当前用户会话列表      | 是（本用户）                            |
| `DELETE` | `/api/sessions`                       | 登出所有设备        | 是（本用户）                            |
| `DELETE` | `/api/sessions/:id`                   | 删除指定设备会话      | 是（本用户）                            |
| `POST`   | `/api/sessions/batch-delete`          | 批量删除自己的会话     | 是（本用户）                            |
| `GET`    | `/api/sessions/current`               | 当前会话详情与挂载点上下文 | 是（本用户）                            |
| `DELETE` | `/api/sessions/current`               | 登出当前设备        | 是（本用户）                            |
| `POST`   | `/api/sessions/current/renew` | 续期当前会话        | 是（本用户）                            |

### POST /api/sessions

- **描述**: 用户登录，创建会话
- **鉴权**: 否
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明  |
|------------|------|--------|:--:|:---:|-----|
| `username` | Body | string | 是  |  -  | 用户名 |
| `password` | Body | string | 是  |  -  | 密码  |

- **响应说明**:
    - 状态码: `201 Created`
    - 响应示例:
      ```json
      {
        "data": {
          "token": "...",
          "role": "normalUser",
          "expiresAt": "2026-07-17T00:00:00Z",
          "passwordMustChange": false,
          "passwordExpiresAt": "2026-08-02T12:54:09Z",
          "file": {
            "enabled": true,
            "locations": [
              { "name": "home", "builtIn": true, "readOnly": false }
            ],
            "downloadEnabled": true,
            "uploadEnabled": true,
            "uploadMaxSizeBytes": 8589934592
          },
          "ui": {
            "adminConsole": {
              "enabled": false,
              "isWhitelist": true,
              "modules": []
            },
            "userPortal": {
              "enabled": true,
              "isWhitelist": false,
              "modules": []
            }
          }
        }
      }
      ```
- **受限会话说明**: 当登录响应中 `passwordMustChange` 为 `true`（密码已过期）时，该会话进入**受限模式**：仅允许访问白名单端点
  `/api/users/me/password`、`/api/users/me`、`/api/sessions/current`、`/api/sessions`，其余端点一律返回 `403 Forbidden`
  （message: `password change required`）。改密成功后恢复正常访问。


### GET /api/sessions

- **描述**: 获取当前用户的所有活跃会话列表
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/sessions

- **描述**: 登出所有设备（删除当前用户全部会话）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`


### DELETE /api/sessions/:id

- **描述**: 删除指定设备会话（仅限自己的会话）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | number | 是  |  -  | 会话 ID |

- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/sessions/batch-delete

- **描述**: 批量删除当前用户自己的会话（跨设备批量登出，仅能删除本人名下的会话）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明                        |
|-------|------|----------|:--:|:---:|---------------------------|
| `ids` | Body | number[] | 是  |  -  | 要删除的会话 ID 列表（自动去重、忽略 `0`） |

- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `400` `ids` 缺失或格式非法 · `403` 目标会话不属于当前用户 · `404` 会话不存在


### GET /api/sessions/current

- **描述**: 获取当前登录会话的完整信息，包含 UI 控制台路由引导、管理模块权限 `ui.adminConsole` 及用户工作台配置
  `ui.userPortal`
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "token": "...",
          "username": "alice",
          "role": "systemAdmin",
          "ip": "127.0.0.1",
          "expiresAt": "2026-07-17T00:00:00Z",
          "file": {
            "enabled": true,
            "locations": [
              { "name": "home", "builtIn": true, "readOnly": false },
              { "name": "shared-data", "builtIn": false, "readOnly": false }
            ],
            "downloadEnabled": true,
            "uploadEnabled": true,
            "uploadMaxSizeBytes": 8589934592
          },
          "ui": {
            "adminConsole": {
              "enabled": true,
              "isWhitelist": true,
              "modules": [ "node", "connection", "scheduler", "app", "settings", "license", "storage", "status" ]
            },
            "userPortal": {
              "enabled": true,
              "isWhitelist": false,
              "modules": [ "dashboard" ]
            }
          }
        }
      }
      ```


### DELETE /api/sessions/current

- **描述**: 登出当前设备（删除当前请求使用的会话）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/sessions/current/renew

- **描述**: 续期当前会话（延长过期时间，Token 不变）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      { "data": { "expiresAt": "2026-07-17T01:00:00Z" } }
      ```

## Users

### 接口列表

| 方法      | 路径                                                     | 用途                | 鉴权                           |
|---|---|---|---|
| `GET`   | `/api/users/me`                                        | 获取当前用户信息          | 是（本用户）                       |
| `PATCH` | `/api/users/me/password`                               | 修改当前用户密码          | 是（本用户）                       |
| `POST`  | `/api/users/id-mappings`                               | 批量解析 UID/GID → 名字 | 是（已登录）                       |

### GET /api/users/me

- **描述**: 获取当前登录用户的基本信息（包含用户个人偏好设置 `preference`）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "id": 1,
          "username": "alice",
          "status": 1,
          "type": 1,
          "downloadAccess": true,
          "uploadAccess": true,
          "terminalAccess": true,
          "role": 1,
          "preference": {
            "disabledModules": ["dashboard"]
          }
        }
      }
      ```


### PATCH /api/users/me/password

- **描述**: 修改当前用户密码
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名           | 位置   | 类型     | 必选 | 默认值 | 说明         |
|---------------|------|--------|:--:|:---:|------------|
| `password`    | Body | string | 是  |  -  | 新密码        |
| `oldPassword` | Body | string | 是  |  -  | 当前密码（用于验证） |

- **响应说明**:
    - 状态码: `204 No Content`（改密成功后该用户所有会话被作废，客户端跳转登录页）
    - 错误状态码: `401` 当前密码错误 · `403` 账号不可用 · `404` 用户不存在 · `422` 新密码与当前密码相同或不符合密码策略


### POST /api/users/id-mappings

- **描述**: 将数字 UID/GID 批量解析为用户名 / 组名。
    - **解析来源**: 本机 passwd/group 打底 + LDAP 覆盖（`auth_mode=ldap` 时；同一 ID 以 LDAP 名为准，仅本机有的保留本机名）。
    - **数量约束**: 去重后 `uids`+`gids` 合计 ≤ 5000；去重前每个数组 ≤ 8192（防止"海量重复项"消耗资源），超出返回 `400`。
    - **缓存**: 已确认存在的结果默认 TTL 24h（`auth.idMappingFoundCacheTTL`）、已确认不存在的结果默认 TTL 10min（`auth.idMappingNotFoundCacheTTL`）；身份源故障时的降级结果固定短 TTL 5min，身份源恢复后自动被正常结果覆盖。这里的 Found/NotFound 表示身份源确认结果，不是数值正负。
    - **输入容错**: 字段缺省或 `null` 等价空数组；重复 ID 只处理/返回一次；`notFound` 按请求数组首次出现顺序排列；空集合序列化为 `[]` / `{}`。去重后没有任何 ID 时恒返回 `200` 空结果，**不**检查平台是否具备身份源。
    - `notFound` 仅表示身份源**已确认不存在**，不表示身份源故障。
- **鉴权**: 是（已登录用户）
- **请求示例**:
  ```json
  {
    "uids": [1000, 10001, 10002],
    "gids": [998, 9000]
  }
  ```
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "users": { "1000": "levin", "10001": "alice" },
          "groups": { "998": "wheel", "9000": "cad" },
          "notFound": { "uids": [10002], "gids": [] },
          "ttl": 86400,
          "negativeTtl": 600
        }
      }
      ```
      `ttl` / `negativeTtl` 仅反映服务端当前配置（秒），不作为客户端一致性依据。
    - 错误状态码: `400` 非法 ID / 数量超限 · `401` 未登录 · `413` 请求体过大 · `429` 触发限流 · `503` 身份源不可用且无法用可信缓存回退（空请求除外，见上）

## Connections

### 接口列表

| 方法       | 路径                                                      | 用途          | 鉴权                                 |
|---|---|---|---|
| `POST`   | `/api/connections`                                      | 创建手工连接      | 是（本用户）                             |
| `GET`    | `/api/connections`                                      | 当前用户的连接列表   | 是（本用户）                             |
| `GET`    | `/api/connections/:id`                                  | 获取连接详情      | 是（本用户）                             |
| `DELETE` | `/api/connections/:id`                                  | 删除连接        | 是（本用户）                             |
| `POST`   | `/api/connections/:id/proxy-session`                    | 创建代理会话      | 是（本用户）                             |
| `GET`    | `/api/connections/:id/proxy-session`                    | 获取代理会话      | 是（本用户）                             |
| `DELETE` | `/api/connections/:id/proxy-session`                    | 删除代理会话      | 是（本用户）                             |
| `POST`   | `/api/connections/:id/vnc-otp`                          | 生成 VNC OTP  | 是（本用户）                             |
| `POST`   | `/api/connections/:id/start`                 | 启动 Fixed VNC 连接  | 是（本用户，仅 Fixed）                    |
| `POST`   | `/api/connections/:id/stop`                  | 停止 Fixed VNC 连接  | 是（本用户，仅 Fixed）                    |
| `POST`   | `/api/connections/:id/restart`               | 重启 Fixed VNC 连接  | 是（本用户，仅 Fixed）                    |
| `POST`   | `/api/connections/batch-delete`                 | 批量删除连接      | 是（本用户）                             |
| `POST`   | `/api/connections/refresh`                      | 刷新用户连接状态    | 是（本用户）                             |

### POST /api/connections

- **描述**: 创建手工连接
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明   |
|------------|------|--------|:--:|:---:|------|
| `alias`    | Body | string | 是  |  -  | 连接别名 |
| `protocol` | Body | string | 否  |  -  | 连接协议 |
| `host`     | Body | string | 是  |  -  | 目标主机 |
| `port`     | Body | number | 是  |  -  | 目标端口 |

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/connections

- **描述**: 获取当前用户的连接列表（含管理员创建的共享 `shared` 连接）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/connections/:id

- **描述**: 获取指定连接的详情
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/connections/:id

- **描述**: 删除指定连接
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/connections/:id/proxy-session

- **描述**: 创建代理会话并返回 OTP 令牌（用于后续 WebSocket 代理连接）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "connectionId": 123,
          "status": "pending",
          "expiresAt": "2026-07-16T12:30:00Z",
          "otp": "sK3mNpQwRtYu8vXz2aB4cD"
        }
      }
      ```


### GET /api/connections/:id/proxy-session

- **描述**: 获取当前连接的活跃代理会话信息（OTP 字段不会返回）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/connections/:id/proxy-session

- **描述**: 删除当前连接的活跃代理会话
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/connections/:id/vnc-otp

- **描述**: 生成 VNC 一次性密码（用于 direct VNC Viewer 直连）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "otp": "12345678"
        }
      }
      ```


### POST /api/connections/:id/:op

- **描述**: 连接生命周期操作。接口按路径动作分发：`op` ∈ `start` / `stop` / `restart`
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明                                  |
|------|------|--------|:--:|:---:|---------------------------------------|
| `id` | Path | string | 是  |  -  | 连接 ID                                 |
| `op` | Path | string | 是  |  -  | 动作动词：`start` / `stop` / `restart`（未知动作返回 `404`） |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400` 参数非法 · `403` 无权操作该连接 · `404` 连接不存在或动作未知 · `502 Bad Gateway` 底层连接执行失败
- **更多信息**: 连接的状态机与生命周期语义见[远程访问模型](../development/remote-access/connection-model.md)


### POST /api/connections/batch-delete

- **描述**: 批量删除当前用户的连接
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明           |
|-------|------|----------|:--:|:---:|--------------|
| `ids` | Body | string[] | 是  |  -  | 要删除的连接 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`（全部成功） / `409 Conflict`（部分删除失败）


### POST /api/connections/refresh

- **描述**: 刷新当前用户的连接状态
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`

## Connection Proxy

### 接口列表

| 方法       | 路径                                   | 用途             | 鉴权     |
|---|---|---|---|
| `GET`    | `/api/connection-proxy/ws`           | WebSocket 代理入口 | 是（OTP） |
| `POST`   | `/api/connections/:id/proxy-session` | 创建代理会话         | 是（本用户） |
| `GET`    | `/api/connections/:id/proxy-session` | 获取代理会话         | 是（本用户） |
| `DELETE` | `/api/connections/:id/proxy-session` | 删除代理会话         | 是（本用户） |

### GET /api/connection-proxy/ws

- **描述**: WebSocket 代理入口。使用 OTP 令牌认证后升级为 WebSocket 连接，在客户端与目标服务之间建立双向字节流代理。
- **鉴权**: 是（OTP 令牌，无需 session cookie）。支持两种传递方式：
  1. `Authorization: Bearer <otp>` 请求头；
  2. WebSocket 子协议头 `Sec-WebSocket-Protocol: dc-proxy-token.<otp>`。
  同时携带时以 `Authorization: Bearer` 为准。
- **请求说明**: 标准 WebSocket 升级请求
- **响应说明**:
    - 状态码: `101 Switching Protocols`（成功升级）
    - `400 Bad Request`：缺少 OTP 令牌
    - `401 Unauthorized`：OTP 无效或已使用
    - `502 Bad Gateway`：无法连接目标后端服务

---

## Apps

### 接口列表

| 方法       | 路径                                 | 用途           | 鉴权                          |
|---|---|---|---|
| `GET`    | `/api/apps`                        | 列出当前用户可访问的应用 | 是（本用户）                      |
| `GET`    | `/api/apps/:id`                    | 获取应用详情       | 是（本用户）                      |
| `POST`   | `/api/apps/:id/launch`     | 启动应用         | 是（本用户）                      |

### GET /api/apps

- **描述**: 获取当前用户可访问的应用列表
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/apps/:id

- **描述**: 获取应用详情
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/apps/:id/launch

- **描述**: 启动应用（异步任务，返回作业 ID）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `202 Accepted`
    - Header: `Location: /api/scheduler/jobs/{id}`

## Scheduler

### 接口列表

| 方法     | 路径                                                       | 用途          | 鉴权                               |
|---|---|---|---|
| `GET`  | `/api/scheduler/hosts`                                   | 列出 HPC 调度计算主机与状态 | 是（本用户）                           |
| `GET`  | `/api/scheduler/jobs`                                    | 列出作业        | 是（本用户）                           |
| `GET`  | `/api/scheduler/jobs/:id`                                | 获取作业详情      | 是（本用户）                           |
| `POST` | `/api/scheduler/jobs/:id/:op`                  | 控制单个作业（op ∈ kill/suspend/resume/top/bottom/switch） | 是（本用户）                           |
| `PATCH` | `/api/scheduler/jobs/:id`                    | 修改作业名称      | 是（本用户）                           |
| `POST` | `/api/scheduler/jobs/batch-control`              | 批量控制作业      | 是（本用户）                           |

### GET /api/scheduler/hosts

- **描述**: 获取 HPC 调度集群计算主机列表与在线/离线状态汇总
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`

### GET /api/scheduler/jobs

- **描述**: 列出当前用户的作业（支持过滤）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名     | 位置    | 类型     | 必选 | 默认值 | 说明     |
|---------|-------|--------|:--:|:---:|--------|
| `user`  | Query | string | 否  |  -  | 按用户名过滤 |
| `queue` | Query | string | 否  |  -  | 按队列名过滤 |
| `node`  | Query | string | 否  |  -  | 按节点名过滤 |

- **响应说明**:
    - 状态码: `200 OK`


### GET /api/scheduler/jobs/:id

- **描述**: 获取指定作业的详细信息
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/scheduler/jobs/:id/:op

- **描述**: 控制单个作业状态（停止、挂起、恢复等）。`op` 为路径白名单动词：`kill` / `suspend` / `resume` / `top` / `bottom` / `switch`，
  非法动词返回 `400 Bad Request`
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名           | 位置   | 类型     | 必选 | 默认值 | 说明                                                                   |
|---------------|------|--------|:--:|:---:|------------------------------------------------------------------------|
| `id`          | Path | string | 是  |  -  | 作业 ID（不允许为 `0`）                                                        |
| `op`          | Path | string | 是  |  -  | 操作动词：`kill` / `suspend` / `resume` / `top` / `bottom` / `switch`       |
| `targetQueue` | Body | string | 否  |  -  | `switch` 操作时的目标队列                                                      |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400` 非法操作动词或作业 ID 为 `0` · `409 Conflict` 该作业控制失败（响应体 `message` 携带原因）


### PATCH /api/scheduler/jobs/:id

- **描述**: 修改作业属性（名称、队列等）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明    |
|---------|------|--------|:--:|:---:|-------|
| `queue` | Body | string | 否  |  -  | 新队列名  |
| `name`  | Body | string | 否  |  -  | 新作业名称 |

- **响应说明**:
    - 状态码: `202 Accepted`


### POST /api/scheduler/jobs/batch-control

- **描述**: 批量控制多个作业
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名           | 位置   | 类型       | 必选 | 默认值 | 说明                |
|---------------|------|----------|:--:|:---:|-------------------|
| `jobIds`      | Body | string[] | 是  |  -  | 作业 ID 列表          |
| `operation`   | Body | string   | 是  |  -  | 操作类型              |
| `targetQueue` | Body | string   | 否  |  -  | 目标队列（`switch` 操作） |

- **响应说明**:
    - 状态码: `202 Accepted`

## Licenses

### 接口列表

| 方法       | 路径                               | 用途        | 鉴权                              |
|---|---|---|---|
| `GET`    | `/api/licenses`                  | 列出许可证服务   | 是（已登录用户）                        |
| `GET`    | `/api/licenses/:id`              | 获取许可证服务详情 | 是（已登录用户）                        |
| `GET`    | `/api/licenses/:id/features`     | 获取许可证功能列表 | 是（已登录用户）                        |
| `GET`    | `/api/licenses/:id/status`       | 获取许可证运行状态 | 是（已登录用户）                        |

### GET /api/licenses

- **描述**: 获取所有启用的许可证服务列表（自动过滤 `enabled=false` 的记录）。各条目 `status` 始终输出内存快照值（未探测或快照 miss 输出 `unknown`，不回源触发外部 CLI）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/licenses/:id

- **描述**: 获取指定许可证服务的详细信息（叠加内存快照状态）
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在或未启用 `enabled=false`）


### GET /api/licenses/:id/features

- **描述**: 获取指定许可证服务支持的功能列表与用量明细。查询结果经进程内 TTLCache 节流（TTL 由 `settings.license.featuresCacheTTL` 动态控制，默认 60s，防击穿保护）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在或未启用）


### GET /api/licenses/:id/status

- **描述**: 获取指定许可证服务的实时运行状态。查询结果经进程内 TTLCache 节流（TTL 由 `settings.license.statusCacheTTL` 动态控制，默认 60s）。状态枚举取值：`up`（服务正常）、`down`（服务离线或异常）、`unknown`（未知）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在或未启用）


## Files

> **文件功能总开关**：所有 `/api/files/*` 接口统一受 `file.enabled` 门控。当管理员在平台设置中禁用文件功能时（`file.enabled = false`），所有文件端点将拒绝访问并返回 `403 Forbidden`（`{"error": "file service disabled"}`）。此外，具体操作分别受到 `downloadEnabled`、`uploadEnabled`、`requiresApproval` 审批策略、用户个人权限以及底层文件系统 POSIX 权限的共同约束。

### 接口列表

| 方法       | 路径                                                               | 用途                | 鉴权                             |
|---|---|---|---|
| `POST`   | `/api/files/download-requests`                                   | 提交下载审批申请          | 是（本用户）                         |
| `GET`    | `/api/files/download-requests`                                   | 查看自己的下载申请         | 是（本用户）                         |
| `GET`    | `/api/files/download-requests/:id`                               | 查看下载申请详情          | 是（本用户）                         |
| `POST`   | `/api/files/download-requests/:id/tokens`                        | 从已批准的申请换取下载 Token | 是（本用户）                         |
| `POST`   | `/api/files/download-tokens`                                     | 创建下载 Token        | 是（本用户）                         |
| `GET`    | `/api/files/downloads/:token`                                    | 凭 Token 下载文件（流式）  | 是（已登录用户，文件传输限流）                |
| `GET`    | `/api/files/locations`                                           | 列出文件位置            | 是（本用户）                         |
| `GET`    | `/api/files/locations/:name/contents`                            | 列出目录内容            | 是（本用户）                         |
| `POST`   | `/api/files/locations/:name/contents`                            | 创建目录              | 是（本用户）                         |
| `GET`    | `/api/files/locations/:name/contents/*`                          | 列出子目录内容           | 是（本用户）                         |
| `PUT`    | `/api/files/locations/:name/contents/*`                          | 上传文件（流式）          | 是（已登录用户，文件传输限流）                |
| `POST`   | `/api/files/locations/:name/contents/batch-delete`       | 批量删除              | 是（本用户）                         |
| `POST`   | `/api/files/trash/items`        | 批量移至回收站           | 是（本用户）                         |
| `POST`   | `/api/files/locations/:name/contents/rename`             | 重命名文件/目录          | 是（本用户）                         |
| `GET`    | `/api/files/trash/contents`                                      | 列出回收站内容           | 是（本用户）                         |
| `GET`    | `/api/files/trash/contents/*`                                    | 列出回收站子目录内容        | 是（本用户）                         |
| `DELETE` | `/api/files/trash/items`                                         | 清空回收站             | 是（本用户）                         |
| `DELETE` | `/api/files/trash/items/:trashName`                              | 删除回收站中单个项目        | 是（本用户）                         |
| `POST`   | `/api/files/trash/items/batch-delete`                    | 批量永久删除回收站项目       | 是（本用户）                         |

### POST /api/files/download-requests

- **描述**: 提交下载审批申请（适用于需要审批的位置）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明   |
|------------|------|--------|:--:|:---:|------|
| `location` | Body | string | 是  |  -  | 文件位置 |
| `path`     | Body | string | 是  |  -  | 文件路径 |

- **响应说明**:
    - 状态码: `202 Accepted`


### GET /api/files/download-requests

- **描述**: 查看当前用户的下载申请列表
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名      | 位置    | 类型     | 必选 | 默认值 | 说明    |
|----------|-------|--------|:--:|:---:|-------|
| `status` | Query | string | 否  |  -  | 按状态过滤 |

- **响应说明**:
    - 状态码: `200 OK`


### GET /api/files/download-requests/:id

- **描述**: 查看指定下载申请的详情
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/files/download-requests/:id/tokens

- **描述**: 审批通过后，从下载申请换取一次性下载 Token
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `201 Created`


### POST /api/files/download-tokens

- **描述**: 直接创建一次性下载 Token（适用于无需审批的位置）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明   |
|------------|------|--------|:--:|:---:|------|
| `location` | Body | string | 是  |  -  | 文件位置 |
| `path`     | Body | string | 是  |  -  | 文件路径 |

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/files/downloads/:token

- **描述**: 凭 Token 下载文件（流式二进制响应）
- **鉴权**: 是（已登录用户，文件传输限流）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明       |
|---------|------|--------|:--:|:---:|----------|
| `token` | Path | string | 是  |  -  | 下载 Token |

- **响应说明**:
    - 状态码: `200 OK`（返回文件二进制流）


### GET /api/files/locations

- **描述**: 列出当前用户可访问的文件存储位置
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/files/locations/:name/contents

- **描述**: 列出指定文件位置下的目录和文件列表
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名    | 位置    | 类型     | 必选 | 默认值 | 说明                    |
|--------|-------|--------|:--:|:---:|-----------------------|
| `name` | Path  | string | 是  |  -  | 文件位置名称                |
| `path` | Query | string | 否  |  -  | 子路径（也可通过 `/*` 路径参数指定） |

- **响应说明**:
    - 状态码: `200 OK`


### POST /api/files/locations/:name/contents

- **描述**: 在指定位置创建新目录
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名    | 位置   | 类型     | 必选 | 默认值 | 说明   |
|--------|------|--------|:--:|:---:|------|
| `name` | Body | string | 是  |  -  | 目录名称 |
| `path` | Body | string | 否  |  -  | 父路径  |

- **响应说明**:
    - 状态码: `201 Created`


### PUT /api/files/locations/:name/contents/*

- **描述**: 上传文件到指定位置（流式上传）
- **鉴权**: 是（已登录用户，文件传输限流）
- **请求参数**: 请求体为文件二进制内容
- **响应说明**:
    - 状态码: `201 Created`


### POST /api/files/locations/:name/contents/batch-delete

- **描述**: 批量删除文件或目录
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名     | 位置   | 类型       | 必选 | 默认值 | 说明       |
|---------|------|----------|:--:|:---:|----------|
| `paths` | Body | string[] | 是  |  -  | 要删除的路径列表 |

- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/files/trash/items

- **描述**: 批量将文件或目录移至回收站
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名     | 位置   | 类型       | 必选 | 默认值 | 说明          |
|---------|------|----------|:--:|:---:|-------------|
| `paths` | Body | string[] | 是  |  -  | 要移入回收站的路径列表 |

- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/files/locations/:name/contents/rename

- **描述**: 重命名文件或目录
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名    | 位置   | 类型     | 必选 | 默认值 | 说明  |
|--------|------|--------|:--:|:---:|-----|
| `path` | Body | string | 是  |  -  | 原路径 |
| `name` | Body | string | 是  |  -  | 新名称 |

- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/files/trash/contents

- **描述**: 列出回收站中的文件和目录
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/files/trash/items

- **描述**: 清空回收站所有内容
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`


### DELETE /api/files/trash/items/:trashName

- **描述**: 永久删除回收站中的单个项目
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/files/trash/items/batch-delete

- **描述**: 批量永久删除回收站中的项目
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名     | 位置   | 类型       | 必选 | 默认值 | 说明        |
|---------|------|----------|:--:|:---:|-----------|
| `names` | Body | string[] | 是  |  -  | 回收站项目名称列表 |

- **响应说明**:
    - 状态码: `204 No Content`

## Nodes

### 接口列表

| 方法       | 路径                                        | 用途          | 鉴权                          |
|---|---|---|---|
| `POST`   | `/api/nodes/:id/registration`             | 节点 OTP 注册   | 否（预共享 Token）                |

### POST /api/nodes/:id/registration

- **描述**: 节点用一次性注册 Token 领取或重新领取证书材料（`cert` / `key` / `ca`）。路径中的 `id` 必须与节点运行时身份逐字一致；Windows 上该身份是 Physical DNS 主机名（`os.Hostname()`），不是 NetBIOS 的 `$env:COMPUTERNAME`。
- **鉴权**: 否（使用预共享注册 Token）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明       |
|---------|------|--------|:--:|:---:|----------|
| `id`    | Path | string | 是  |  -  | 节点 ID，须已存在 |
| `token` | Body | string | 是  |  -  | 注册 Token |

- **语义**: 节点记录必须已存在。有效且未过期的 token 即授权首次签发或重新签发，不要求节点仍为 `pending`，也不要求尚未绑定证书。成功后写入新的证书指纹、主题与到期时间，状态变为 `provisioned`，并清空 token。节点证书的 Organization 固定为 `Design Cloud Engine`，不是签发方节点 ID。
- **响应说明**:
    - 状态码: `200 OK`
    - `data` 字段：`nodeId`、`serverAddr`、`certPem`、`keyPem`、`caPem`、`certExpiresAt`
    - `400 Bad Request`：请求体无法解析
    - `401 Unauthorized`：token 不匹配
    - `404 Not Found`：节点不存在
    - `409 Conflict`：token 缺失或已过期（需管理员重新签发后再领取）

## Tickets

### 接口列表

| 方法       | 路径                                               | 用途           | 鉴权                             |
|---|---|---|---|
| `GET`    | `/api/tickets`                                   | 列出工单         | 是（已登录用户）                       |
| `POST`   | `/api/tickets`                                   | 创建工单         | 是（已登录用户）                       |
| `GET`    | `/api/tickets/:id`                               | 获取工单详情       | 是（已登录用户）                       |
| `PATCH`  | `/api/tickets/:id`                               | 更新工单         | 是（已登录用户）                       |
| `DELETE` | `/api/tickets/:id`                               | 删除工单（软删，仅自己） | 是（本用户）                         |
| `GET`    | `/api/tickets/projects`                          | 项目列表         | 是（已登录用户）                       |
| `GET`    | `/api/tickets/summary`                           | 工单统计摘要       | 是（已登录用户）                       |
| `GET`    | `/api/tickets/users`                             | 用户列表（用于指派）   | 是（已登录用户）                       |

### GET /api/tickets

- **描述**: 列出工单（支持分页和多种过滤条件）
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数名          | 位置    | 类型      | 必选 | 默认值 | 说明     |
|--------------|-------|---------|:--:|:---:|--------|
| `projectId`  | Query | integer | 否  |  -  | 项目 ID  |
| `type`       | Query | integer | 否  |  -  | 工单类型   |
| `status`     | Query | integer | 否  |  -  | 状态     |
| `priority`   | Query | integer | 否  |  -  | 优先级    |
| `reporterId` | Query | integer | 否  |  -  | 报告人 ID |
| `assigneeId` | Query | integer | 否  |  -  | 指派人 ID |

- **校验**: `projectId`/`reporterId`/`assigneeId`/`type`/`status`/`priority` 须为数字，非数字值返回 `400 Bad Request`。
  | `limit`      | Query | number | 否 | - | 分页大小 |
  | `offset`     | Query | number | 否 | - | 分页偏移 |

- **响应说明**:
    - 状态码: `200 OK`


### POST /api/tickets

- **描述**: 创建新工单（Reporter 自动设为当前用户）
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数名                  | 位置   | 类型       | 必选 | 默认值 | 说明                                                |
|----------------------|------|----------|:--:|:---:|---------------------------------------------------|
| `projectId`          | Body | integer  | 是  |  -  | 所属项目 ID                                           |
| `type`               | Body | integer  | 是  |  0  | 工单类型（0~2）                                         |
| `title`              | Body | string   | 是  |  -  | 标题（最长 256）                                        |
| `description`        | Body | string   | 否  |  -  | 描述                                                |
| `assigneeId`         | Body | integer  | 是  |  -  | 指派人 ID                                            |
| `dueDate`            | Body | string   | 否  |  -  | 截止日期（RFC3339）                                     |
| `priority`           | Body | integer  | 否  |  -  | 优先级（0~3）                                          |
| `status`             | Body | integer  | 否  |  -  | 状态（0~3）                                           |
| `acceptanceCriteria` | Body | object   | 否  |  -  | 验收标准：`{description, checklist[]}`                 |
| `attachments`        | Body | object[] | 否  |  -  | 附件列表：`[{fileName, fileSize, filePath, mimeType}]` |

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/tickets/:id

- **描述**: 获取指定工单的详细信息
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`


### PATCH /api/tickets/:id

- **描述**: 更新工单的指定字段。所有请求字段均为可选（omitempty），仅传入需要修改的字段。
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数名                  | 位置   | 类型       | 必选 | 默认值 | 说明            |
|----------------------|------|----------|:--:|:---:|---------------|
| `type`               | Body | integer  | 否  |  -  | 工单类型          |
| `title`              | Body | string   | 否  |  -  | 标题            |
| `description`        | Body | string   | 否  |  -  | 描述            |
| `assigneeId`         | Body | integer  | 否  |  -  | 指派人 ID        |
| `dueDate`            | Body | string   | 否  |  -  | 截止日期（RFC3339） |
| `priority`           | Body | integer  | 否  |  -  | 优先级           |
| `status`             | Body | integer  | 否  |  -  | 状态            |
| `acceptanceCriteria` | Body | object   | 否  |  -  | 验收标准          |
| `attachments`        | Body | object[] | 否  |  -  | 附件列表          |
| `sortOrder`          | Body | number   | 否  |  -  | 排序权重          |

- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/tickets/:id

- **描述**: 软删除工单（仅 Reporter 可删）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/tickets/projects

- **描述**: 获取项目列表
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数名      | 位置    | 类型     | 必选 | 默认值 | 说明   |
|----------|-------|--------|:--:|:---:|------|
| `limit`  | Query | number | 否  |  -  | 分页大小 |
| `offset` | Query | number | 否  |  -  | 分页偏移 |

- **响应说明**:
    - 状态码: `200 OK`


### GET /api/tickets/summary

- **描述**: 获取工单统计摘要
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数名         | 位置    | 类型      | 必选 | 默认值 | 说明    |
|-------------|-------|---------|:--:|:---:|-------|
| `projectId` | Query | integer | 否  |  -  | 项目 ID |

- **校验**: `projectId` 须为数字，非数字值返回 `400 Bad Request`。

- **响应说明**:
    - 状态码: `200 OK`


### GET /api/tickets/users

- **描述**: 获取用户列表（用于指派工单时的下拉框）
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`

## Settings

### 接口列表

| 方法    | 路径                                          | 用途                              | 鉴权                             |
|---|---|---|---|
| `GET` | `/api/settings/platform`                    | 获取平台设置                          | 是（已登录用户）                       |

### GET /api/settings/platform

- **描述**: 获取平台级公开设置（平台名称、描述、技术支持链接等）
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`

## Dashboard

### 接口列表

| 方法    | 路径                          | 用途            | 鉴权       |
|---|---|---|---|
| `GET` | `/api/dashboard`            | 获取集群状态概览      | 是（已登录用户） |
| `GET` | `/api/dashboard?stream=true` | Dashboard 实时数据流（SSE，变更驱动推送） | 是（已登录用户） |

> **大屏访客模式**: 当平台启用公开 Dashboard 配置时，持有 PublicDashboardToken 的请求通过 `Authorization: Bearer <token>` 请求头或 `?token=<token>` Query 参数传递（适配浏览器原生 EventSource），以只读访客身份获得访问权限。
> 该能力受 `enable_dashboard` 门控：未开启公开大屏时此类请求返回 `403 Forbidden`。

### GET /api/dashboard

- **描述**: 获取集群聚合状态概览（节点、作业、存储等）
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "name": "设计云平台",
          "sampled_at": "2026-08-10T17:57:00Z",
          "nodes": { "configured": 128, "online": 122 },
          "cpu": { "configured": 8192, "online": 7808, "utilization": 48.5 },
          "load": { "1m": 3747.8, "5m": 4216.3, "15m": 4528.6 },
          "memory": { "configured": 32768.0, "online": 31232.0, "used": 19660.0, "unit": "GB" },
          "gpu": {
            "nodes": { "configured": 16, "online": 16 },
            "cards": { "configured": 128, "online": 124, "utilization": 52.0 },
            "vram": { "configured": 10240.0, "online": 9920.0, "used": 5952.0, "unit": "GB" }
          },
          "scheduler": {
            "hosts": { "online": 16, "total": 16 },
            "jobs": {
              "running": 2680,
              "pending": 165,
              "suspended": 12,
              "completed": 4520,
              "failed": 18
            },
            "queues": [
              { "name": "normal", "allocated": 100, "running": 80, "pending": 15, "suspended": 3, "completed": 400, "failed": 2 }
            ]
          },
          "storage": {
            "summary": { "total": 7600.0, "used": 4546.0, "unit": "TB", "utilization": 59.8 },
            "items": [
              { "id": "st-01", "name": "NFS-Share", "type": "nfs", "total": 400.0, "used": 256.0, "unit": "TB", "sampled_at": "2026-08-10T17:24:00Z" }
            ]
          }
        }
      }
      ```
    - **字段说明（3阶自解释规范）**:

| 资源领域 | 字段 | 类型 | 说明 |
|---|---|---|---|
| **`nodes`** | `configured` | integer | 全网物理注册/配置总节点数（DCE Agent） |
| | `online` | integer | 当前健康在线的可用节点数（DCE Agent） |
| **`cpu`** | `configured` | integer | 全网物理设计装机总 CPU 核心数 |
| | `online` | integer | 当前在线可用 CPU 核心数 |
| | `utilization` | float | 核心加权平均利用率 (%) |
| **`memory`** | `configured` | float | 全网物理设计装机总内存 (GB) |
| | `online` | float | 当前健康在线节点可用内存 (GB) |
| | `used` | float | 实时实际已用内存容量 (GB) |
| **`gpu.nodes`** | `configured` / `online` | integer | 全网物理设计 GPU 节点数 / 当前在线 GPU 节点数 |
| **`gpu.cards`** | `configured` / `online` | integer | 全网物理设计 GPU 总卡数 / 当前在线 GPU 卡数 |
| | `utilization` | float | GPU 平均算力利用率 (%) |
| **`gpu.vram`** | `configured` / `online` / `used` | float | 物理设计总显存 / 在线可用显存 / 实时实际已用显存 |
| **`scheduler.hosts`** | `online` / `total` | integer | HPC 调度集群在线计算主机数 / 总计算主机数 |
| **`scheduler.jobs`** | `running` / `pending` / `suspended` | integer | 调度集群运行中 / 等待中 / 已暂停作业数 |
| | `completed` / `failed` | integer | 调度集群已完成 / 失败作业数 |
| **`scheduler.queues`** | `queues[].name` | string | 队列名（分区） |
| | `queues[].allocated` | integer | 队列中已分配 / 活跃作业数 |
| | `queues[].running` / `pending` / `suspended` | integer | 队列中运行 / 等待 / 暂停作业数 |
| | `queues[].completed` / `failed` | integer | 队列中完成 / 失败作业数 |
| **`storage.summary`** | `total` / `used` | float | 共享存储总容量 / 已用容量 |
| | `unit` | string | 容量单位（自动缩放 TB/GB） |
| | `utilization` | float | 存储利用率 (%) |
| **`storage.items`** | `id` / `name` / `type` | string | 存储 ID / 显示名 / 类型 |
| | `total` / `used` | float | 该存储总容量 / 已用容量 |
| | `unit` / `sampled_at` | string / datetime | 容量单位 / 采样时间 |

### GET /api/dashboard?stream=true

- **描述**: Dashboard 实时数据流。连接建立后立即推送第一帧完整快照，之后每 **30s** 推送一次；数据无变化时仅发送心跳（SSE 注释行），有变化才推完整帧。
- **鉴权**: 是（已登录用户；大屏 Guest Token 亦可访问）
- **响应格式**: `text/event-stream`，命名事件 **`event: dashboard`** + `data:`（内容与 `GET /api/dashboard` 返回结构一致）。客户端须用 `addEventListener('dashboard', ...)` 订阅。

---

## 实时推送（SSE）

服务端通过 **SSE（Server-Sent Events）** 提供实时推送，替代前端轮询。连接建立后持续推送，断开即停；客户端断线需自行重连。

### GET /api/events

- **描述**: 全局通知流。登录后建立常驻连接，服务端按当前用户（UserID + Role）自动决定推送内容，客户端无需传参。
- **鉴权**: 是（已登录用户）
- **响应格式**: `text/event-stream`，帧含 `event:` / `data:`（部分含 `id:` / `retry:`），每 **15s** 发送一次心跳保活。
- **事件类型（`event:` 字段）**: `job`（作业）/ `ticket`（工单）/ `connection`（连接）/ `node`（节点）/ `session`（会话，含强制下线）/ `system`（系统告警）/ `notification`（通知）
- **统一载荷（`data:` 字段，EventPayload）**:

| 字段 | 类型 | 说明 |
|---|---|---|
| `action` | string | 指令：`refresh`（刷新数据）/ `logout`（强制下线）/ `notify`（仅通知） |
| `targetId` | string | 可选；`refresh` 时有值=局部刷新该实体，无值=全量刷新列表 |
| `title` | string | 可选；通知弹窗标题 |
| `message` | string | 可选；通知弹窗内容 |
| `level` | string | 可选；`info` / `success` / `warning` / `error` |
| `extra` | object | 可选；预留扩展 |

- **示例**:
  ```
  event: job
  data: {"action":"refresh","targetId":"job-abc","title":"作业完成","message":"模型训练已完成","level":"success"}
  ```

---

## Version

### 接口列表

| 方法    | 路径                             | 用途                 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/version`                 | 获取服务端版本信息          | 否  |
| `GET` | `/api/version/desktop/download/:filename` | 下载指定 Desktop 安装包 | 否  |
| `GET` | `/api/version/desktop/download/latest` | 下载最新 Desktop 安装包 | 否  |
| `GET` | `/api/version/desktop/latest` | 获取 Desktop 客户端最新版本 | 否  |

### GET /api/version

- **描述**: 获取服务端版本信息（version、commit、releaseDate）
- **鉴权**: 否（公开）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/version/desktop/download/:filename

- **描述**: 下载 Desktop 客户端安装包。客户端可由 `latest` 端点返回的 `assets[].FileName` 拼接此路径（`/api/version/desktop/download/:filename`）进行下载
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `filename` | Path | string | 是 | 发布包文件名（如 `DesignCloudDesktop-1.0.0.nupkg`） |

- **响应说明**:
    - 状态码: `200 OK`（文件流）
    - 状态码: `404 Not Found`（文件不存在或文件名非法）

---


### GET /api/version/desktop/download/latest

- **描述**: 下载最新 Desktop 客户端安装包
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `os` / `platform` | Query | string | 否 | 平台过滤，语义同 `latest` 端点 |

- **响应说明**:
    - 状态码: `200 OK`（文件流）
    - 状态码: `404 Not Found`（无可用发布包）


### GET /api/version/desktop/latest

- **描述**: 获取 Desktop 客户端最新版本信息（用于客户端自动更新检查）
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `limit` | Query | int | 否 | 返回资产数量上限；不能超过管理员设置的 `maxAssets`（默认 5） |
| `os` / `platform` | Query | string | 否 | 平台：`win`/`windows`、`linux`、`macos`/`osx`/`darwin`（macOS 读取 `releases.osx.json`）；缺省默认 `win`；非空且不支持的值返回 `404` |

- **响应说明**:
    - 状态码: `200 OK`
    - 状态码: `404 Not Found`（未配置发布或管理员已禁用发布）
