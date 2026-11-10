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
| `GET`    | `/api/sessions/current`               | 当前会话基本信息      | 是（本用户）                            |
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
| `client`   | Body | object | 否  |  -  | 客户端自报信息；整体缺省时各字段为空字符串 |
| `client.hostname` | Body | string | 否 | `""` | 客户端主机名，存入会话供会话列表展示（去除首尾空白，最长 255 字符） |
| `client.platform` | Body | string | 否 | `""` | 操作系统家族，小写，如 `windows`、`macos`、`linux`（去除首尾空白，最长 32 字符） |
| `client.osVersion` | Body | string | 否 | `""` | 操作系统版本号，如 `10.0.19045`、`14.6.1`（去除首尾空白，最长 64 字符） |
| `client.appVersion` | Body | string | 否 | `""` | 客户端应用版本号，如 `v2.3.1`（去除首尾空白，最长 64 字符） |
| `client.deviceId` | Body | string | 否 | `""` | 客户端安装实例的稳定设备标识（如 UUID），用于区分同类设备（去除首尾空白，最长 64 字符） |

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
          "multiSessionEnabled": true,
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
              "mode": "allow",
              "modules": []
            },
            "userPortal": {
              "enabled": true,
              "mode": "deny",
              "modules": []
            }
          }
        }
      }
      ```
    - `multiSessionEnabled`：同一用户是否允许多个会话并存（`auth.maxSessionsPerUser > 1`），客户端据此决定是否展示活跃会话列表。
    - `ui.adminConsole.modules` 是基于角色与管理 Scope 生成的可见模块 allow 列表；会话管理 `sessions`（`ScopeSessions`）与安全管理 `security`（`ScopeBans`）分别列出。`ui.userPortal.modules` 是平台 `disabledModules` 中值为 `true` 的模块与用户个人禁用模块合并后的 deny 列表。模块标识是稳定的模块 ID，不是 API 路由（如 `ai`、`app`、`connection`、`file`、`license`、`overview`、`scheduler`、`ticket`）；例如设置 `platform.disabledModules.ticket=true` 会隐藏普通用户端工单导航，`platform.disabledModules.ai=true` 会隐藏 AI 工作台导航。
- **受限会话说明**: 当登录响应中 `passwordMustChange` 为 `true`（密码已过期）时，该会话进入**受限模式**：仅允许访问白名单端点
  `/api/users/me/password`、`/api/users/me`、`/api/sessions/current`、`/api/sessions`，其余端点一律返回 `428 Precondition Required`
  （message: `password change required`）。改密成功后恢复正常访问。


### GET /api/sessions

- **描述**: 获取当前用户的活跃会话列表
- **鉴权**: 是（本用户）
- **请求参数**: `limit` / `offset` 同 [列表分页约定](../_index.md#列表分页limit--offset)
- **响应说明**:
    - 状态码: `200 OK`
    - 分页 envelope：`data.items` / `data.total`
    - 响应示例:
      ```json
      {
        "data": {
          "items": [
          {
            "id": 12,
            "username": "alice",
            "ip": "10.0.0.8",
            "hostname": "alice-laptop",
            "platform": "windows",
            "osVersion": "10.0.19045",
            "appVersion": "v2.3.1",
            "deviceId": "5f9c1a2e-8b7d-4c3a-9e10-2d6b7a8c4f11",
            "role": 0,
            "type": "system",
            "passwordMustChange": false,
            "createdAt": "2026-07-16T08:00:00Z",
            "updatedAt": "2026-07-16T08:00:00Z",
            "expiresAt": "2026-07-17T08:00:00Z",
            "current": true
          }
          ],
          "total": 1
        }
      }
      ```
    - `current`：只在发起本次请求的会话上输出 `true`，其他会话不输出该字段。
    - `hostname` / `platform` / `osVersion` / `appVersion` / `deviceId`：登录时客户端自报的值，未上报时为空字符串。


### DELETE /api/sessions

- **描述**: 登出**所有设备**（删除当前登录用户名下的全部会话）。与 `POST /api/sessions/batch-delete`（仅删除 body 中列出的会话 ID）不同，本端点无请求体、不可指定 ID。
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

- **描述**: 获取当前会话的基本信息
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "id": 12,
          "username": "alice",
          "token": "...",
          "role": "normalUser",
          "ip": "10.0.0.8",
          "expiresAt": "2026-07-17T08:00:00Z",
          "multiSessionEnabled": true
        }
      }
      ```
    - `multiSessionEnabled`：含义同登录响应。


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
    - 错误状态码: `401 Unauthorized`（会话无效或已过期）· `428 Precondition Required`（`password change required`，账号密码已过期须先改密）
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
          "role": 1,
          "preference": {
            "disabledModules": ["overview"]
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

用户侧不提供 `PATCH /api/connections/:id`：实例事实字段（落点、端口、VNC display 等）在创建时确定，变更请删除后重建。连接**规格**由管理员通过 `/api/admin/system/connections/resources` 维护（见 [管理端 · 连接管理](./admin.md#连接管理connections)）。

**远程接入**：直连 TurboVNC 使用 `POST .../vnc-otp`；仅可达 Portal 时使用 `GET /api/connections/:id/proxy-connection`（VNC/RDP/XRDP）。**SSH** 不创建 connection 实例，直接 `GET /api/connections/ssh?resourceId=`（WebSocket Upgrade + Portal Session，详见 [协议代理](/docs/architecture/remote-access/proxy)）。

### 接口列表

| 方法       | 路径                                                      | 用途          | 鉴权                                 |
|---|---|---|---|
| `GET`    | `/api/connections/resources`                            | 当前用户可用规格列表   | 是（本用户）                             |
| `GET`    | `/api/connections/resources/:id`                        | 获取可用规格详情      | 是（本用户）                             |
| `GET`    | `/api/connections`                                      | 当前用户的连接实例列表 | 是（本用户）                             |
| `POST`   | `/api/connections`                                      | 从规格启动新连接实例   | 是（本用户）                             |
| `GET`    | `/api/connections/:id`                                  | 获取连接实例详情      | 是（本用户）                             |
| `DELETE` | `/api/connections/:id`                                  | 删除连接实例        | 是（本用户）                             |
| `GET`    | `/api/connections/ssh`                                  | SSH 代理隧道（须 `?resourceId=` + WebSocket Upgrade） | 是（本用户）                             |
| `GET`    | `/api/connections/:id/proxy-connection`                    | 代理隧道（Upgrade）或查询本机会话（VNC/RDP/XRDP） | 是（本用户）                             |
| `POST`   | `/api/connections/:id/vnc-otp`                          | 生成 VNC OTP      | 是（本用户）                             |
| `POST`   | `/api/connections/:id/touch`                            | 上报连接活跃访问时间   | 是（本用户）                             |
| `POST`   | `/api/connections/:id/start`                            | 启动连接实例（按资源重选机） | 是（本用户）                             |
| `POST`   | `/api/connections/:id/stop`                             | 结束远程会话并写 stopped 墓碑（VNC/XRDP/RDP） | 是（本用户）                             |
| `POST`   | `/api/connections/:id/restart`                          | 重启连接实例（stop+start） | 是（本用户）                             |
| `POST`   | `/api/connections/batch-delete`                         | 批量删除连接实例      | 是（本用户）                             |
| `POST`   | `/api/connections/refresh`                              | 刷新用户连接状态      | 是（本用户）                             |

### GET /api/connections/resources

- **描述**: 获取当前用户可见并可申请的连接规格列表（包含公开规格及已授权规格，附带 `usedCount` 及会话存在状态）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - `data` 为数组；每项含规格字段（`name`、`protocol`、`nodes`、`nodeGroups`、`maxPerUser`、`vncConfig`、`public` 等，camelCase）以及 **`usedCount`**（当前用户在该规格下的实例行数）、**`resolvedPort`**（展示用解析端口，无则省略）

### GET /api/connections/resources/:id

- **描述**: 获取指定可用连接规格的详情
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 默认值 | 说明 |
|---|---|---|:---:|:---:|---|
| `id` | Path | string | 是 | - | 规格 ID |

- **响应说明**:
    - 状态码: `200 OK`

### POST /api/connections

- **描述**: 从指定连接资源规格拉起新建连接实例
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 默认值 | 说明 |
|---|---|---|:---:|:---:|---|
| `resourceId` | Body | uint | 是 | - | 资源规格 ID |

- **响应说明**:
    - 状态码: `201 Created`
    - 错误状态码: `400` 参数非法 · `404` 规格不存在或无权访问 · `409 Conflict`（主要为 **VNC**：规格配额已满、显示号占用、候选节点无空闲显示号等）。**RDP** 在已达 `maxPerUser` 或候选节点用尽时会**复用**已有实例行并返回 `201`，不因「已有会话」返回 `409`。**SSH** 返回 `422`（不创建实例；改走 `GET /api/connections/ssh?resourceId=`）

### GET /api/connections

- **描述**: 获取当前用户的连接实例列表（包含运行中、已停止及 `resourceId` 为空的无归属实例，按 `lastAccessedAt` 倒序）。各协议行的字段与状态语义见 [连接模型 · 状态探测](/docs/architecture/remote-access/connection#状态探测与对账按协议)。
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

协议代理（`GET .../proxy-connection`、`GET /api/connections/ssh`）的完整说明见 [Connection Proxy](#connection-proxy)。

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


### POST /api/connections/:id/touch

- **描述**: 上报连接仍在活跃使用（更新 `lastAccessedAt`，引擎 60 秒节流写入）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | string | 是  |  -  | 连接 ID |

- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/connections/:id/:op

- **描述**: 连接生命周期操作。分发动作：`op` ∈ `start` / `stop` / `restart`
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明                                  |
|------|------|--------|:--:|:---:|---------------------------------------|
| `id` | Path | string | 是  |  -  | 连接 ID                                 |
| `op` | Path | string | 是  |  -  | 动作动词：`start`（按资源重选机拉起）、`stop`（结束远程会话并写 stopped 墓碑，全协议）、`restart`（stop+start） |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400` 参数非法或协议不匹配 · `403` 无权操作该连接 · `404` 连接不存在或动作未知 · `502 Bad Gateway` 底层连接执行失败
- **更多信息**: 连接的状态机与生命周期语义见 [远程访问模型](/docs/architecture/remote-access/connection)


### POST /api/connections/batch-delete

- **描述**: 批量删除当前用户的连接
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明           |
|-------|------|----------|:--:|:---:|--------------|
| `ids` | Body | integer[] | 是  |  -  | 要删除的连接 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`（全部可删目标已删除）
    - `400 Bad Request`：`ids` 为空
    - `403 Forbidden`：列表中任一连接非当前用户所属（**整批中止**，不部分删除）
    - 不存在的 ID 静默忽略；无部分失败聚合语义


### POST /api/connections/refresh

- **描述**: 刷新当前用户名下 **VNC** 连接实例的运行态：向各落点节点下发 dc-exec `CONNECTION_CHECK`（PID/`/proc` 校验），据结果更新 `connections.status`。**不包含** RDP/SSH 实例，也不触发全集群 connection-probe。同用户短时间内的重复请求会命中冷却窗口并被静默忽略（仍返回 `204`）。
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `204 No Content`

## Connection Proxy

桌面 Portal 客户端集成契约（探活、重连、与 TurboVNC OTP 区分）见 [协议代理 · 桌面客户端集成契约](/docs/architecture/remote-access/proxy#桌面客户端集成契约)。

### 接口列表

| 方法       | 路径                                   | 用途             | 鉴权     |
|---|---|---|---|
| `GET`    | `/api/connections/ssh` | **SSH**：`?resourceId=` + WebSocket Upgrade 建隧（不写 `connections` 行；无 Upgrade 查询） | 是（本用户） |
| `GET`    | `/api/connections/:id/proxy-connection` | **VNC/RDP/XRDP**：Upgrade 建隧，或查询本机 active 会话 | 是（本用户） |

### GET /api/connections/ssh（WebSocket Upgrade）

- **描述**: SSH 专用：查询参数 `resourceId`（必填）指定可访问的 SSH 规格 → 当场选机 → 拨目标 TCP 并桥接 WebSocket。**不**创建或复用 `connections` 行；桌面关闭 WebSocket 即结束。无 Upgrade 时返回 `400`。规格字段 `maxPerUser` / `maxTotal` **不**用于拒绝隧道（多 Engine 下单节点计数语义不成立）；本机并发仅受 `connection.proxy.maxProxyConnections*`，同 `(resourceId, requester)` 再 Upgrade 顶替本机旧会话。
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `resourceId` | Query | uint | 是 | SSH 连接规格 ID |

- **响应**: `101 Switching Protocols`；`400` 缺 `resourceId` 或未 Upgrade；`403`/`422` 协议未启用或非 SSH 规格；`404` 规格不可访问；`429` 本机配额已满；`502`/`503` 拨号或选机失败

### GET /api/connections/:id/proxy-connection（WebSocket Upgrade）

- **描述**: 一次握手建立协议代理（VNC/RDP/XRDP）：`Upgrade: websocket` + `Authorization: Bearer <portal-session>`。**SSH 不可走本路径**（`422`，改用 `GET /api/connections/ssh?resourceId=`）。设计细节见 [协议代理](/docs/architecture/remote-access/proxy)。
- **响应**: `101 Switching Protocols`；`403` 无权或协议未启用；`429` 本机配额已满；`502` 拨号失败（不留下会话）

### GET /api/connections/:id/proxy-connection（无 Upgrade）

- **描述**: 返回**受理节点**上当前用户在该 connection 的 active 隧道元数据（`data`）。
- **响应**: `200 OK`；`404` 本机无 active 隧道。
- **`data` 字段**: `id`、`connectionId`、`requester`、`status`（`active`）、`startedAt`。协议与目标 `host`/`port` 不出现在 JSON 中（服务端内部字段），客户端勿期待响应里含拨号地址。
- **禁止**: 不得定时调用本接口判断隧道是否存活（多节点 LB 会误报 `404`）。隧道结束以 WebSocket EOF / CloseFrame 为准。

隧道结束以桌面关闭 WebSocket（EOF / CloseFrame）为准；服务端在桥接结束时回收本机会话。不提供 `DELETE` 拆隧道接口。

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
- **请求参数**: `limit` / `offset` 同 [列表分页约定](../_index.md#列表分页limit--offset)
- **响应说明**:
    - 状态码: `200 OK`
    - 分页 envelope：`data.items` / `data.total`


### GET /api/apps/:id

- **描述**: 获取应用详情
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/apps/:id/launch

- **描述**: 启动应用（异步任务，返回作业 ID）。**应用运行态不在 Apps API 暴露**：后续状态、控制与查询一律以 Scheduler 作业为准（响应 `Location` 指向 `/api/scheduler/jobs/{id}`）。
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `202 Accepted`
    - Header: `Location: /api/scheduler/jobs/{id}`

## Scheduler

### 接口列表

| 方法     | 路径                                                       | 用途          | 鉴权                               |
|---|---|---|---|
| `GET`  | `/api/scheduler/meta`                                    | 获取当前调度器元数据（推荐列、核心字段映射、状态动作集合） | 是（已登录）                           |
| `GET`  | `/api/scheduler/hosts`                                   | 列出 HPC 调度计算主机与状态 | 是（本用户）                           |
| `GET`  | `/api/scheduler/queues`                                  | 列出 HPC 调度队列（分区）作业数汇总 | 是（本用户）                           |
| `GET`  | `/api/scheduler/jobs`                                    | 列出全集群所有用户的作业 | 是（已登录）                           |
| `GET`  | `/api/scheduler/jobs/me`                                 | 列出当前用户作业      | 是（已登录）                           |
| `GET`  | `/api/scheduler/jobs/:id`                                | 获取任意作业详情    | 是（已登录）                           |
| `POST` | `/api/scheduler/jobs/:id/:op`                  | 控制单个作业（op ∈ kill/suspend/resume/toTop/toBottom/switch） | 是（本用户）                           |
| `PATCH` | `/api/scheduler/jobs/:id`                    | 修改作业名称      | 是（本用户）                           |
| `POST` | `/api/scheduler/jobs/batch-control`              | 批量控制作业（最多 100 个） | 是（本用户）                       |

> **可见性与操作权限**：作业列表与详情对所有已登录用户开放（全集群可见）。控制与修改类接口以**当前登录用户身份**执行调度器命令，调度器只允许操作本人作业。
>
> **参数校验规则**（违反时返回 `400 Bad Request`，响应体 `{"message": "<原因>"}`，不执行任何调度器命令）：
>
> - 作业 ID（路径 `:id`、`jobIds[]`）：仅十进制数字且为正整数，例如 `123`；`0`、`00`、带空白/符号、数组下标形式（`123[1]`、`123_1`）均拒绝。`message` 为 `job id must be a positive integer: <id>`（为空时 `job id is required`）。
> - 队列名（`targetQueue`）、作业名（`name`）：去除首尾空白后非空，且不以 `-` 开头。`message` 为 `<field> is required` 或 `<field> must not start with '-'`（`<field>` 取 `targetQueue` / `queue` / `name`）。

### GET /api/scheduler/meta

- **描述**: 获取当前 HPC 调度器的元数据（`fields.job` / `fields.host` / `fields.queue` 推荐列、作业核心字段映射 `keys`、以及各控制动作允许的作业原生状态集合 `actions`）。元数据随 `settings.scheduler.type` 变化（管理员切换调度器类型后即时生效），客户端可在会话内缓存一段时间并周期性或在登录后重新拉取。
- **鉴权**: 是（已登录）
- **请求参数**: 无
- **响应说明**:
    - 状态码: `200 OK`
    - 响应体结构：
        - `data.scheduler`: 底层调度器类型（`"lsf"` / `"slurm"` / `"openlava"`）
        - `data.fields`: 按资源分组的推荐展示列（有序原生键名）
            - `fields.job`：作业表；客户端可截取前 N 列，列选择器基于完整列表（`/jobs` 与 `/jobs/me` 共用）
            - `fields.host` / `fields.queue`：主机表、队列表（通常即该资源当前返回列全集）
        - `data.keys`: **定位用户/状态等核心列的权威指示**（`id` / `user` / `status` / `submitTime` → 原生键）。不要假定列下标固定，应用 `keys.user`、`keys.status` 取字段
        - `data.actions`: 平台 6 个控制动词（`kill` / `suspend` / `resume` / `toTop` / `toBottom` / `switch`）各自允许执行的调度器大写原生状态集合；未出现在集合中的状态不可执行该动作（终态天然排除）。路径 `:op` 与批量 `operation` 使用同一组拼写。
        - 状态比较：LSF 的 `STAT` 是字符串，直接与 `actions` 中的状态比较。Slurm 的 `job_state` 是字符串数组，**只取第一个非空元素**作为基础状态（其后是标志，如 `POWER_UP_NODE`），再与 `actions` 比较。例如 `["CONFIGURING", "POWER_UP_NODE"]` 取 `CONFIGURING`。`openlava` 的 `fields` / `keys` / `actions` 与 LSF 相同，仅 `scheduler` 为 `"openlava"`。
    - 响应示例（LSF）：
      ```json
      {
        "data": {
          "scheduler": "lsf",
          "fields": {
            "job": [
              "JOBID",
              "USER",
              "STAT",
              "QUEUE",
              "FROM_HOST",
              "EXEC_HOST",
              "JOB_NAME",
              "SUBMIT_TIME"
            ],
            "host": [
              "HOST_NAME",
              "STATUS",
              "JL_U",
              "MAX",
              "NJOBS",
              "RUN",
              "SSUSP",
              "USUSP",
              "RSV"
            ],
            "queue": [
              "QUEUE_NAME",
              "PRIORITY",
              "STATUS",
              "NJOBS",
              "PEND",
              "RUN",
              "SUSP",
              "MAX",
              "JL_H",
              "JL_P",
              "JL_U"
            ]
          },
          "keys": {
            "id": "JOBID",
            "user": "USER",
            "status": "STAT",
            "submitTime": "SUBMIT_TIME"
          },
          "actions": {
            "kill":    ["PEND", "RUN", "PROV", "PSUSP", "USUSP", "SSUSP", "WAIT"],
            "suspend": ["PEND", "RUN"],
            "resume":  ["PSUSP", "USUSP"],
            "toTop":    ["PEND"],
            "toBottom": ["PEND"],
            "switch":  ["PEND"]
          }
        }
      }
      ```
    - 响应示例（Slurm）：
      ```json
      {
        "data": {
          "scheduler": "slurm",
          "fields": {
            "job": [
              "job_id",
              "user_name",
              "job_state",
              "partition",
              "nodes",
              "name",
              "submit_time"
            ],
            "host": [
              "name",
              "hostname",
              "state",
              "cpus",
              "alloc_cpus",
              "idle_cpus",
              "real_memory",
              "alloc_memory",
              "cpu_load",
              "partitions",
              "reason",
              "weight"
            ],
            "queue": [
              "name",
              "priority_tier",
              "state",
              "total_nodes",
              "max_nodes",
              "max_cpus_per_node",
              "max_time",
              "nodes",
              "qos"
            ]
          },
          "keys": {
            "id": "job_id",
            "user": "user_name",
            "status": "job_state",
            "submitTime": "submit_time"
          },
          "actions": {
            "kill":    ["PENDING", "REQUEUED", "CONFIGURING", "RUNNING", "RESIZING", "SUSPENDED"],
            "suspend": ["RUNNING"],
            "resume":  ["SUSPENDED"],
            "toTop":    ["PENDING", "REQUEUED"],
            "toBottom": ["PENDING", "REQUEUED"],
            "switch":  ["PENDING", "REQUEUED"]
          }
        }
      }
      ```

### GET /api/scheduler/hosts

- **描述**: 获取 HPC 调度集群计算主机列表。记录为调度器**原生字段**（与作业列表同风格），不再收拢为固定摘要结构。`total` / `online` 为服务端按状态派生的汇总计数。建议展示列见 [`GET /api/scheduler/meta`](#get-apischedulermeta) 的 `fields.host`。
  - **LSF / OpenLava**：`bhosts -json -o "HOST_NAME STATUS JL_U MAX NJOBS RUN SSUSP USUSP RSV"`（文本头里的 `JL/U` 在 JSON 键中为 `JL_U`）
  - **Slurm**：`sinfo --json` 后按 `fields.host` 投影节点记录
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - `data.hosts[]`：原生键值记录数组；`data.total` / `data.online`：主机总数与在线数

### GET /api/scheduler/queues

- **描述**: 获取 HPC 调度队列（分区）列表。记录为调度器**原生字段**，不再从作业列表聚合。与 Overview `scheduler.queues` 同源。建议展示列见 `meta.fields.queue`。
  - **LSF / OpenLava**：`bqueues -json -o "QUEUE_NAME PRIO STATUS MAX JL_U JL_P JL_H NJOBS PEND RUN SUSP"`（`-o` 写 `PRIO`，JSON 键为 `PRIORITY`；`JL/U`→`JL_U` 等同理）
  - **Slurm**：`scontrol show partition --json` 后按 `fields.queue` 投影；分区 JSON **不含** LSF 式 `PEND`/`RUN` 作业计数（见作业表）
- **鉴权**: 是（本用户）
- **响应说明**:
    - 状态码: `200 OK`
    - `data.queues[]`：原生键值记录数组

### GET /api/scheduler/jobs

- **描述**: 列出**全集群、所有用户**的作业（等价于调度器侧全量列表），对所有已登录用户开放。个人视图用 `GET /api/scheduler/jobs/me`；按指定用户或 `queue` / `node` 过滤使用管理员接口 `GET /api/admin/system/scheduler/jobs`。
- **鉴权**: 是（已登录）
- **请求参数**: 无

- **响应说明**: 与 `GET /api/scheduler/jobs/me` 相同（见下）。

### GET /api/scheduler/jobs/me

- **描述**: 列出**当前登录用户**的作业。全集群作业见 `GET /api/scheduler/jobs`；管理员按指定用户过滤见 `GET /api/admin/system/scheduler/jobs?user=`。
- **鉴权**: 是（已登录）
- **请求参数**: 无

- **响应说明**（`/jobs` 与 `/jobs/me` 共用）:
    - 状态码: `200 OK`
    - 响应体 `data.scheduler`：当前调度器类型（与 [`GET /api/scheduler/meta`](#get-apischedulermeta) 的 `scheduler` 相同，即 `settings.scheduler.type`）；空列表也带此字段，供客户端核对是否复用已缓存的 meta
    - 响应体 `data.jobs`：作业记录数组，键名同为调度器原生键名；**记录字段多于建议显示列**，为调度器原生字段的超集（注：建议展示列 `fields` 已移至 [`GET /api/scheduler/meta`](#get-apischedulermeta) 提供）
        - **LSF**：字段由 `bjobs -json -o "<fields>"` 显式声明。建议显示列之外，还额外请求运行/资源类字段：`RUN_TIME`、`START_TIME`、`FINISH_TIME`、`PEND_TIME`、`CPU_USED`、`MEM`、`SWAP`、`PIDS`、`SLOTS`、`PROJ_NAME`、`JOB_PRIORITY`、`APPLICATION`、`EXEC_HOME`、`NEXEC_HOST`、`USER_GROUP`、`JOB_GROUP`、`CMD`、`EXEC_CWD`、`INPUT_FILE`、`OUTPUT_FILE`、`ERROR_FILE`、`OUTPUT_DIR`、`EXIT_CODE`、`EXIT_REASON`
        - **Slurm**：`squeue --json` 本身**全量转储**（137 字段，`-o`/`--format` 被 Slurm 忽略）；Portal 在**列表路径已裁剪**为固定子集（字段名取自 Slurm 官方 `data_parser` 作业定义，共 69 键）：
            - 标识/归属：`job_id`、`name`、`user_name`、`user_id`、`group_name`、`group_id`、`account`、`qos`、`wckey`
            - 状态/调度：`job_state`、`state_reason`、`state_description`、`partition`、`cluster`、`priority`、`nice`、`requeue`、`restart_cnt`、`resv_name`、`failed_node`、`dependency`、`comment`、`system_comment`、`hold`、`mcs_label`
            - 资源/放置：`nodes`、`node_count`、`cpus`、`cpus_per_task`、`batch_host`、`tres_alloc_str`、`tres_req_str`、`memory_per_node`、`memory_per_cpu`、`cpus_per_tres`、`memory_per_tres`、`minimum_cpus_per_node`、`minimum_tmp_disk_per_node`、`features`、`gres_detail`、`network`、`job_size_str`、`excluded_nodes`、`required_nodes`、`scheduled_nodes`
            - 时间：`submit_time`、`start_time`、`end_time`、`eligible_time`、`suspend_time`、`preempt_time`、`pre_sus_time`、`time_limit`
            - 输入输出/退出：`command`、`submit_line`、`current_working_directory`、`standard_input`、`standard_output`、`standard_error`、`exit_code`、`derived_exit_code`
            - 数组/异构：`array_job_id`、`array_task_id`、`array_task_string`、`array_max_tasks`、`het_job_id`、`step_id`
            - 许可：`licenses`、`licenses_allocated`
            - 未列出的键（如超大嵌套对象 `job_resources`、`steps`，以及 `tres_bind`/`tres_freq`/`tres_per_*` 等低价值配置回显）在列表中**不返回**；缺失的键（旧版本 Slurm 无该字段，如 `submit_line`/`step_id`/`hold`/`scheduled_nodes`/`network`/`job_size_str`/`licenses_allocated`）直接跳过
        - **详情接口 `GET /api/scheduler/jobs/:id` 不做裁剪**，Slurm 详情返回全量字段
    - 数据形态差异：Slurm 的 `job_state` 为字符串**数组**（如 `["RUNNING"]`，标志排在基础状态之后），LSF 的 `STAT` 为字符串。与 `actions` 比较的规则见 [`GET /api/scheduler/meta`](#get-apischedulermeta)

### GET /api/scheduler/jobs/:id

- **描述**: 获取指定作业的详细信息（任意用户的作业均可查询）
- **鉴权**: 是（已登录）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误状态码: `400 Bad Request`（作业 ID 非正整数）· `404 Not Found`（作业不存在）· `500 Internal Server Error`（查询失败）


### POST /api/scheduler/jobs/:id/:op

- **描述**: 控制单个作业状态（停止、挂起、恢复等）。`op` 为路径白名单动词：`kill` / `suspend` / `resume` / `toTop` / `toBottom` / `switch`，
  非法动词返回 `400 Bad Request`
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名           | 位置   | 类型     | 必选 | 默认值 | 说明                                                                   |
|---------------|------|--------|:--:|:---:|------------------------------------------------------------------------|
| `id`          | Path | string | 是  |  -  | 作业 ID（正整数）                                                            |
| `op`          | Path | string | 是  |  -  | 操作动词：`kill` / `suspend` / `resume` / `toTop` / `toBottom` / `switch`       |
| `targetQueue` | Body | string | 否  |  -  | `switch` 操作时必填：目标队列（非空、不以 `-` 开头）                                  |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400` 非法操作动词、作业 ID 非正整数或 `targetQueue` 不合法 · `409 Conflict` 该作业控制失败（响应体 `message` 携带原因）


### PATCH /api/scheduler/jobs/:id

- **描述**: 修改作业名称（切换队列使用 `POST /api/scheduler/jobs/:id/switch`）
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名    | 位置   | 类型     | 必选 | 默认值 | 说明                      |
|--------|------|--------|:--:|:---:|-------------------------|
| `id`   | Path | string | 是  |  -  | 作业 ID（正整数）              |
| `name` | Body | string | 是  |  -  | 新作业名称（非空、不以 `-` 开头） |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400 Bad Request`（作业 ID 非正整数、未提供 `name` 或 `name` 不合法）


### POST /api/scheduler/jobs/batch-control

- **描述**: 批量控制多个作业
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名           | 位置   | 类型       | 必选 | 默认值 | 说明                |
|---------------|------|----------|:--:|:---:|-------------------|
| `jobIds`      | Body | string[] | 是  |  -  | 作业 ID 列表（均为正整数），最多 100 个 |
| `operation`   | Body | string   | 是  |  -  | 与路径 `:op` 相同：`kill` / `suspend` / `resume` / `toTop` / `toBottom` / `switch` |
| `targetQueue` | Body | string   | 否  |  -  | 目标队列（`switch` 操作时必填，非空、不以 `-` 开头） |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400 Bad Request`（列表为空、超过 100 个作业、任一作业 ID 非正整数或 `targetQueue` 不合法；整批拒绝，不执行任何作业）· `409 Conflict`（部分作业控制失败，`data.failures[]` 为 `{jobId, message}`）

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
- **请求参数**: `limit` / `offset` 同 [列表分页约定](../_index.md#列表分页limit--offset)
- **响应说明**:
    - 状态码: `200 OK`
    - 分页 envelope：`data.items` / `data.total`


### GET /api/licenses/:id

- **描述**: 获取指定许可证服务的详细信息（叠加内存快照状态）
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在或未启用 `enabled=false`）


### GET /api/licenses/:id/features

- **描述**: 获取指定许可证服务支持的功能列表与用量明细。查询结果经进程内 TTLCache 节流（TTL 由 `settings.license.featuresCacheTTL` 动态控制，默认 300s，防击穿保护）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在或未启用）


### GET /api/licenses/:id/status

- **描述**: 获取指定许可证服务的实时运行状态。查询结果经进程内 TTLCache 节流（TTL 由 `settings.license.statusCacheTTL` 动态控制，默认 300s）。状态枚举取值：`up`（服务正常）、`down`（服务离线或异常）、`unknown`（未知）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在或未启用）


## Files

桌面客户端对下载 POST 须流式写盘、禁止内存整包缓冲；见 [协议代理 · 文件下载](/docs/architecture/remote-access/proxy#文件下载)。

> **文件功能总开关**：所有 `/api/files/*` 接口统一受 `file.enabled` 门控。当管理员在平台设置中禁用文件功能时（`file.enabled = false`），所有文件端点将拒绝访问并返回 `403 Forbidden`（`{"error": "file service disabled"}`）。此外，具体操作分别受到 `downloadEnabled`、`uploadEnabled`、`requiresApproval` 审批策略、用户个人权限以及底层文件系统 POSIX 权限的共同约束。

### 接口列表

| 方法       | 路径                                                               | 用途                | 鉴权                             |
|---|---|---|---|
| `POST`   | `/api/files/download-requests`                                   | 提交下载审批申请          | 是（本用户）                         |
| `GET`    | `/api/files/download-requests`                                   | 查看自己的下载申请         | 是（本用户）                         |
| `GET`    | `/api/files/download-requests/:id`                               | 查看下载申请详情          | 是（本用户）                         |
| `POST`   | `/api/files/downloads`                                             | 直接下载（流式）          | 是（本用户，文件传输限流）                |
| `POST`   | `/api/files/download-requests/:id/content`                       | 审批通过后下载（流式）       | 是（本用户，文件传输限流）                |
| `GET`    | `/api/files/locations`                                           | 列出文件位置            | 是（本用户）                         |
| `GET`    | `/api/files/locations/:name/contents`                            | 列出目录内容            | 是（本用户）                         |
| `POST`   | `/api/files/locations/:name/contents`                            | 创建目录              | 是（本用户）                         |
| `GET`    | `/api/files/locations/:name/contents/*`                          | 列出子目录内容           | 是（本用户）                         |
| `PUT`    | `/api/files/locations/:name/contents/*`                          | 上传文件（流式）          | 是（已登录用户，文件传输限流）                |
| `POST`   | `/api/files/locations/:name/contents/batch-delete`       | 批量永久删除（不经回收站）     | 是（本用户）                         |
| `POST`   | `/api/files/trash/items`        | 批量移至回收站           | 是（本用户）                         |
| `POST`   | `/api/files/locations/:name/contents/rename`             | 重命名文件/目录          | 是（本用户）                         |
| `GET`    | `/api/files/trash/contents`                                      | 列出回收站内容           | 是（本用户）                         |
| `GET`    | `/api/files/trash/contents/*`                                    | 列出回收站子目录内容        | 是（本用户）                         |
| `DELETE` | `/api/files/trash/items`                                         | 清空回收站             | 是（本用户）                         |
| `DELETE` | `/api/files/trash/items/:trashName`                              | 删除回收站中单个项目        | 是（本用户）                         |
| `POST`   | `/api/files/trash/items/restore`                         | 从回收站还原到原路径         | 是（本用户）                         |
| `POST`   | `/api/files/trash/items/batch-delete`                    | 批量永久删除回收站项目       | 是（本用户）                         |

### POST /api/files/download-requests

- **描述**: 提交下载审批申请（适用于需要审批的位置）。申请持久化为 `download_requests` 记录；真正拉文件字节走节点 `FILE_DOWNLOAD` 流式 Action，与审批表分离。
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明   |
|------------|------|--------|:--:|:---:|------|
| `location` | Body | string | 是  |  -  | 文件位置 |
| `path`     | Body | string | 是  |  -  | 文件路径 |

- **响应说明**:
    - 状态码: `202 Accepted`；`data` 含 `downloadRequestId`、`status`
    - `409 Conflict`：当前策略不需要审批（`downloadAccess=allow`）等

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


### POST /api/files/downloads

- **描述**: 直接下载（`downloadAccess=allow`）；响应体为文件二进制流。客户端应使用流式写入磁盘，勿整包缓冲。
- **鉴权**: 是（本用户，文件传输限流）
- **请求 Body**:

| 字段名       | 类型     | 必选 | 说明              |
|-----------|--------|:--:|-----------------|
| `location` | string | 是  | 文件位置名（见 `GET /api/files/locations`） |
| `path`     | string | 是  | 位置内相对路径（不以 `/` 开头） |

- **响应说明**: `200 OK`（`Content-Type: application/octet-stream`）；开流前指纹不一致等业务错误见 `409 Conflict` 等

### POST /api/files/download-requests/:id/content

- **描述**: 已审批申请的一次性流式下载
- **鉴权**: 是（本用户，文件传输限流）
- **响应**: `200 OK`（文件二进制流）


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
    - `data.files` 为目录条目，按文件名排序。条数窗口由文件设置 `listMaxEntries` 决定（50–999，默认 999）。实际最多再返回 100 条，设置取 999 时一次最多 1099 条。客户端在这一批结果里自行分页。
    - 读满该上限后目录仍有后续条目时 `data.truncated` 为 `true`，只返回已经读到的条目。字段省略表示这次就是整个目录。这些条目是扫描过程中先拿到的一批，不是按字母序截取的前 N 条。不要用 `files` 的长度判断是否截断。
    - 错误状态码: `413 Request Entity Too Large`（文件动作结果超过 32 MiB，响应体 `{"message": "file action result exceeds 32 MiB limit"}`）


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

- **描述**: 批量**永久删除**文件或目录（直接删除，不经过回收站）。移入回收站请使用 `POST /api/files/trash/items`。
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
    - `data.files` 单次条数与目录列表相同，由 `listMaxEntries` 决定（50–999，默认 999，实际最多再多 100 条）。读满后仍有后续条目时 `data.truncated` 为 `true`；字段省略表示回收站本次列表完整。不要用 `files` 长度判断是否截断。


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


### POST /api/files/trash/items/restore

- **描述**: 将回收站中的项目还原到 `.trashinfo` 记录的原路径
- **鉴权**: 是（本用户）
- **请求参数**:

| 参数名          | 位置   | 类型       | 必选 | 默认值 | 说明           |
|--------------|------|----------|:--:|:---:|--------------|
| `trashNames` | Body | string[] | 是  |  -  | 回收站项目名称列表（`trashName`） |

- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `404` 项目不存在 · `409` 原路径已存在


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

- **语义**: 节点记录必须已存在。有效且未过期的 token 即授权首次签发或重新签发。成功后写入新的证书指纹、主题与到期时间，清空 token；运行状态列保持为空直至 Agent 首次连接。节点证书的 Organization 固定为 `Design Cloud Engine`，不是签发方节点 ID。
- **响应说明**:
    - 状态码: `200 OK`
    - `data` 字段：`nodeId`、`serverAddr`、`certPem`、`keyPem`、`caPem`、`certExpiresAt`
    - `400 Bad Request`：请求体无法解析
    - `401 Unauthorized`：token 不匹配
    - `404 Not Found`：节点不存在
    - `409 Conflict`：token 缺失或已过期（需管理员重新签发后再领取）

## Tickets

工单模块：工单、看板、评论与变更记录。所有登录用户可查看全部未删除的工单、评论、变更记录与统计。

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/tickets` | 工单列表（过滤、搜索、排序、分页） | 是（已登录用户） |
| `POST` | `/api/tickets` | 创建工单 | 是（已登录用户） |
| `GET` | `/api/tickets/:id` | 工单详情 | 是（已登录用户） |
| `PATCH` | `/api/tickets/:id` | 部分更新（乐观锁） | 是（报告人、指派人、工单管理员） |
| `DELETE` | `/api/tickets/:id` | 软删除 | 是（报告人、工单管理员） |
| `POST` | `/api/tickets/batch-delete` | 批量软删除（全有或全无） | 是（报告人、工单管理员） |
| `POST` | `/api/tickets/:id/move` | 看板移动（乐观锁） | 是（报告人、指派人、工单管理员） |
| `GET` | `/api/tickets/:id/comments` | 评论列表 | 是（已登录用户） |
| `POST` | `/api/tickets/:id/comments` | 发表评论 | 是（已登录用户） |
| `PATCH` | `/api/tickets/:id/comments/:commentId` | 编辑评论 | 是（评论作者） |
| `DELETE` | `/api/tickets/:id/comments/:commentId` | 删除评论 | 是（评论作者、工单管理员） |
| `GET` | `/api/tickets/:id/events` | 变更记录 | 是（已登录用户） |
| `GET` | `/api/tickets/assignees` | 指派人搜索（`q` 可选；省略/不足 2 字符返回热点指派人） | 是（已登录用户） |
| `GET` | `/api/tickets/projects` | 未归档项目列表 | 是（已登录用户） |
| `GET` | `/api/tickets/summary` | 工单统计 | 是（已登录用户） |

### 约定

- **工单管理员**：`superAdmin`、`systemAdmin`，或持有 `tickets` 管理范围的普通用户（与 `/api/admin/system/tickets` 的访问条件一致）。
- **权限**：

| 操作 | 允许 |
|---|---|
| 查看工单、评论、变更记录、统计 | 任意已登录用户 |
| 创建工单、发表评论 | 任意已登录用户 |
| 编辑字段、看板移动 | 报告人、指派人、工单管理员 |
| 修改或取消指派 | 报告人、工单管理员 |
| 删除工单 | 报告人、工单管理员 |
| 编辑评论 | 评论作者 |
| 删除评论 | 评论作者、工单管理员 |

- **已删除工单**：在本组接口中视为不存在（`404`），只能通过管理员接口查看、恢复或永久删除。
- **枚举**（请求体、查询参数与响应一致，均为字符串，大小写不敏感）：
    - `type`：`bug`、`feature`、`task`
    - `priority`：`low`、`medium`、`high`、`critical`
    - `status`：`todo`、`inProgress`、`inReview`、`done`
- **请求体**：严格 JSON，含未知字段或格式错误返回 `422 invalid request body`。
- **乐观锁**：每张工单有 `version`，每次修改加 1。`PATCH` 与 `move` 必须在请求体中携带当前 `version`；不一致返回 `409 ticket was modified`，客户端应重新获取后再提交。
- **错误码**：

| 状态码 | 消息 |
|---|---|
| `422` | 参数或字段非法，消息为具体原因，如 `title is required`、`invalid status`、`invalid assignee`、`invalid attachment`、`version is required`、`invalid limit`、`invalid request body` |
| `403` | `permission denied` |
| `404` | `ticket not found` / `project not found` / `comment not found` |
| `409` | `ticket was modified` / `project is archived` |

### 工单对象

```json
{
  "id": 12,
  "project": { "id": 1, "name": "平台" },
  "type": "bug",
  "title": "登录页错位",
  "description": "小屏幕下按钮错位",
  "reporter": { "id": 3, "username": "alice" },
  "assignee": { "id": 5, "username": "bob" },
  "priority": "high",
  "status": "inProgress",
  "dueDate": "2026-10-20T00:00:00+08:00",
  "acceptanceCriteria": {
    "description": "各分辨率下显示正确",
    "checklist": [{ "text": "移动端适配", "done": false }]
  },
  "attachments": [
    { "location": "home", "path": "docs/shot.png", "name": "shot.png", "size": 1024, "mimeType": "image/png" }
  ],
  "sortOrder": 2048,
  "version": 3,
  "completedAt": null,
  "createdAt": "2026-10-13T09:00:00+08:00",
  "updatedAt": "2026-10-13T10:00:00+08:00"
}
```

- `assignee`、`dueDate`、`acceptanceCriteria`、`completedAt` 可为 `null`；`attachments` 无附件时为 `[]`。
- `sortOrder` 为看板列（项目 + 状态）内的位置，由服务端计算，只读。
- `completedAt`：进入 `done` 时写入，离开 `done` 时清空。
- **附件**是对文件服务中文件的引用，不保存文件内容，也不授予访问权：
    - `location` 为文件位置名（见 `GET /api/files/locations`），`path` 为位置内的相对路径（不能以 `/` 开头、不能越出位置根目录）；写入时以写入者身份解析，位置不存在或不可用、文件服务未启用时返回 `422 invalid attachment`。
    - `name` 1～255 字符，缺省取路径最后一段；`size` ≥ 0；`mimeType` 最长 127 字符；最多 20 个。
    - 查看者下载附件走文件接口，按查看者自己的文件权限判断。
- **验收标准**：`description` 最长 2000 字符；`checklist` 最多 100 项，每项 `text` 1～500 字符。

分页响应统一为：

```json
{ "data": { "items": [], "totalCount": 0, "limit": 500, "offset": 0, "hasMore": false } }
```

### GET /api/tickets

- **描述**: 工单列表。
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数 | 位置 | 说明 |
|---|---|---|
| `projectId` | Query | 项目 ID |
| `status` | Query | 状态，可多值：逗号分隔或重复参数，如 `status=todo,inProgress` |
| `type` | Query | 类型，可多值 |
| `priority` | Query | 优先级，可多值 |
| `assigneeId` | Query | 指派人 ID；`none` 表示未指派 |
| `reporterId` | Query | 报告人 ID |
| `q` | Query | 标题关键字，不区分大小写包含匹配，`%`、`_` 按字面匹配；最长 256 字符 |
| `sort` | Query | `sortOrder`（默认）、`createdAt`、`updatedAt`、`dueDate`、`priority` |
| `order` | Query | `asc` / `desc`；`sortOrder` 默认 `asc`，其余默认 `desc` |
| `limit` | Query | 1～2000，默认 500 |
| `offset` | Query | 0～10000，默认 0；**续页**请用 keyset（见下），与游标同时出现时 offset **被忽略** |
| `afterSort` | Query | keyset 续页：上一页 `nextAfterSort`（或最后一条的 `sortOrder`）；须与 `afterId` 成对；**仅 `sort=sortOrder`（默认）** |
| `afterId` | Query | keyset 续页：上一页 `nextAfterId`（或最后一条的 `id`）；须与 `afterSort` 成对 |

- **排序**：同值时按 `id` **同向**排序兜底，保证跨页稳定；按 `dueDate` 排序时没有截止日期的排在最后。
- **Keyset 续页（推荐）**：`sortOrder` 排序下用 `afterSort` + `afterId` 向后翻页；响应含 `nextAfterSort` / `nextAfterId` 时可原样带入下一请求。`sortOrder` 且 `offset ≥ 2000` 且无游标时返回 `422 invalid offset`。带齐 keyset 游标时**不重算** `totalCount`，固定返回 **`-1`**（请用首页/`offset` 请求的 `totalCount`，续页看 `hasMore`）。
- **响应说明**: `200 OK`，`data` 为工单分页结构（`items`、`totalCount`、`limit`、`offset`、`hasMore` 等）。
- **错误**: 参数非法返回 `422`，消息为 `invalid <参数名>`。

### POST /api/tickets

- **描述**: 创建工单；报告人为当前用户，新工单排在所在列末尾。
- **鉴权**: 是（已登录用户）
- **请求体**:

| 字段 | 类型 | 必选 | 默认值 | 说明 |
|---|---|:--:|:--:|---|
| `projectId` | integer | 是 | - | 项目须存在、未删除、未归档 |
| `title` | string | 是 | - | 去首尾空白后 1～256 字符 |
| `type` | string | 否 | `task` | 见枚举 |
| `priority` | string | 否 | `medium` | 见枚举 |
| `status` | string | 否 | `todo` | 见枚举 |
| `description` | string | 否 | `""` | 最长 20000 字符 |
| `assigneeId` | integer \| null | 否 | `null` | 须为状态正常、无管理范围的普通用户（与 `/api/tickets/assignees` 同一规则） |
| `dueDate` | string \| null | 否 | `null` | RFC3339 |
| `acceptanceCriteria` | object \| null | 否 | `null` | 见工单对象 |
| `attachments` | object[] | 否 | `[]` | 见工单对象 |

- **响应说明**: `201 Created`，`data` 为工单对象。
- **错误**: `422`（字段非法）、`404 project not found`、`409 project is archived`。

### GET /api/tickets/:id

- **描述**: 工单详情。
- **鉴权**: 是（已登录用户）
- **响应说明**: `200 OK`，`data` 为工单对象；不存在或已删除返回 `404 ticket not found`。

### PATCH /api/tickets/:id

- **描述**: 部分更新。只传需要修改的字段；与当前值相同的字段不产生变更，全部相同时不增加版本号。
- **鉴权**: 是（报告人、指派人、工单管理员；修改 `assigneeId` 仅限报告人与工单管理员）
- **请求体**:

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | integer | **必填**，当前版本号 |
| `title` | string | 同创建 |
| `description` | string | 同创建 |
| `type` / `priority` / `status` | string | 见枚举；`status` 变化时工单移到新列末尾 |
| `assigneeId` | integer \| null | `null` 表示取消指派 |
| `dueDate` | string \| null | `null` 表示清空 |
| `acceptanceCriteria` | object \| null | 整体替换，`null` 表示清空 |
| `attachments` | object[] | 整体替换，`[]` 表示清空 |

- 看板内的位置用 `POST /api/tickets/:id/move` 调整；项目与报告人不可修改。
- **响应说明**: `200 OK`，`data` 为更新后的工单对象。
- **错误**: `422`、`403 permission denied`、`404 ticket not found`、`409 ticket was modified`。

### POST /api/tickets/:id/move

- **描述**: 看板移动，服务端计算排序位置。
- **鉴权**: 是（报告人、指派人、工单管理员）
- **请求体**:

| 字段 | 类型 | 必选 | 说明 |
|---|---|:--:|---|
| `version` | integer | 是 | 当前版本号 |
| `status` | string | 否 | 目标列状态，缺省为当前状态（同列内移动） |
| `afterId` | integer | 否 | 放在该工单之后 |
| `beforeId` | integer | 否 | 放在该工单之前 |

- `afterId` 与 `beforeId` 都给时两者须在目标列中相邻（`afterId` 紧挨在 `beforeId` 之前）；都不给时放到目标列末尾。参照工单须在同一项目、目标列中且未删除，否则返回 `422 invalid position`。
- 相邻位置的间距不足时，服务端在同一事务内把整列重新编号，客户端无需处理。
- 进入 `done` 时写入 `completedAt`，离开时清空。
- **响应说明**: `200 OK`，`data` 为移动后的工单对象。
- **错误**: `422`、`403`、`404`、`409 ticket was modified`。

### DELETE /api/tickets/:id

- **描述**: 软删除工单。
- **鉴权**: 是（报告人、工单管理员）
- **响应说明**: `204 No Content`。
- **错误**: `403 permission denied`、`404 ticket not found`（不存在或已删除）。

### POST /api/tickets/batch-delete

- **描述**: 批量软删除，**全有或全无**：先检查全部 ID，任一不满足条件则一张都不删除。
- **鉴权**: 是（报告人、工单管理员）
- **请求体**: `{"ids": [1, 2, 3]}`，1～100 个，接受数字或数字字符串，重复 ID 自动合并。
- **响应说明**: `204 No Content`。
- **错误**:
    - `422 ids must contain 1 to 100 ticket ids`
    - `404 tickets not found`，`details.notFound` 列出不存在或已删除的 ID：`{"message": "tickets not found", "details": {"notFound": [7, 9]}}`
    - `403 permission denied`，`details.forbidden` 列出无权删除的 ID（只在全部 ID 都存在时检查）
    - 只传一个 ID 时与 `DELETE /api/tickets/:id` 相同，不带 `details`。

### GET /api/tickets/:id/comments

- **描述**: 评论列表，按发表顺序。
- **鉴权**: 是（已登录用户）
- **请求参数**: `limit` 1～2000（默认 500）、`offset` 0～10000。
- **响应说明**: `200 OK`，分页结构，元素为评论对象：

```json
{ "id": 4, "ticketId": 12, "author": { "id": 3, "username": "alice" }, "body": "已复现", "createdAt": "...", "updatedAt": "..." }
```

- **错误**: `404 ticket not found`。

### POST /api/tickets/:id/comments

- **描述**: 发表评论，并通知报告人与指派人。
- **鉴权**: 是（已登录用户）
- **请求体**: `{"body": "..."}`，去首尾空白后 1～10000 字符。
- **响应说明**: `201 Created`，`data` 为评论对象。
- **错误**: `422 body is required` / `422 body is too long`、`404 ticket not found`。

### PATCH /api/tickets/:id/comments/:commentId

- **描述**: 编辑评论正文。
- **鉴权**: 是（评论作者）
- **请求体**: `{"body": "..."}`，规则同发表。
- **响应说明**: `200 OK`，`data` 为评论对象。
- **错误**: `422`、`403 permission denied`、`404 ticket not found` / `404 comment not found`。

### DELETE /api/tickets/:id/comments/:commentId

- **描述**: 删除评论（不可恢复）。
- **鉴权**: 是（评论作者、工单管理员）
- **响应说明**: `204 No Content`。
- **错误**: `403`、`404`。

### GET /api/tickets/:id/events

- **描述**: 工单变更记录，按发生顺序。
- **鉴权**: 是（已登录用户）
- **请求参数**: `limit` 1～2000（默认 500）、`offset` 0～10000。
- **响应说明**: `200 OK`，分页结构，元素为：

```json
{ "id": 9, "actor": { "id": 3, "username": "alice" }, "action": "updated", "field": "status", "from": "todo", "to": "inProgress", "createdAt": "..." }
```

| `action` | 说明 |
|---|---|
| `created` | 创建 |
| `updated` | 字段修改，每个字段一条；`field` 为 `title`、`description`、`type`、`priority`、`status`、`assignee`、`dueDate`、`acceptanceCriteria`、`attachments` |
| `moved` | 看板移动；跨列时 `field` 为 `status` 并带前后值 |
| `commented` | 发表评论，`to` 为评论 ID |
| `deleted` / `restored` | 删除 / 恢复 |

- `from` / `to`：`title`、`type`、`priority`、`status` 为对应值，`assignee` 为用户名（未指派为 `null`），`dueDate` 为 RFC3339；`description`、`acceptanceCriteria`、`attachments` 只记录字段名，`from` / `to` 为 `null`。`field` 无值时省略。
- **错误**: `404 ticket not found`。

### GET /api/tickets/assignees

- **描述**: 按用户名搜索工单指派人，或在不输入 / 关键字不足 2 字符时返回近期热点指派人（最多 10 条，服务端 cache）。不区分大小写的包含匹配，以 `q` 开头的用户名排前，其余按用户名升序；只返回状态正常（`active`）的普通角色用户（不含管理员角色及持有管理范围的用户）；搜索路径最多 20 条，不分页、不返回总数。`q` 中的 `%`、`_` 按字面匹配。
- **鉴权**: 是（已登录用户）
- **限流**: 全局 IP 限流与会话限流之外，本接口另按用户限流（5 次/秒，突发 5）
- **请求参数**:

| 参数 | 位置 | 必填 | 说明 |
|---|---|---|---|
| `q` | query | 否 | 用户名关键字；省略或去首尾空白后不足 2 字符时返回热点指派人（最多 10 条） |

- **响应说明**: `200 OK`；无匹配时 `data` 为 `[]`。

```json
{
  "data": [
    { "id": 12, "username": "alice" },
    { "id": 31, "username": "malice" }
  ]
}
```

- **错误**:
    - `429 Too Many Requests`：`too many requests`

### GET /api/tickets/projects

- **描述**: 未删除、未归档的项目列表，按 `id` 升序。
- **鉴权**: 是（已登录用户）
- **请求参数**: `limit` 1～2000（默认 500）、`offset` 0～10000。
- **响应说明**: `200 OK`，分页结构，元素为 `{id, name, description, isArchived, createdAt, updatedAt}`。

### GET /api/tickets/summary

- **描述**: 未删除工单的统计。
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数 | 说明 |
|---|---|
| `projectId` | 可选，缺省统计全部项目 |
| `days` | 趋势天数 1～90，默认 7；最后一天为今天 |
| `tz` | IANA 时区名，如 `Asia/Shanghai`；缺省为服务器本地时区。日界按该时区切分 |

- **响应说明**: `200 OK`

```json
{
  "data": {
    "timezone": "Asia/Shanghai",
    "totalTasks": 10,
    "completedTasks": 4,
    "overallCompletionRate": 40,
    "byStatus": { "todo": 3, "inProgress": 2, "inReview": 1, "done": 4 },
    "trends": [{ "date": "2026-10-07", "created": 2, "completed": 1 }]
  }
}
```

- `trends[].created`：当天新建且未删除的工单数；`trends[].completed`：当天进入 `done` 且仍为 `done` 的工单数。
- **错误**: `422 invalid projectId` / `invalid days` / `invalid tz`。

## Settings

### 接口列表

| 方法    | 路径                                          | 用途                              | 鉴权                             |
|---|---|---|---|
| `GET` | `/api/settings/platform`                    | 获取平台设置                          | 是（已登录用户）                       |

### GET /api/settings/platform

- **描述**: 获取 **Settings 域 `platform` 的展示用子集**（平台名称、描述、技术支持链接等）。路径中的 `platform` 指配置域名称，**不是** Guest 免登录接口；未登录请使用公开端点（见 [API 总览](./_index.md#公开端点无需认证)）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`

## Overview

### 接口列表

| 方法    | 路径                          | 用途            | 鉴权       |
|---|---|---|---|
| `GET` | `/api/overview/me`         | 个人概览（`scheduler.jobs` 为当前用户） | 是（已登录用户，常规会话鉴权） |
| `GET` | `/api/overview/me/stream` | 个人概览实时流（SSE，当前用户作业统计） | 是（已登录用户，常规会话鉴权） |
| `GET` | `/api/overview/cluster`    | 集群概览（`scheduler.jobs` 为全集群） | 是（已登录用户，常规会话鉴权；受 `overview.allowUserCluster` 约束） |
| `GET` | `/api/overview/cluster/stream` | 集群概览实时流（SSE，与大屏一致） | 是（已登录用户；或大屏访客 Token） |

> **路径约定**：裸 `GET /api/overview` **不提供数据**（无路由 / 404）。JSON 快照与 SSE 流分属不同路径，SSE 路径统一以 `/stream` 结尾，不受常规 API 60s 请求超时限制。

> **大屏开关（Settings `platform.website.overviewEnabled`）**：只作用于网页大屏：关闭时嵌入页 `/overview` 与携带 `PublicOverviewToken` 的访客请求返回 `404 Not Found`；已登录用户访问本组接口不受该开关影响。
>
> **普通用户集群可见性（Settings `overview.allowUserCluster`，默认 `true`）**：为 `false` 时，普通用户（`RoleNormal` 且无 AdminScopes）访问 `/api/overview/cluster` 与 `/api/overview/cluster/stream` 返回 `403 Forbidden`（`cluster overview is disabled for ordinary users`）。管理员与大屏访客 Token 不受此开关约束。
>
> **大屏访客模式**：在 `platform.website.overviewEnabled` 为 `true` 时，持有 `PublicOverviewToken`（`__dc_public_overview_guest_token__`）的请求可免登录只读访问 `GET /api/overview/cluster/stream`（网页大屏唯一使用的数据接口）。Token 优先取 `Authorization: Bearer <token>` 请求头，无请求头时取 `?token=<token>`（适配浏览器原生 EventSource）。若大屏已关闭，访客 Token 请求返回 `404 Not Found`（`overview guest access is disabled`）。
>
> **常规会话鉴权**：`GET /api/overview/me`、`/me/stream`、`/cluster` 与其他登录接口使用同一会话鉴权：仅接受 `Authorization: Bearer <token>` 请求头；访客 Token 或无效 Token 返回 `401 Unauthorized`（`invalid session token`）；客户端 IP 已被封禁时返回 `403 Forbidden`（不再累计失败）；需改密受限会话返回 `428 Precondition Required`（`password change required`）。有效会话跳过 IP 封禁检查。

### GET /api/overview/cluster

- **描述**: 获取集群聚合状态概览（节点、作业、存储等；`scheduler.jobs` 为全集群）
- **鉴权**: 是（已登录用户，仅 `Authorization: Bearer <token>` 请求头；不受大屏开关影响；普通用户受 `overview.allowUserCluster` 约束）
- **请求参数**: 无（实时流见 `GET /api/overview/cluster/stream`）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误状态码: `401 Unauthorized`（访客 Token 或无效 Token，`invalid session token`）· `403 Forbidden`（普通用户且 `allowUserCluster=false`）· `428 Precondition Required`（需改密受限会话，`password change required`）
    - 字段说明（资源块）:
        - `cpu.utilization`、`gpu.cards.utilization`、`storage.summary.utilization`：在线资源平均或占用比例，**0~1**（如 `0.485` 表示约 48.5%）。
        - `load.1m` / `5m` / `15m`：各在线节点 `loadavg` **按节点求和**的原始值（与单核百分比无关；核数越多、负载越高时数值可远大于 1）。
    - 响应示例:
      ```json
      {
        "data": {
          "name": "设计云平台",
          "sampledAt": "2026-08-10T17:57:00Z",
          "nodes": { "configured": 128, "online": 122 },
          "cpu": { "configured": 8192, "online": 7808, "utilization": 0.485 },
          "load": { "1m": 3747.8, "5m": 4216.3, "15m": 4528.6 },
          "memory": { "configured": 32768.0, "online": 31232.0, "used": 19660.0, "unit": "GB" },
          "gpu": {
            "nodes": { "configured": 16, "online": 16 },
            "cards": { "configured": 128, "online": 124, "utilization": 0.52 },
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
              {
                "QUEUE_NAME": "normal",
                "PRIORITY": "30",
                "STATUS": "Open:Active",
                "MAX": "",
                "JL_U": "",
                "JL_P": "",
                "JL_H": "",
                "NJOBS": "126",
                "PEND": "124",
                "RUN": "2",
                "SUSP": "0"
              }
            ]
          },
          "storage": {
            "summary": { "total": 7600.0, "used": 4546.0, "unit": "TB", "utilization": 0.598 },
            "items": [
              { "id": "st-01", "name": "NFS-Share", "type": "nfs", "total": 400.0, "used": 256.0, "unit": "TB", "sampledAt": "2026-08-10T17:24:00Z" }
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
| **`scheduler.jobs`** | `running` / `pending` / `suspended` | integer | 全集群运行中 / 等待中 / 已暂停作业数 |
| | `completed` / `failed` | integer | 全集群已完成 / 失败作业数 |
| **`scheduler.queues`** | `queues[]` | object[] | 调度器原生队列/分区记录（与 `GET /api/scheduler/queues` 同源；LSF 键如 `QUEUE_NAME`/`PRIORITY`/`NJOBS`/`PEND`/`RUN`/`SUSP`，Slurm 为 partition 投影键） |
| **`storage.summary`** | `total` / `used` | float | 共享存储总容量 / 已用容量 |
| | `unit` | string | 容量单位（自动缩放 TB/GB） |
| | `utilization` | float | 存储利用率 (%) |
| **`storage.items`** | `id` / `name` / `type` | string | 存储 ID / 显示名 / 类型 |
| | `total` / `used` | float | 该存储总容量 / 已用容量 |
| | `unit` / `sampledAt` | string / datetime | 容量单位 / 采样时间 |

### GET /api/overview/me

- **描述**: 与 `GET /api/overview/cluster` 相同，但 `scheduler.jobs` 仅统计**当前登录用户**的作业（其余块仍为全集群口径）。不受 `overview.allowUserCluster` 约束。
- **鉴权**: 是（已登录用户，`Authorization: Bearer <token>`；不受大屏开关影响）
- **请求参数**: 无（实时流见 `GET /api/overview/me/stream`）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误状态码: `401 Unauthorized`（访客 Token 或无效 Token，`invalid session token`）· `428 Precondition Required`（需改密受限会话，`password change required`）

### GET /api/overview/cluster/stream

- **描述**: 集群概览实时流（全集群 `scheduler.jobs`）。连接建立后立即推送第一帧完整快照，之后按缓存刷新节律推送；数据无变化时仅发送心跳（SSE 注释行），有变化才推完整帧。
- **鉴权**: 是（已登录用户，不受大屏开关影响；普通用户受 `overview.allowUserCluster` 约束）；或持有 `PublicOverviewToken` 的访客（须 `platform.website.overviewEnabled` 开启）；Token 取请求头，无请求头时取 `?token=`
- **响应格式**: `text/event-stream`，命名事件 **`event: overview`**，客户端须用 `addEventListener('overview', ...)` 订阅。每帧 `data:` 为单行 JSON `{"data": <Overview>, "version": "<引擎版本>"}`：`data` 与 `GET /api/overview/cluster` 响应体的 `data` 字段结构相同，`version` 为引擎版本（客户端可据此在引擎升级后刷新）。帧不含 `message` / `details`。心跳为 SSE 注释行 `: ping`（每 30s）。
- **响应说明**:
    - 错误状态码: `401 Unauthorized`（凭据无效且未封禁）· `403 Forbidden`（IP 已封禁，或普通用户且 `allowUserCluster=false`）· `404 Not Found`（仅访客 Token 且 `platform.website.overviewEnabled` 关闭，`overview guest access is disabled`）

### GET /api/overview/me/stream

- **描述**: 与 `GET /api/overview/cluster/stream` 相同的帧格式与推送节律，但 `scheduler.jobs` 仅统计**当前登录用户**的作业。
- **鉴权**: 是（已登录用户，仅 `Authorization: Bearer <token>` 请求头；不受大屏开关影响）
- **响应格式**: `text/event-stream`，命名事件 `event: overview`，每帧 `data:` 为 `{"data": <Overview>, "version": "<引擎版本>"}`，`data` 与 `GET /api/overview/me` 响应体的 `data` 字段结构相同
- **响应说明**:
    - 错误状态码: `401 Unauthorized`（访客 Token 或无效 Token，`invalid session token`）· `428 Precondition Required`（需改密受限会话，`password change required`）

---

## 实时推送（SSE）

服务端通过 **SSE（Server-Sent Events）** 提供实时推送，替代前端轮询。连接建立后持续推送，断开即停；客户端断线需自行重连。

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/events` | 客户端全局实时事件总线（SSE 消息流） | 是（已登录用户） |

### GET /api/events

- **描述**: 全局通知流。登录后建立常驻连接，服务端按当前用户（UserID + Role）自动决定推送内容，客户端无需传参。
- **鉴权**: 是（已登录用户）
- **响应格式**: `text/event-stream`，帧含 `event:` / `data:`（部分含 `id:` / `retry:`），每 **15s** 发送一次心跳保活。
- **事件类型（`event:` 字段）**: `job` / `ticket` / `connection` / `node` / `session` / `system`（与 `internal/server/push/event.go` 一致；**无**单独的 `notification` 事件名，Toast 通过载荷 `action: "notify"` 与嵌套 `notify` 表达）
- **统一载荷（`data:` 字段，`EventPayload`）**:

| 字段 | 类型 | 说明 |
|---|---|---|
| `action` | string | `refresh`（数据失效，客户端重拉）/ `logout`（强制下线）/ `notify`（仅 Toast，无需拉数） |
| `targetId` | string | 可选；`refresh` 时有值=局部刷新该实体，无值=全量刷新列表 |
| `notify` | object | 可选；Toast：`title`、`message`、`level`（`info` / `success` / `warning` / `error`）。省略或为 null 表示静默刷新 |
| `extra` | object | 可选；预留扩展 |

详细语义与受众路由见 [状态概览与实时流](/docs/architecture/domains/events-and-overview)。

- **示例**:
  ```
  event: job
  data: {"action":"refresh","targetId":"job-abc","notify":{"title":"作业完成","message":"模型训练已完成","level":"success"}}
  ```

  SSE 首次连接、重连，以及管理员更新 `desktop` 设置域后的静默刷新，客户端都应重新请求同一个公开接口 `GET /api/version`（设置通知载荷不含最低支持版本本身）：

  ```
  event: system
  data: {"action":"refresh","extra":{"domain":"desktop"}}
  ```

---

## Version

Desktop 客户端发布与 sidecar 插件路径为 `/api/desktop/releases/*` 与 `/api/desktop/plugins/*`。最低支持版本由 `GET /api/version` 的 `data.minDesktopVersion` 提供，与最新安装包清单分离。

### 接口列表

| 方法    | 路径                             | 用途                 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/version`                 | 获取 Engine 构建信息、桌面发布是否开启与桌面最低支持版本          | 否  |
| `GET` | `/api/desktop/releases/latest` | 获取 Desktop 客户端最新版本（Velopack 更新清单） | 否  |
| `GET` | `/api/desktop/releases/installer` | 获取首次安装 Setup.exe 元数据（fileName / sha256 / size） | 否  |
| `GET` | `/api/desktop/releases/bootstrap-installer` | 获取 Windows 在线安装器元数据（fileName / sha256 / size） | 否  |
| `GET` | `/api/desktop/releases/download/latest` | 下载最新 Desktop 更新包（通常为 nupkg） | 否  |
| `GET` | `/api/desktop/releases/download/:filename` | 下载指定 Desktop 安装包 | 否  |
| `GET` | `/api/desktop/plugins/:name/latest` | 获取指定 Desktop 插件最新版本 | 否  |
| `GET` | `/api/desktop/plugins/:name/download/latest` | 下载最新插件包 | 否  |
| `GET` | `/api/desktop/plugins/:name/download/:filename` | 下载指定插件包（一般为 `latest.zip`） | 否  |

### GET /api/version

- **描述**: 返回 Engine 运行版本信息，以及当前桌面客户端最低支持版本。`version` / `commit` / `releaseDate` 定位一次 Engine 构建；`desktopEnabled` 表示桌面发布通道是否启用；`minDesktopVersion` 是桌面产品最低支持版本，与 Engine 构建信息以及 `GET /api/desktop/releases/latest` 中的最新安装包相互独立。该下限仅在不兼容的 API 或协议变动时提高；普通新增功能只通过可选更新提供，不抬下限。该字段不依赖发布文件，也不因桌面发布关闭、无安装包、无 Session 或 Session 过期而省略。
- **鉴权**: 否（公开）。无 Session 或 Session 过期均可访问。
- **响应说明**:
    - 状态码: `200 OK`
    - 响应体为标准信封，`data` 始终包含以下字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | string | Engine 构建版本 |
| `commit` | string | Engine 构建 commit |
| `releaseDate` | string | Engine 构建日期 |
| `desktopEnabled` | bool | 桌面发布通道是否启用 |
| `minDesktopVersion` | string | 桌面产品最低支持版本，格式为 `MAJOR.MINOR.PATCH`。空字符串表示不设下限；对客户端而言，字段缺失与空字符串同义 |

```json
{
  "data": {
    "version": "0.3.0",
    "commit": "0d3dfd1f04908e3ee87da08085cb0d8ce48b3ea9",
    "releaseDate": "2026-11-10",
    "desktopEnabled": true,
    "minDesktopVersion": ""
  }
}
```

    - 状态码: `500 Internal Server Error`（无法读取或校验桌面设置时；客户端不得将此情况视为「不设下限」）

客户端用本地产品版本与 `data.minDesktopVersion` 比较（`local >= min` 为通过），以决定是否必须更新。字段缺失与空字符串对客户端同义，表示不设下限；字段非空但不是合法 `MAJOR.MINOR.PATCH` 视为检查失败。最新安装包版本以 `GET /api/desktop/releases/latest` 为准，只用于可选更新，二者不要混用。Session 接口不增加该字段。登录请求体中的 `client.appVersion` 仅为会话上报，不在本接口返回，也不由服务端据此拦截业务 API。没有 426，也没有客户端版本请求头。

### GET /api/desktop/releases/download/:filename

- **描述**: 下载 Desktop 客户端安装包。客户端可由 `releases/latest` 返回的 `assets[].FileName` 拼接此路径（`/api/desktop/releases/download/:filename`）进行下载。
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `filename` | Path | string | 是 | 发布包文件名（如 `DesignCloudDesktop-1.0.0.nupkg`） |

- **响应说明**:
    - 状态码: `200 OK`（文件流）
    - 状态码: `404 Not Found`（文件不存在或文件名非法）

---


### GET /api/desktop/releases/download/latest

- **描述**: 下载最新 Desktop 客户端安装包。
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `os` / `platform` | Query | string | 否 | 平台过滤，语义同 `latest` 端点 |

- **响应说明**:
    - 状态码: `200 OK`（文件流）
    - 状态码: `404 Not Found`（无可用发布包）


### GET /api/desktop/releases/installer

- **描述**: 返回发布目录中的首次安装 Setup 包（文件名由管理台 `desktop.installerFileName` 配置，默认 `DesignCloudDesktop-win-Setup.exe`）的 `fileName` / `sha256` / `size`。Velopack 的 `releases.*.json` 只登记 nupkg，不包含 Setup；`dc-desktop-installer` 在线安装应走此接口再按 `fileName` 下载并校验 SHA256。Setup 为单一固定产物，不做版本化命名；安装器不解析 `releases.*.json`，该清单仅供 Velopack OTA 更新使用。
- **鉴权**: 否
- **响应说明**:
    - 状态码: `200 OK`
    - 响应体 `data` 示例：`{"fileName":"DesignCloudDesktop-win-Setup.exe","sha256":"<64 位十六进制>","size":12345678}`
    - 状态码: `404 Not Found`（配置的 Setup 文件不存在，或 Desktop 发布被管理员关闭）

### GET /api/desktop/releases/bootstrap-installer

- **描述**: 返回 Windows 在线安装器（Online Installer）`DesignCloudDesktop-Installer.exe` 的 `fileName` / `sha256` / `size`。该文件由 `cmd/dc-desktop-installer` 经 `cert inject-installer` 注入本集群 CA 与 Portal 地址后发布，负责安装证书/客户端配置并在线下载、校验 Setup.exe。文件名为固定约定，不可配置；下载走 `/api/desktop/releases/download/DesignCloudDesktop-Installer.exe`。
- **鉴权**: 否
- **响应说明**:
    - 状态码: `200 OK`
    - 响应体 `data` 示例：`{"fileName":"DesignCloudDesktop-Installer.exe","sha256":"<64 位十六进制>","size":12345678}`
    - 状态码: `404 Not Found`（在线安装器未发布，或 Desktop 发布/Windows 平台被管理员关闭）

### GET /api/desktop/releases/latest

- **描述**: 获取 Desktop 客户端最新版本信息（用于客户端自动更新检查）。
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `limit` | Query | int | 否 | 返回资产数量上限；不能超过管理员设置的 `maxAssets`（默认 5） |
| `os` / `platform` | Query | string | 否 | 平台：`win`/`windows`、`linux`、`macos`/`osx`/`darwin`（macOS 读取 `releases.osx.json`）；缺省默认 `win`；非空且不支持的值返回 `404` |

- **响应说明**:
    - 状态码: `200 OK`
    - 状态码: `404 Not Found`（未配置发布或管理员已禁用发布）

### GET /api/desktop/plugins/:name/latest

- **描述**: 获取 Desktop sidecar 插件（如 TurboVNC）的最新发布信息。磁盘布局：`data/releases/desktop/plugins/:name/{win|linux|osx}/latest.zip` + `latest.version`（单行 `version|sha256|size`）。可用 `engine desktop plugin publish` 生成。
- **鉴权**: 否
- **请求参数**:

| 参数名     | 位置 | 类型   | 必选 | 说明               |
|----------|----|------|----|------------------|
| `name` | Path | string | 是 | 插件名（小写字母数字、`_`、`-`，如 `turbovnc`） |
| `os` / `platform` | Query | string | 否 | 平台：`win`/`windows`、`linux`、`macos`/`osx`/`darwin`；缺省 `win` |

- **响应说明**:
    - 状态码: `200 OK`（`data` 含 `version`、`os`、`fileName`、`sha256`、`size`）
    - 状态码: `404 Not Found`（无清单或平台不支持）
    - 状态码: `422 Unprocessable Entity`（`name` 非法）

### GET /api/desktop/plugins/:name/download/:filename

- **描述**: 下载指定插件包（通常为 `latest.zip`）。需带 `os` / `platform` 查询参数以定位平台目录。
- **鉴权**: 否
- **响应说明**:
    - 状态码: `200 OK`（文件流）
    - 状态码: `404 Not Found`

### GET /api/desktop/plugins/:name/download/latest

- **描述**: 下载当前平台的 `latest.zip`（`os` / `platform` 查询参数语义同 `latest`）。
- **鉴权**: 否
