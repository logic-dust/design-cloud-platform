---
title: Portal API · 管理 API
linkTitle: 管理 API
weight: 902
description: 管理 API Portal 端点一览（System/Security/Audit Admin）
---

## 文档导航

- 首页（总体约定、权限模型与公开端点）：[Portal API 总览](./_index.md)
- 用户 API（登录用户/公开）：[Portal API · 用户 API](./user.md)

> 本文件面向**管理 API** 调用者，包含全部 `/api/admin/*` 端点，按 Admin Scope 分节：
> **System Admin**（系统管理）负责资源管理；**Security Admin**（安全管理）负责安全策略；
> **Audit Admin**（审计管理）负责审计日志。Scope 与路由的对应关系见首页[权限模型](./_index.md#权限模型)。

### System Admin 用户 vs Security Admin 授权（路径对照）

| 能力 | 路由前缀 | 典型端点 |
|---|---|---|
| 用户实体（LDAP 同步、组、重置密码、删除、资料） | `/api/admin/system/users` | `GET/POST /sync`、`PATCH /:user`、`DELETE /:user`、`POST /:user/reset-password` |
| 角色与 AdminScope（三员 / 子管理员） | `/api/admin/security/roles`、`/scopes`、`/users` | `GET /roles`、`PUT /scopes/:username`、`PUT /users/:user/role` |

同名「users」分属不同前缀：System 管账号事实，Security 管角色与 Scope 授权。

## System Admin（系统管理）

### 应用管理（Apps）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/system/apps`           | 列出所有应用          | 是（System Admin · ScopeApps） |
| `POST`   | `/api/admin/system/apps`           | 创建应用         | 是（System Admin · ScopeApps） |
| `GET`    | `/api/admin/system/apps/:id`       | 获取应用详情         | 是（System Admin · ScopeApps） |
| `PUT`    | `/api/admin/system/apps/:id`       | 更新应用         | 是（System Admin · ScopeApps） |
| `DELETE` | `/api/admin/system/apps/:id`       | 删除应用         | 是（System Admin · ScopeApps） |
| `GET`    | `/api/admin/system/apps/:id/users` | 获取应用的用户列表    | 是（System Admin · ScopeApps） |
| `PUT`    | `/api/admin/system/apps/:id/users` | 替换式设置应用用户    | 是（System Admin · ScopeApps） |
| `PATCH`  | `/api/admin/system/apps/:id/users` | 增量更新应用用户     | 是（System Admin · ScopeApps） |

### GET /api/admin/system/apps

- **描述**: 列出所有应用
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**: `limit` 默认 500、范围 1～2000；`offset` 默认 0（见 [API 总览 · 列表分页](./_index.md#列表分页limit--offset)）。
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/system/apps

- **描述**: 创建新应用
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**:

| 参数名      | 位置   | 类型     | 必选 | 默认值 | 说明     |
|----------|------|--------|:--:|:---:|--------|
| `name`   | Body | string | 是  |  -  | 应用名称   |
| `alias`  | Body | string | 否  |  -  | 显示别名   |
| `type`   | Body | string | 是  |  -  | 应用类型   |
| `cmd`    | Body | string | 是  |  -  | 启动命令   |
| `icon`   | Body | string | 否  |  -  | 图标     |
| `public` | Body | bool   | 否  |  -  | 是否公开   |
| `form`   | Body | object | 否  |  -  | 启动参数表单 |
| `about`  | Body | string | 否  |  -  | 应用描述   |
| `queue`  | Body | string | 否  |  -  | 调度队列   |

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/admin/system/apps/:id

- **描述**: 获取指定应用的完整配置（与用户面 `GET /api/apps/:id` 字段一致，但不校验当前用户是否在授权列表内；含启动命令、表单定义、队列等）
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**:

| 参数名  | 位置   | 类型      | 必选 | 默认值 | 说明    |
|------|------|---------|:--:|:---:|-------|
| `id` | Path | integer | 是  |  -  | 应用 ID |

- **响应说明**:
    - 状态码: `200 OK`
    - 错误状态码: `404 Not Found`（应用不存在）


### PUT /api/admin/system/apps/:id

- **描述**: 更新应用信息
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**: 同创建应用的字段
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/system/apps/:id

- **描述**: 删除应用（**硬删除**：物理移除记录，并清理 `user_apps` 用户关联；不保留软删副本，因此删除后同名应用可立即重建）
- **鉴权**: 是（System Admin · ScopeApps）
- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/admin/system/apps/:id/users

- **描述**: 获取指定应用的可访问用户列表（数据库 `limit`/`offset` 分页，`ORDER BY username ASC`）。
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**: `limit` / `offset` 同 [列表分页约定](./_index.md#列表分页limit--offset)。
- **响应说明**:
    - 状态码: `200 OK`


### PUT /api/admin/system/apps/:id/users

- **描述**: 替换式设置应用的可访问用户列表
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**:

| 参数名     | 位置   | 类型       | 必选 | 默认值 | 说明    |
|---------|------|----------|:--:|:---:|-------|
| `users` | Body | string[] | 是  |  -  | 用户名列表 |

- **响应说明**:
    - 状态码: `204 No Content`


### PATCH /api/admin/system/apps/:id/users

- **描述**: 增量更新应用用户（添加/移除）
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**:

| 参数名      | 位置   | 类型       | 必选 | 默认值 | 说明        |
|----------|------|----------|:--:|:---:|-----------|
| `add`    | Body | string[] | 否  |  -  | 要添加的用户名列表 |
| `remove` | Body | string[] | 否  |  -  | 要移除的用户名列表 |

- **响应说明**:
    - 状态码: `204 No Content`

---


### 连接管理（Connections）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/system/connections/resources`               | 列出所有连接规格   | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/resources`               | 创建连接规格      | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/resources/batch-upsert`         | 批量创建/更新连接规格 | 是（System Admin · ScopeConnections） |
| `GET`    | `/api/admin/system/connections/resources/:id`           | 获取连接规格详情   | 是（System Admin · ScopeConnections） |
| `PUT`    | `/api/admin/system/connections/resources/:id`           | 更新连接规格      | 是（System Admin · ScopeConnections） |
| `DELETE` | `/api/admin/system/connections/resources/:id`           | 删除连接规格      | 是（System Admin · ScopeConnections） |
| `GET`    | `/api/admin/system/connections/resources/:id/users`     | 获取规格授权用户列表 | 是（System Admin · ScopeConnections） |
| `PUT`    | `/api/admin/system/connections/resources/:id/users`     | 替换设置规格授权用户 | 是（System Admin · ScopeConnections） |
| `PATCH`  | `/api/admin/system/connections/resources/:id/users`     | 增量更新规格授权用户 | 是（System Admin · ScopeConnections） |
| `GET`    | `/api/admin/system/connections`                         | 列出所有连接实例   | 是（System Admin · ScopeConnections） |
| `GET`    | `/api/admin/system/connections/:id`                     | 获取任意连接实例详情| 是（System Admin · ScopeConnections） |
| `DELETE` | `/api/admin/system/connections/:id`                     | 删除任意连接实例   | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/batch-delete`            | 批量删除连接实例   | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/cleanup`                 | 执行连接对账清理   | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/refresh`                 | 触发全局 VNC 采集   | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/start`               | 启动连接实例（按资源重选机） | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/stop`                | 结束远程会话并写 stopped 墓碑（全协议） | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/restart`             | 重启连接实例（stop+start） | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/vnc-otp`             | 为任意 VNC 生成 OTP| 是（System Admin · ScopeConnections） |

### GET /api/admin/system/connections/resources

- **描述**: 分页获取连接规格列表，支持协议与名称模糊过滤
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `limit` | Query | integer | 否 | 每页条数，默认 500，范围 1–2000 |
| `offset` | Query | integer | 否 | 偏移量，默认 0 |
| `protocol` | Query | string | 否 | 按协议过滤 |
| `name` | Query | string | 否 | 按名称模糊过滤 |

- **响应说明**:
    - 状态码: `200 OK`

### POST /api/admin/system/connections/resources

- **描述**: 创建连接规格。`name` 业务唯一；冲突返回 `409 Conflict`（单条创建不覆盖；批量同步请用 **POST …/resources/batch-upsert** 按名 upsert）。候选节点与节点组不能同时为空；`nodes` 中的节点必须已登记，数组内不得有空值或重复项；`nodeGroups` 只做命名语法校验（允许先建规格、后登记节点）。`maxPerUser` 必须在 1 与设置 `connection.maxPerUserCap`（默认 16，本身最大 16）之间；`display` 为整数或 `null`（`null` = 动态/不预留；非 null 时仅允许 `1..99`），VNC 端口由 display 派生。校验失败返回 `400`。服务端不做跨名称的配置相似度去重。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `name` | Body | string | 是 | 规格名称（全局唯一） |
| `protocol` | Body | string | 是 | `VNC` / `RDP` / `SSH` / `XRDP` |
| `nodes` | Body | string[] | 条件 | 候选节点 `nodeId` 列表；与 `nodeGroups` 不能同时为空 |
| `nodeGroups` | Body | string[] | 条件 | 候选节点组名；与 `nodes` 不能同时为空 |
| `display` | Body | integer \| null | 否 | 仅 **VNC / XRDP**：恰好一个 `nodes` 且无 `nodeGroups` 时可固定；`null` 表示动态/不预留；固定值须为 `1..99`。XRDP 的 display 预留 X 席位，不推导 TCP 端口 |
| `port` | Body | integer \| null | 否 | **RDP / SSH / XRDP**：`1..65535`，`null` 时默认 RDP/XRDP `3389`、SSH `22`。VNC 不允许设置（由 display 推导 `5900+display`） |
| `maxPerUser` | Body | integer | 否 | 每用户实例上限，默认 `1` |
| `vncConfig` | Body | object | 否 | 仅 VNC：可选 `resolution`（string）、`startOptions`（string）、`useCgroup`（bool） |
| `public` | Body | boolean | 否 | 是否公开规格，默认 `false` |

- **响应说明**:
    - 状态码: `201 Created`
    - `data` 为规格对象（含 `id`、`createdAt`、`updatedAt` 等 camelCase 字段）


### POST /api/admin/system/connections/resources/batch-upsert

- **描述**: 批量创建或更新连接规格（部分成功）。目录身份以 `name` 为准：
  - `id` 省略或为 `0`：按 **`name` upsert**（已存在则覆盖更新该行，不存在则创建）；同一请求内同名后写覆盖前写。
  - `id > 0`：按主键更新（改名时仍受 `name` 全局唯一约束；与其它规格重名记入该条目 `failures`）。
  - 服务端**不**根据用户/主机/`display` 等字段做跨 `name` 的「实质重复」硬拒绝；此类软提示由客户端负责。运行时占位（如固定 display 争用）仍在启动路径校验。
  - 单次最多 100 条；空列表或超限返回 `400`。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求体示例**:
  ```json
  {
    "items": [
      {
        "id": 0,
        "name": "desk-a",
        "protocol": "VNC",
        "nodes": ["n1"],
        "nodeGroups": [],
        "display": null,
        "port": null,
        "maxPerUser": 1,
        "vncConfig": null,
        "public": false
      },
      {
        "id": 12,
        "name": "desk-b",
        "protocol": "VNC",
        "nodes": ["n2"],
        "nodeGroups": [],
        "maxPerUser": 2,
        "public": false
      }
    ]
  }
  ```
- **响应说明**:
  - 状态码: `200 OK`
  - 响应示例（1 条按名创建、1 条按 id 更新、1 条主键不存在失败）：
    ```json
    {
      "data": {
        "created": 1,
        "updated": 1,
        "failures": [
          { "index": 2, "id": 99999, "name": "nope", "message": "connection resource not found: resource 99999" }
        ]
      }
    }
    ```

### GET /api/admin/system/connections/resources/:id

- **描述**: 获取指定连接规格的详细定义
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `id` | Path | integer | 是 | 规格 ID |

- **响应说明**:
    - 状态码: `200 OK`
    - `404 Not Found`：规格不存在

### PUT /api/admin/system/connections/resources/:id

- **描述**: 更新连接规格定义。校验规则与创建相同；`name` 与其它规格冲突时返回 `409`；规格不存在返回 `404`。改为公开时自动清理已有用户映射；不修改已有实例的落点。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**: Path `id`（规格 ID）；Body 字段与 **POST** 创建相同（整对象提交）。
- **响应说明**:
    - 状态码: `200 OK`

### DELETE /api/admin/system/connections/resources/:id

- **描述**: 删除连接规格（若仍有存量实例引用则返回 `409 Conflict`；无实例时级联清理映射）
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `204 No Content`

### GET /api/admin/system/connections/resources/:id/users

- **描述**: 获取指定连接规格的私有授权用户列表（数据库分页，`ORDER BY id ASC`）。公开规格（`public=true`）恒返回空页。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**: `limit` / `offset` 同 [列表分页约定](./_index.md#列表分页limit--offset)
- **响应说明**:
    - 状态码: `200 OK`
    - 分页 envelope：`data.items` / `data.total`

### PUT /api/admin/system/connections/resources/:id/users

- **描述**: 全量替换指定连接规格的私有授权用户白名单，被移出的用户若有正在运行的代理会话将被断开。公开规格不允许设置映射（`400`）；重复 `userIds` 自动去重；新增用户必须存在（`400`）。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `userIds` | Body | integer[] | 是 | 完整的被授权用户 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`

### PATCH /api/admin/system/connections/resources/:id/users

- **描述**: 增量添加或移除指定连接规格的授权用户。公开规格不允许设置映射（`400`）；`add` 侧校验用户存在，`remove` 侧允许清理悬挂映射；同一用户不能同时出现在 `add` 与 `remove`（`400`）；重复 ID 自动去重。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `addUserIds` | Body | integer[] | 否 | 需要增加授权的用户 ID 列表 |
| `removeUserIds` | Body | integer[] | 否 | 需要移除授权的用户 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`

### GET /api/admin/system/connections

- **描述**: 列出所有用户的连接实例
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**: `limit` / `offset` 同 [列表分页约定](./_index.md#列表分页limit--offset)。
- **响应说明**:
    - 状态码: `200 OK`

### GET /api/admin/system/connections/:id

- **描述**: 获取任意用户的连接实例详情
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `200 OK`

### DELETE /api/admin/system/connections/:id

- **描述**: 删除任意用户的连接实例（若 VNC 运行中则先停止进程）
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `204 No Content`

### POST /api/admin/system/connections/batch-delete

- **描述**: 批量删除任意用户的连接实例
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `ids` | Body | integer[] | 是 | 要删除的连接 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`

### POST /api/admin/system/connections/cleanup

- **描述**: 执行连接对账清理：（1）online 节点 running **VNC** 下发 `CONNECTION_CHECK`（无对应进程则 `stopped`；offline 不再写 DB `unknown`）；（2）重试全部协议的 **`terminating` 墓碑 teardown**（覆盖无席位探针的 RDP/SSH）；（3）物理删除过期 `stopped` 行（默认 90 天，释放配额）。周期任务 `connection_stopped_prune` 亦做 stopped 保留清理。见 [连接模型](/docs/architecture/remote-access/connection)。
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `200 OK`
    - `data.updated` / `data.pruned`：对账更新条数 / 物理删除的过期 stopped 条数

### POST /api/admin/system/connections/:id/:op

- **描述**: 连接生命周期操作（管理员可对任意用户的连接执行）。分发动作：`op` ∈ `start` / `stop`（全协议结束远程会话） / `restart`
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `id` | Path | integer | 是 | 连接实例 ID |
| `op` | Path | string | 是 | 操作类型：`start` / `stop` / `restart` |

- **响应说明**:
    - 状态码: `202 Accepted`（异步操作已接收）
    - `404 Not Found`：连接不存在或未知操作类型

### POST /api/admin/system/connections/:id/vnc-otp

- **描述**: 为指定 VNC 连接生成一次性密码（管理员特权生成，用于直连调试）
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:---:|---|
| `id` | Path | integer | 是 | 连接实例 ID |

- **响应说明**:
    - 状态码: `200 OK`
    - 响应体格式: `Response{data: {"otp": "<vnc-otp>"}}`

### POST /api/admin/system/connections/refresh

- **描述**: 触发全局连接席位采集。广播 `CollectProbeCommand` (probe_name: `"connection-probe"`) 到所有在线 agent，各节点独立采集并上报 `ConnectionReport`（`vnc_seats` + `xrdp_seats`）。
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `202 Accepted`（命令已下发，采集结果异步入 DB）

---

### POST /api/admin/system/nodes/refresh

- **描述**: 触发全节点状态即时采集。广播 `CollectProbeCommand`（`probe_name: "metrics"`）到所有在线 agent，各节点立即执行一次 metrics 采集。
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `202 Accepted`

### POST /api/admin/system/storage/refresh

- **描述**: 触发共享存储容量即时采集。广播 `CollectStorageCommand` 到所有在线 agent，仅配置了 probe 的节点上报。
- **鉴权**: 是（System Admin · ScopeStorages）
- **响应说明**:
    - 状态码: `202 Accepted`


### 许可管理（Licenses）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/system/licenses`     | 列出所有许可证服务（含未启用） | 是（System Admin · ScopeLicenses） |
| `GET`    | `/api/admin/system/licenses/:id` | 获取许可证服务详情（含未启用） | 是（System Admin · ScopeLicenses） |
| `POST`   | `/api/admin/system/licenses`     | 创建许可证服务   | 是（System Admin · ScopeLicenses） |
| `PUT`    | `/api/admin/system/licenses/:id` | 更新许可证服务   | 是（System Admin · ScopeLicenses） |
| `DELETE` | `/api/admin/system/licenses/:id` | 删除许可证服务   | 是（System Admin · ScopeLicenses） |

### GET /api/admin/system/licenses

- **描述**: 列出所有许可证服务（含未启用的下线记录）。各条目 `status` 始终输出内存快照值（未探测或快照 miss 输出 `unknown`，不回源触发外部 CLI）。条目可含 `lmutilPath`（服务级绝对路径覆盖；空或省略表示使用 settings `license.lmutilPath`，含节点覆盖）。
- **鉴权**: 是（System Admin · ScopeLicenses）
- **请求参数**: `limit` / `offset` 同 [列表分页约定](./_index.md#列表分页limit--offset)
- **响应说明**:
    - 状态码: `200 OK`
    - 分页 envelope：`data.items` / `data.total`

### GET /api/admin/system/licenses/:id

- **描述**: 获取指定许可证服务详情（含未启用的记录），叠加内存快照状态。响应可含 `lmutilPath`（语义同列表）。
- **鉴权**: 是（System Admin · ScopeLicenses）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `404 Not Found`（记录不存在）

### POST /api/admin/system/licenses

- **描述**: 创建新的许可证服务配置
- **鉴权**: 是（System Admin · ScopeLicenses）
- **请求参数**:

| 参数名       | 位置   | 类型     | 必选 | 默认值 | 说明     |
|-----------|------|--------|:--:|:---:|--------|
| `name`    | Body | string | 是  |  -  | 服务名称（≤ 64 字符） |
| `type`    | Body | string | 是  |  -  | 许可证类型（固定为 `flexlm`） |
| `address` | Body | string | 是  |  -  | 服务地址（`port@host` 或许可证路径） |
| `server`  | Body | string | 否  |  -  | 探测目标节点 `node_id`（留空则优先本机执行） |
| `lmutilPath` | Body | string | 否  |  -  | 本服务专用 `lmutil` 绝对路径；空则使用 settings `license.lmutilPath`（含节点覆盖） |
| `enabled` | Body | bool   | 否  | true | 是否启用（键缺省时默认 `true`；显式 false 为创建即下线） |
| `comment` | Body | string | 否  |  -  | 备注说明 |

- **响应说明**:
    - 状态码: `201 Created`
    - 错误码: `422 Unprocessable Entity`（业务规则校验失败，如非法 type、空 name/address、相对路径 `lmutilPath`、指定的 server 节点不存在）

### PUT /api/admin/system/licenses/:id

- **描述**: 全量更新许可证服务配置。更新成功后会立即使本节点对应的 status 与 features 缓存失效。
- **鉴权**: 是（System Admin · ScopeLicenses）
- **请求参数**: 同创建接口的字段。注意本接口为全量替换，**更新时必须显式携带 `enabled` 字段**；若请求体中未携带 `enabled`，将被视为 `false`（禁用/下线）。
- **响应说明**:
    - 状态码: `200 OK`
    - 错误码: `422 Unprocessable Entity`（校验失败），`404 Not Found`（记录不存在）

### DELETE /api/admin/system/licenses/:id

- **描述**: 删除许可证服务（同时清理内存缓存）
- **鉴权**: 是（System Admin · ScopeLicenses）
- **响应说明**:
    - 状态码: `204 No Content`
    - 错误码: `404 Not Found`（记录不存在）

---



### 节点管理（Nodes）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/system/nodes`                 | 列出所有节点      | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes`                 | 创建节点        | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/batch-update`           | 批量更新节点（仅 update） | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id`             | 获取节点详情      | 是（System Admin · ScopeNodes） |
| `PUT`    | `/api/admin/system/nodes/:id`             | 更新节点        | 是（System Admin · ScopeNodes） |
| `PUT`    | `/api/admin/system/nodes/:id/certificate` | 更新节点证书      | 是（System Admin · ScopeNodes） |
| `DELETE` | `/api/admin/system/nodes/:id/certificate` | 吊销节点证书      | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/config`      | 获取 Agent 配置 | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/settings`        | 获取节点配置覆盖与最终生效配置 | 是（System Admin · ScopeNodes） |
| `PUT`    | `/api/admin/system/nodes/:id/settings`        | 局部覆盖更新节点配置并在线热更推送 | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/settings/schema` | 获取节点配置覆盖 JSON Schema  | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/settings/batch-update`      | 批量按选择器更新节点配置（支持预演与在线推送） | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/status`          | 获取节点运行状态    | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/:id/tokens`          | 签发注册令牌      | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/certificates`        | 创建集群证书      | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/refresh`             | 触发全节点状态即时采集 | 是（System Admin · ScopeNodes） |

组名语法：`^[a-z][a-z0-9-]{0,31}$`。组只存在于各节点的 `groups` 字段（无独立 `/node-groups` 路由）。创建或 `PUT` 节点时用 `groups` **整表覆盖** 维护成员关系；从某节点去掉组名或全局废弃某组名，均通过对相关节点再次 `PUT` 实现。组目录与成员列表由 `GET /api/admin/system/nodes` 的返回聚合；按组筛选使用查询参数 `groups`（见下列 GET 说明）。

**平台字段（节点 JSON）**：`os`、`arch` 为 Agent 编译目标（`runtime.GOOS` / `runtime.GOARCH`），在 Agent **连接注册**时写入节点行；`osName`、`osVersion`、可选 `kernelVersion` 为 Agent 在**同一次连接注册**时本地探测一次的结果，心跳不会更新这些列。列表/详情返回上述字段；批量 Settings 选择器可按 `os`、`arch` 筛选，不按版本字段筛选。

---

### GET /api/admin/system/nodes

- **描述**: 列出所有集群节点；查询参数 `groups`（逗号分隔）可要求节点**同时**属于所列组名（分页前过滤）。组名目录需客户端对本页或全量节点结果的 `groups` 字段聚合。
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**: `groups`（可选，逗号分隔）；`limit` / `offset` 同 [列表分页约定](./_index.md#列表分页limit--offset)。无 `groups` 时在数据库分页；带 `groups` 时先按组名在内存过滤（JSON `groups` 字段），再对过滤结果切片分页。
- **响应说明**:
    - 状态码: `200 OK`
    - 响应节点对象包含 **`os`**、**`arch`**（Agent 编译目标，连接注册时落库）、**`osName`**、**`osVersion`**、可选 **`kernelVersion`**（连接注册时探测一次；心跳不更新）。未连接前可能为空。
    - 每项均含 **`enrollmentStage`**（`awaiting_certificate` / `awaiting_connect` / `enrolled`）。**`status`**（`online` / `offline` / `maintenance`）仅在节点已完成至少一次成功接入后出现；未接入前省略。


### POST /api/admin/system/nodes

- **描述**: 创建新节点
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**:

| 参数名               | 位置   | 类型     | 必选 | 默认值  | 说明                                                   |
|-------------------|------|---------|:--:|:----:|------------------------------------------------------|
| `nodeId`          | Body | string  | 是  |  -   | 节点 ID                                                |
| `isServer`        | Body | bool    | 否  | false| 是否为 Server 复合节点（Portal+Agent）；false 表示纯 Agent 计算节点   |
| `dialHost`        | Body | string  | 否  |  -   | 拨号地址（Portal/前端建连时拨向该节点的主机名/IP；缺失时兜底为 `nodeId`）                |
| `description`     | Body | string  | 否  |  -   | 备注（最长 512 字符）                                        |
| `groups`          | Body | string[]| 否  |  -   | 扁平组名列表                                               |
| `allowConnections`| Body | bool    | 否  | true | 是否允许接入连接（节点连接准入开关）                                   |
| `maxConnections`  | Body | integer | 否  |  0   | 节点并发连接上限（0 表示不限）                                     |

- **说明**: 创建时不接受运行状态；响应含 `enrollmentStage`，已首次接入后才有 `status`（`online` / `offline` / `maintenance`）。服务端会写入 enrollment token **哈希**与过期时间，但本接口**不返回明文 token**；明文须调用 `POST /api/admin/system/nodes/:id/tokens`。

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/admin/system/nodes/:id

- **描述**: 获取指定节点的详细信息
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应节点对象中包含 `settings` 字段，存储节点级的个性化覆盖配置（若未配置则为 `null` 或空对象）
    - 响应节点对象中包含平台字段（来源同列表接口：`os` / `arch` 为编译目标；`osName` / `osVersion` / `kernelVersion` 为连接注册时一次性探测）
    - 响应节点对象中包含 `allowConnections` (bool) 与 `maxConnections` (uint) 字段
    - 响应含 **`enrollmentStage`**；**`status`** 仅在已接入后出现（同列表接口）



### POST /api/admin/system/nodes/batch-update

- **描述**: 批量稀疏更新已有节点（**仅 update，不创建**）。每项必填 `nodeId`；可变字段与单条 `PUT /api/admin/system/nodes/:id` 相同（省略表示不修改）。节点不存在或单项校验失败记入该条目 `failures` 并继续后续项；无任何可变字段的项既不计入 `updated` 也不进 `failures`。单次最多 100 条；空列表或超限返回 `400`。
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求体示例**:
  ```json
  {
    "items": [
      { "nodeId": "n1", "dialHost": "host-a.example" },
      { "nodeId": "n2", "dialHost": "host-b.example", "description": "lab" }
    ]
  }
  ```
- **响应说明**:
  - 状态码: `200 OK`
  - 响应示例：
    ```json
    {
      "data": {
        "updated": 1,
        "failures": [
          { "index": 1, "nodeId": "n2", "message": "node not found" }
        ]
      }
    }
    ```

### PUT /api/admin/system/nodes/:id

- **描述**: 更新节点信息
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**: `isServer`、`dialHost`、`description`、`groups`（整表覆盖；省略字段表示不修改，传 `[]` 清空该节点全部组名）、`maintenance`（bool，进入/退出维护）、`allowConnections`、`maxConnections`；不可直接写入 `online`/`offline`。
- **响应说明**:
    - 状态码: `200 OK`


### PUT /api/admin/system/nodes/:id/certificate

- **描述**: 更新节点证书信息
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**:

| 参数名               | 位置   | 类型     | 必选 | 默认值 | 说明     |
|-------------------|------|--------|:--:|:---:|--------|
| `certFingerprint` | Body | string | 是  |  -  | 证书指纹   |
| `certSubject`     | Body | string | 是  |  -  | 证书主题   |
| `certExpiresAt`   | Body | string | 是  |  -  | 证书过期时间 |

- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/system/nodes/:id/certificate

- **描述**: 吊销节点证书
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/admin/system/nodes/:id/config

- **描述**: 获取指定节点的 Agent 配置
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/system/nodes/:id/settings

- **描述**: 获取指定节点的原始个性化覆盖配置（`override`）与融合全局后的最终生效配置（`effective`）。`effective` 始终包含完整四域（`agent`、`alert`、`license`、`scheduler`）
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应结构：
      ```json
      {
        "data": {
          "override": {
            "agent": {
              "vnc": { "scanInterval": 120 },
              "monitor": { "nvidiaSmiPath": "/usr/bin/nvidia-smi" }
            }
          },
          "effective": {
            "agent": {
              "execBinPath": "/opt/dce/dc-exec",
              "vnc": { "binDir": "/usr/bin", "scanEnabled": true, "scanInterval": 120, "serverArgs": [], "passwdMode": "user" },
              "monitor": { "metricsEnabled": true, "metricsInterval": 60, "diskMounts": [], "nvidiaSmiPath": "" }
            },
            "alert": {
              "enabled": true,
              "cpu": { "enabled": false, "maxUsedPercent": 0, "cooldownInterval": 300 },
              "load": { "enabled": false, "maxLoadPercent": 110, "cooldownInterval": 300 },
              "memory": { "enabled": true, "maxUsedPercent": 85, "minFreeSpace": 52428800, "cooldownInterval": 300 },
              "disk": { "enabled": true, "maxUsedPercent": 90, "minFreeSpace": 0, "cooldownInterval": 300 },
              "sharedStorage": { "enabled": true, "maxUsedPercent": 90, "minFreeSpace": 0, "cooldownInterval": 1800 }
            },
            "license": {
              "statusCacheTTL": 300,
              "featuresCacheTTL": 300,
              "lmutilPath": "/opt/flexnet/bin/lmutil"
            },
            "scheduler": {
              "type": "lsf",
              "envScript": "/share/apps/lsf/conf/profile.lsf",
              "jobCacheTTL": 60
            }
          }
        }
      }
      ```


### PUT /api/admin/system/nodes/:id/settings

- **描述**: 局部覆盖（Patch）更新节点的个性化覆盖配置，保存后立即向在线 Agent 推送 `UpdateSettingsCommand` 进行零停机热更。
    - **Patch 语义**：仅更新请求中声明的域和字段；未声明的域和字段保持原有覆盖；空对象 `{}` 不会清除已有覆盖。
    - **删除语义**：显式指定 `null` 可清除对应域或字段的覆盖（例如 `{"agent": null}`、`{"license": {"lmutilPath": null}}` 或 `{"scheduler": {"envScript": null}}`；`scheduler` 域节点级仅可覆盖 `envScript`）。
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**: 严格 JSON 对象，必须是 top-level JSON Object，拒绝任何层级未知键（返回 `422 Unprocessable Entity`）。稀疏字段未声明项自动继承全局配置
- **响应说明**:
    - 状态码: `200 OK`
    - 响应内容为更新后的 `NodeSettingsView`（包含 `override` 与 `effective`）


### GET /api/admin/system/nodes/:id/settings/schema

- **描述**: 获取节点级个性化覆盖配置的 JSON Schema 定义
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应包含 `NodeSettings` 的属性结构与校验约束


### POST /api/admin/system/nodes/settings/batch-update

- **描述**: 节点舰队批量修改配置覆盖。根据多维度选择器（`nodeIds`、`groups`、`os`、`arch`、`status`）匹配节点并逐节点应用稀疏 Patch；支持预演模式（`dryRun=true`）。修改成功后精确失效各节点的覆盖缓存，并向在线 Agent 推送生效快照。
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**:
  - `selector` (object, required): 节点匹配条件（`nodeIds`、`groups`、`os`、`arch`、`status`，**不能全为空**，全空返回 `422`；`groups` 为 AND 语义）
  - `patch` (object, required): 稀疏覆盖内容，仅支持 `agent`、`alert`、`license` 域（**禁止 `storage`**，出现返回 `422`）；支持 `null` 恢复全局默认
  - `dryRun` (boolean, optional): 为 `true` 时仅预演计算匹配数与变更数，不写库、不推送
- **请求体示例**:
  ```json
  {
    "selector": {
      "groups": ["login"],
      "os": ["linux"],
      "status": ["online"]
    },
    "patch": {
      "agent": {
        "monitor": {
          "nvidiaSmiPath": "/usr/bin/nvidia-smi",
          "diskMounts": [
            { "enabled": true, "name": "data", "path": "/data", "os": "linux" }
          ]
        }
      }
    },
    "dryRun": false
  }
  ```
- **响应说明**:
  - 状态码: `200 OK`
  - 响应示例：
    ```json
    {
      "data": {
        "matched": 2,
        "updated": 2,
        "unchanged": 0,
        "dryRun": false,
        "failures": []
      }
    }
    ```


### GET /api/admin/system/nodes/:id/status

- **描述**: 获取指定节点的资源运行状态
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/system/nodes/:id/tokens

- **描述**: 为已存在节点签发或轮换一次性注册令牌。节点已经领过证书时也可以签发；新令牌用于 `POST /api/nodes/:id/registration` 重新领取证书并覆盖旧绑定。未带有效令牌时不能重新引导。
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `201 Created`
    - `data` 字段：`nodeId`、`token`、`expiresAt`（明文 token 仅在此响应出现一次）


### POST /api/admin/system/nodes/certificates

- **描述**: 创建集群证书（服务端签名）
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**:

| 参数名         | 位置   | 类型       | 必选 | 默认值 | 说明       |
|-------------|------|----------|:--:|:---:|----------|
| `nodeId`    | Body | string   | 是  |  -  | 节点 ID    |
| `hosts`     | Body | string[] | 是  |  -  | SAN 主机列表 |
| `validDays` | Body | number   | 是  |  -  | 有效天数     |
| `syncNode`  | Body | bool     | 否  |  -  | 是否同步到节点  |

- **响应说明**:
    - 状态码: `200 OK`

---


### 调度管理（Scheduler）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`  | `/api/admin/system/scheduler/meta`                       | 获取当前 HPC 调度器元数据（与用户面同实现） | 是（System Admin · ScopeSchedulers） |
| `GET`  | `/api/admin/system/scheduler/hosts`                      | 列出 HPC 计算主机原生记录（与用户面同实现） | 是（System Admin · ScopeSchedulers） |
| `GET`  | `/api/admin/system/scheduler/queues`                     | 列出 HPC 队列/分区原生记录（与用户面同实现） | 是（System Admin · ScopeSchedulers） |
| `GET`  | `/api/admin/system/scheduler/jobs`                       | 列出所有作业          | 是（System Admin · ScopeSchedulers） |
| `GET`  | `/api/admin/system/scheduler/jobs/:id`                   | 获取任意作业详情    | 是（System Admin · ScopeSchedulers） |
| `POST` | `/api/admin/system/scheduler/jobs/:id/:op`       | 控制任意用户作业（kill/suspend/resume/toTop/toBottom/switch） | 是（System Admin · ScopeSchedulers） |
| `PATCH` | `/api/admin/system/scheduler/jobs/:id`        | 修改任意作业名称    | 是（System Admin · ScopeSchedulers） |
| `POST` | `/api/admin/system/scheduler/jobs/batch-control` | 批量控制任意用户作业（最多 100 个） | 是（System Admin · ScopeSchedulers） |

### GET /api/admin/system/scheduler/meta

- **描述**: 获取当前 HPC 调度器元数据（作业/主机/队列推荐列 `fields.job`/`fields.host`/`fields.queue`、核心字段映射 `keys`、动作状态集合 `actions`；与用户面 `GET /api/scheduler/meta` 相同）
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**: 无
- **响应说明**:
    - 状态码: `200 OK`
    - 响应体结构与字段语义详见 [用户面接口文档](./user.md#get-apischedulermeta)


### GET /api/admin/system/scheduler/hosts

- **描述**: 获取 HPC 调度集群计算主机原生记录（与用户面 `GET /api/scheduler/hosts` 相同）
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **响应说明**:
    - 状态码: `200 OK`

### GET /api/admin/system/scheduler/queues

- **描述**: 获取 HPC 调度队列/分区原生记录（与用户面 `GET /api/scheduler/queues` 相同；不再从作业列表聚合）
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/system/scheduler/jobs

- **描述**: 列出作业；**省略 `user`** 时为全集群所有用户，传 `user` 时按该提交用户过滤（用户面仅提供 `GET /api/scheduler/jobs` 与 `GET /api/scheduler/jobs/me`）
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 默认值 | 说明 |
|--------|------|------|:--:|--------|------|
| `user` | Query | string | 否 | （全集群） | 指定提交用户时收窄列表 |
| `queue` | Query | string | 否 | - | 队列/分区 |
| `node` | Query | string | 否 | - | 执行节点（仅 LSF `bjobs -m`；Slurm 列表忽略） |

- **响应说明**:
    - 状态码: `200 OK`
    - 响应体结构与字段语义与用户面 `GET /api/scheduler/jobs` **完全一致**（`data.scheduler` + `data.jobs`，建议显示列请查询 [`GET /api/admin/system/scheduler/meta`](#get-apiadminsystemschedulermeta)），详见 [用户面接口文档](./user.md#get-apischedulerjobs)；Slurm 列表记录同样为裁剪后的固定子集，详情接口 `GET /api/admin/system/scheduler/jobs/:id` 保持全量


### GET /api/admin/system/scheduler/jobs/:id

- **描述**: 获取任意用户的作业详情
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **响应说明**:
    - 状态码: `200 OK`
    - 错误状态码: `404 Not Found`（作业不存在）· `500 Internal Server Error`（查询失败）


### POST /api/admin/system/scheduler/jobs/:id/:op

- **描述**: 控制任意用户的作业，额外支持指定 `user` 参数
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名           | 位置   | 类型     | 必选 | 默认值 | 说明            |
|---------------|------|--------|:--:|:---:|---------------|
| `op`          | Path | string | 是  |  -  | `kill` / `suspend` / `resume` / `toTop` / `toBottom` / `switch` |
| `user`        | Body | string | 否  |  -  | 目标用户名（默认当前用户） |
| `targetQueue` | Body | string | 否  |  -  | `switch` 操作时必填：目标队列（去除首尾空白后非空、不以 `-` 开头） |

- **响应说明**:
    - 状态码: `202 Accepted`


### PATCH /api/admin/system/scheduler/jobs/:id

- **描述**: 修改任意用户的作业属性
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明    |
|---------|------|--------|:--:|:---:|-------|
| `queue` | Body | string | 否  |  -  | 新队列名（提供时去除首尾空白后非空、不以 `-` 开头） |
| `name`  | Body | string | 否  |  -  | 新作业名称（提供时去除首尾空白后非空、不以 `-` 开头） |
| `user`  | Body | string | 否  |  -  | 目标用户名 |

- **响应说明**:
    - 状态码: `202 Accepted`


### POST /api/admin/system/scheduler/jobs/batch-control

- **描述**: 批量控制任意用户的作业
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名           | 位置   | 类型       | 必选 | 默认值 | 说明       |
|---------------|------|----------|:--:|:---:|----------|
| `jobIds`      | Body | string[] | 是  |  -  | 作业 ID 列表，最多 100 个 |
| `operation`   | Body | string   | 是  |  -  | 与路径 `:op` 相同：`kill` / `suspend` / `resume` / `toTop` / `toBottom` / `switch` |
| `user`        | Body | string   | 否  |  -  | 目标用户名    |
| `targetQueue` | Body | string   | 否  |  -  | `switch` 操作时必填：目标队列（去除首尾空白后非空、不以 `-` 开头） |

- **响应说明**:
    - 状态码: `202 Accepted`
    - 错误状态码: `400 Bad Request`（列表为空、超过 100 个作业或 `targetQueue` 不合法；`message` 为 `targetQueue is required` 或 `targetQueue must not start with '-'`）

---


### 平台设置（Settings）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/system/settings/:domain`        | 获取指定域设置值                        | 是（System Admin · ScopeSettings） |
| `PUT` | `/api/admin/system/settings/:domain`        | 叠加更新指定域设置（未携带字段保持原值）                         | 是（System Admin · ScopeSettings） |
| `GET` | `/api/admin/system/settings/domains`        | 列出所有设置域                         | 是（System Admin · ScopeSettings） |
| `GET` | `/api/admin/system/settings/:domain/schema` | 获取设置域 JSON Schema               | 是（System Admin · ScopeSettings） |

### GET /api/admin/system/settings/:domain

- **描述**: 获取指定域的当前设置值
- **鉴权**: 是（System Admin · ScopeSettings）
- **请求参数**:

| 参数名      | 位置   | 类型     | 必选 | 默认值 | 说明  |
|----------|------|--------|:--:|:---:|-----|
| `domain` | Path | string | 是  |  -  | 设置域 |

- **响应说明**:
    - 状态码: `200 OK`


### PUT /api/admin/system/settings/:domain

- **描述**: **叠加更新**指定域设置。
    - **未携带的字段保持库中现值**（不会被覆盖成零值）；嵌套对象（`website`/`vnc`/`monitor`/告警阈值/`desktop`）递归叠加；数组整体替换。
    - **基线是"读取侧生效值"**（`GET` 看到的那一份），不是库里未经校验的原始 JSON：存量值若通不过该域校验，`GET` 本就整域回退默认值，叠加基线同样用默认值——因此取值非法的脏数据不会让合法的增量更新被 `422` 拒绝，本次写入会把库中那份追平为生效值。
    - 存量行若仅残留 schema **不认识的键**（字段改名/混版本写入），基线规范化会先剥掉未知键再叠加，**不会**因此把未携带字段静默重置为默认值；剥键阶段会保留显式零值（避免规范化本身改配置）。
    - 携带的字段**严格校验**：越界、违反跨字段规则、或出现 schema 之外的未知字段 → 整次修改被拒绝（`422`），不部分生效。
    - 显式传某字段的 `0` 要分两轴看：
      - **校验**：`allowzero` 只用于 `min > 0` 且无独立布尔开关时仍允许哨兵 0（仅 `auth.userSyncInterval`）。`min=0` 的字段（如 `maxProxyConnections`、组合告警 `*usedPercent`）不需要 `allowzero`，0 本就在区间内。`min > 0` 且无 `allowzero` 的字段（如各类周期/刷新间隔、`license.*CacheTTL`、`load.maxLoadPercent`、`cpu.maxUsedPercent`、`uploadMaxSizeBytes`）拒绝 0。
      - **业务含义**：启停统一由独立布尔开关控制；配额项（`maxProxyConnections`）的 0 表示不限；组合告警维度的 0 表示不参与评估。
    - 这不是全量覆盖接口；请求体可仅包含要修改的字段，未列字段会保留当前值。
    - 闭环保证：任何 `GET` 成功返回的域快照，都能原样 `PUT` 回去而不被拒绝。
    - **域内常见数值边界（节选）**：`alert.load.maxLoadPercent` 50–200（默认 110）；`license.statusCacheTTL` / `featuresCacheTTL` 60–900（默认 300）；`auth.maxSessionsPerUser` 1–5。完整约束以 `GET .../settings/:domain/schema` 为准。
- **鉴权**: 是（System Admin · ScopeSettings）
- **请求参数**: 请求体为待修改字段的 JSON 对象（可只含要改的字段）
- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `422` 越界 / 跨字段规则不满足 / 未知字段

---


### GET /api/admin/system/settings/domains

- **描述**: 列出所有可用的设置域（共 12 个活跃域：`platform`, `auth`, `connection`, `file`, `overview`, `storage`, `scheduler`, `license`, `agent`, `alert`, `desktop`, `cert`；协议代理配置嵌套在 `connection.proxy`；VNC 与 Monitor 已合流至 `agent` 复合域）
- **鉴权**: 是（System Admin · ScopeSettings）
- **响应说明**:
    - 状态码: `200 OK`
    - 返回结构: `{ "data": ["platform", "auth", "connection", "file", "overview", "storage", "scheduler", "license", "agent", "alert", "desktop", "cert"] }`



### GET /api/admin/system/settings/:domain/schema

- **描述**: 获取指定设置域的 JSON Schema 定义
- **鉴权**: 是（System Admin · ScopeSettings）
- **请求参数**:
  - `domain`: 设置域名称（如 `agent`、`alert`）
- **响应说明**:
  - 状态码: `200 OK`

---

### 系统告警历史（Alerts History）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/system/alerts/history` | 查询系统资源告警历史记录 | 是（System Admin · ScopeSettings） |

### GET /api/admin/system/alerts/history

- **描述**: 查询系统最近触发与恢复的资源告警历史记录（全量保留，不限制保留条数）
- **鉴权**: 是（System Admin · ScopeSettings）
- **查询参数**:
  - `limit`: 返回条数（可选，默认 100，不做上限截断）
- **响应说明**:
  - 状态码: `200 OK`
  - 返回告警历史结构列表 (`ruleKey`, `metric`, `threshold`, `alertState`, `level`, `message`, `createdAt`)


### 系统状态（System Status）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/system/status` | 获取系统状态 | 是（已登录用户） |

### GET /api/admin/system/status

- **描述**: 获取系统整体健康与运行快照（只读；字段均有单一数据源，不含衍生计数）。
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应体一级键：

| 字段 | 说明 |
|:---|:---|
| `platform` | 平台身份与拓扑：`version`、`uptimeSeconds`、`name`、`description`（platform settings 快照）、`servers`（`cluster.servers` 展开的集群 Server 成员，非节点表统计） |
| `auth` | 认证：`authMode`、`threeRoles`（三员模式）、`ldap?`（`status` / `server?` / `latencyMs?` / `error?`；仅 LDAP 模式） |
| `database` | DB 健康与连接池：`status`（`healthy` / `unhealthy`）、`type`、`host?`、`latencyMs?`、`error?`、`poolOpen` / `poolInUse` / `poolIdle` / `poolWaitTotal` |
| `portal` | Portal HTTP：`address`、`tls`、`cert?` |
| `nodes` | 已 enroll 节点运行时汇总：`total` / `online` / `offline` / `maintenance`（含纯 Agent 与复合 Server 节点，不拆分） |
| `log` | 日志：`dir`、`level` |

    - 示例：

```json
{
  "platform": {
    "version": "1.2.0",
    "uptimeSeconds": 86400,
    "name": "Design Cloud Platform",
    "description": "Design and manage HPC clusters in the cloud",
    "servers": ["cn01", "cn02"]
  },
  "auth": {
    "authMode": "ldap",
    "threeRoles": true,
    "ldap": {
      "status": "healthy",
      "server": "ldap.example.com",
      "latencyMs": 12
    }
  },
  "database": {
    "status": "healthy",
    "type": "postgres",
    "host": "db.internal:5432",
    "latencyMs": 3,
    "poolOpen": 10,
    "poolInUse": 2,
    "poolIdle": 8,
    "poolWaitTotal": 0
  },
  "portal": {
    "address": "0.0.0.0:8443",
    "tls": true,
    "cert": "/path/to/portal.crt"
  },
  "nodes": {
    "total": 24,
    "online": 22,
    "offline": 1,
    "maintenance": 1
  },
  "log": {
    "dir": "/var/log/design-cloud",
    "level": "info"
  }
}
```

---


### 存储管理（Storage）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/system/storage`                     | 获取存储快照 | 是（已登录用户） |
| `GET` | `/api/admin/system/storage/:id/history` | 获取存储历史 | 是（已登录用户） |
| `POST`| `/api/admin/system/storage/refresh`             | 触发共享存储容量即时采集 | 是（System Admin · ScopeStorages） |

### GET /api/admin/system/storage

- **描述**: 获取所有已配置存储的最新容量快照（auto-scale 含 name/type/unit）
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/system/storage/:id/history

- **描述**: 获取指定存储的历史记录（统一单位）
- **鉴权**: 是（已登录用户）
- **请求参数**:

| 参数名    | 位置    | 类型     | 必选 | 默认值 | 说明      |
|--------|-------|--------|:--:|:---:|---------|
| `days` | Query | number | 否  |  7  | 获取最近的天数（上限 90，与保留策略对齐） |

- **响应说明**:
    - 状态码: `200 OK`

---


### 工单管理（Tickets）

工单对象、枚举、错误码与列表参数见 [用户 API · Tickets](./user.md#tickets)。本组接口中已删除的工单与项目带 `deletedAt`。

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/system/tickets` | 列出工单（可含已删除） | 是（System Admin · ScopeTickets） |
| `POST` | `/api/admin/system/tickets/batch-delete` | 批量软删除（全有或全无） | 是（System Admin · ScopeTickets） |
| `POST` | `/api/admin/system/tickets/batch-delete-permanent` | 批量永久删除（全有或全无，不可恢复） | 是（System Admin · ScopeTickets） |
| `DELETE` | `/api/admin/system/tickets/:id` | 软删除工单 | 是（System Admin · ScopeTickets） |
| `DELETE` | `/api/admin/system/tickets/:id/permanent` | 永久删除工单 | 是（System Admin · ScopeTickets） |
| `POST` | `/api/admin/system/tickets/:id/restore` | 恢复软删除的工单 | 是（System Admin · ScopeTickets） |
| `GET` | `/api/admin/system/tickets/projects` | 列出项目 | 是（System Admin · ScopeTickets） |
| `POST` | `/api/admin/system/tickets/projects` | 创建项目 | 是（System Admin · ScopeTickets） |
| `PATCH` | `/api/admin/system/tickets/projects/:id` | 更新项目（含归档） | 是（System Admin · ScopeTickets） |
| `DELETE` | `/api/admin/system/tickets/projects/:id` | 删除项目 | 是（System Admin · ScopeTickets） |

### GET /api/admin/system/tickets

- **描述**: 列出工单。
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**: 与 `GET /api/tickets` 相同（含 `afterSort` / `afterId` keyset 续页），另有 `includeDeleted`（布尔，默认 `false`）。
- **响应说明**: `200 OK`，分页结构；已删除的工单带 `deletedAt`。keyset 约定见用户侧 `GET /api/tickets`。

### POST /api/admin/system/tickets/batch-delete

- **描述**: 批量软删除，全有或全无。
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求体**: `{"ids": [1, 2]}`，1～100 个。
- **响应说明**: `204 No Content`。
- **错误**: `422`；`404 tickets not found`，`details.notFound` 列出不存在或已删除的 ID（单个 ID 时为 `404 ticket not found`）。

### POST /api/admin/system/tickets/batch-delete-permanent

> ⚠️ 破坏性操作：永久删除工单及其评论与变更记录，不可恢复。

- **描述**: 批量永久删除（含已软删的工单），全有或全无。
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求体**: `{"ids": [1, 2]}`，1～100 个。
- **响应说明**: `204 No Content`。
- **错误**: `422`；`404 tickets not found`，`details.notFound` 列出不存在的 ID。

### DELETE /api/admin/system/tickets/:id

- **描述**: 软删除任意未删除的工单。
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**: `204 No Content`；不存在或已删除返回 `404 ticket not found`。

### DELETE /api/admin/system/tickets/:id/permanent

- **描述**: 永久删除工单（含已软删）及其评论与变更记录。
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**: `204 No Content`；不存在返回 `404 ticket not found`。

### POST /api/admin/system/tickets/:id/restore

- **描述**: 恢复软删除的工单，版本号加 1，并记录 `restored` 变更。
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**: `200 OK`，`data` 为恢复后的工单对象。
- **错误**: `404 ticket not found`；`409 ticket is not deleted`；`409 project is deleted`（所在项目已删除）。

### GET /api/admin/system/tickets/projects

- **描述**: 列出项目，按 `id` 升序。
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**: `includeArchived`、`includeDeleted`（布尔，默认 `false`）；`limit` 1～2000（默认 500）、`offset` 0～10000。
- **响应说明**: `200 OK`，分页结构，元素为 `{id, name, description, isArchived, createdAt, updatedAt, deletedAt?}`。

### POST /api/admin/system/tickets/projects

- **描述**: 创建项目。名称在未删除的项目中唯一，已删除项目的名称可以再次使用。
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求体**:

| 字段 | 类型 | 必选 | 说明 |
|---|---|:--:|---|
| `name` | string | 是 | 去首尾空白后 1～128 字符 |
| `description` | string | 否 | 最长 2000 字符 |

- **响应说明**: `201 Created`，`data` 为项目对象。
- **错误**: `422 name is required` / `name is too long` / `description is too long`；`409 project name already exists`。

### PATCH /api/admin/system/tickets/projects/:id

- **描述**: 部分更新未删除的项目。归档后不能在其中创建工单（`409 project is archived`），已有工单不受影响。
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求体**: `name`、`description`、`isArchived`（布尔），均可选，规则同创建。
- **响应说明**: `200 OK`，`data` 为项目对象。
- **错误**: `422`；`404 project not found`；`409 project name already exists`。

### DELETE /api/admin/system/tickets/projects/:id

- **描述**: 软删除项目。
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**: `204 No Content`。
- **错误**: `404 project not found`；`409 project has tickets`（仍有未删除的工单）。

---


### 用户管理（Users）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`   | `/api/admin/system/users`                              | 列出所有用户            | 是（System Admin · ScopeUsers） |
| `GET`   | `/api/admin/system/users/:user`                        | 获取单用户详情           | 是（System Admin · ScopeUsers） |
| `PATCH` | `/api/admin/system/users/:user`                        | 更新用户记录            | 是（System Admin · ScopeUsers） |
| `DELETE` | `/api/admin/system/users/:user`                       | 删除门户用户及其连接    | 是（System Admin · ScopeUsers） |
| `POST`  | `/api/admin/system/users/:user/reset-password` | 重置用户密码            | 是（System Admin · ScopeUsers） |
| `POST`  | `/api/admin/system/users/sync`                 | 同步 LDAP 用户        | 是（System Admin · ScopeUsers） |
| `POST`  | `/api/admin/system/users/id-mappings/refresh` | 刷新 UID/GID 名字缓存 | 是（System Admin · ScopeUsers） |
| `GET`   | `/api/admin/system/users/groups`                       | 获取用户组列表           | 是（System Admin · ScopeUsers） |
| `GET`   | `/api/admin/system/users/groups/:group`                | 获取特定组成员           | 是（System Admin · ScopeUsers） |

### GET /api/admin/system/users

- **描述**: 列出所有用户（可选按用户名 / 最近登录 IP 子串过滤）
- **鉴权**: 是（System Admin · ScopeUsers）
- **请求参数**:

| 参数名      | 位置    | 类型     | 必选 | 默认值 | 说明   |
|----------|-------|--------|:--:|:---:|------|
| `limit`  | Query | number | 否  | 500 | 分页大小，范围 1–2000（超出上限按 2000 截断） |
| `offset` | Query | number | 否  |  0  | 分页偏移（负值按 0） |
| `q`      | Query | string | 否  |  -  | 可选；trim 后空等同不过滤；非空时对 `username` 或 `lastLoginIP` 做不区分大小写的子串匹配；`total` 为过滤后总数 |

- **响应说明**:
    - 状态码: `200 OK`
    - 分页信封 `{ items, total }`；`total` 已应用 `q`


### GET /api/admin/system/users/:user

- **描述**: 获取单个用户详情
- **鉴权**: 是（System Admin · ScopeUsers）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明                |
|---------|------|--------|:--:|:---:|--------------------|
| `user` | Path | string | 是  |  -  | 用户名 |

- **响应说明**:
    - 状态码: `200 OK`
    - 错误状态码: `404` 用户不存在


### PATCH /api/admin/system/users/:user

- **描述**: 管理员更新用户记录（状态、权限开关等）
- **鉴权**: 是（System Admin · ScopeUsers）
- **请求参数**:

| 参数名              | 位置   | 类型     | 必选 | 默认值 | 说明     |
|------------------|------|--------|:--:|:---:|--------|
| `status`         | Body | string | 否  |  -  | 用户状态   |
| `downloadAccess` | Body | bool   | 否  |  -  | 下载权限   |
| `uploadAccess`   | Body | bool   | 否  |  -  | 上传权限   |
| `maxConnections` | Body | number | 否  |  -  | 最大连接数  |

- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/system/users/:user

- **描述**: 删除门户用户。先关闭该用户的代理并结束其 VNC 进程，再删除 `connections`，最后删除用户行。用户不存在时返回 `404`。
- **鉴权**: 是（System Admin · ScopeUsers）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明  |
|---------|------|--------|:--:|:---:|-----|
| `user` | Path | string | 是  |  -  | 用户名 |

- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `404` 用户不存在；`400` 用户名为空


### POST /api/admin/system/users/:user/reset-password

- **描述**: 管理员重置指定用户密码
- **鉴权**: 是（System Admin · ScopeUsers）
- **响应说明**:
    - 状态码: `200 OK`
    - 返回新密码


### POST /api/admin/system/users/sync

- **描述**: 同步 LDAP 用户（从 LDAP 导入/更新用户数据）
- **鉴权**: 是（System Admin · ScopeUsers）
- **响应说明**:
    - 状态码: `202 Accepted`


### POST /api/admin/system/users/id-mappings/refresh

- **描述**: 清空本机 UID/GID → 名字映射缓存（generation++，丢弃在途旧回填），并通过 `cache_versions` 广播使其它 Server 节点在 ≤15 秒内同步清空。不向身份源发起预热查询；仅清缓存，不重载 LDAP/AuthMode 等启动配置（换源需重启进程）。
- **鉴权**: 是（System Admin · ScopeUsers）
- **响应说明**:
    - 状态码: `202 Accepted`（空体）
    - 错误状态码: `503` 本机已清空但广播失败（其它节点靠各自 TTL 兜底，可重试）
- **审计**: 动作名 `ID_MAPPING_CACHE_REFRESH`；记录操作者、节点、时间与广播结果。


### GET /api/admin/system/users/groups

- **描述**: 获取所有用户组列表
- **鉴权**: 是（System Admin · ScopeUsers）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/system/users/groups/:group

- **描述**: 获取指定用户组的成员列表
- **鉴权**: 是（System Admin · ScopeUsers）
- **响应说明**:
    - 状态码: `200 OK`

## Security Admin（安全管理）

### 封禁管理（Bans）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/security/bans`      | 列出所有封禁 | 是（Security Admin · ScopeBans） |
| `POST`   | `/api/admin/security/bans`      | 手动封禁 IP 或 IP+用户 | 是（Security Admin · ScopeBans） |
| `DELETE` | `/api/admin/security/bans`      | 清空所有封禁 | 是（Security Admin · ScopeBans） |
| `DELETE` | `/api/admin/security/bans/:key` | 解除指定封禁 | 是（Security Admin · ScopeBans） |
| `GET`    | `/api/admin/security/bans/allowlist` | 查看封禁放行名单 | 是（Security Admin · ScopeBans） |
| `POST`   | `/api/admin/security/bans/allowlist` | 追加放行条目 | 是（Security Admin · ScopeBans） |
| `DELETE` | `/api/admin/security/bans/allowlist` | 移除放行条目 | 是（Security Admin · ScopeBans） |

### GET /api/admin/security/bans

- **描述**: 获取所有封禁记录（map：封禁键 → 记录）
- **鉴权**: 是（Security Admin · ScopeBans）
- **响应说明**:
    - 状态码: `200 OK`
    - 记录字段: `subject`（`IP` / `UserIP`）、`source`（`Login` / `SessionToken` / `Manual`）、`username`、`ip`、`banTime`、`reason`

### POST /api/admin/security/bans

- **描述**: 手动封禁。立即生效，不涉及失败计数；`source` 固定为 `Manual`
- **鉴权**: 是（Security Admin · ScopeBans）
- **请求体**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:--:|---|
| `subject` | Body | string | 是 | 封禁主体：`IP`（整 IP 封禁）或 `UserIP`（仅封该 IP 下的指定用户） |
| `ip` | Body | string | 是 | 被封禁的 IP 地址（IPv4 / IPv6） |
| `username` | Body | string | 条件 | `subject=UserIP` 时必填 |
| `duration` | Body | int | 是 | 封禁时长（秒），范围 60 ~ 2592000（30 天） |
| `reason` | Body | string | 否 | 封禁原因，缺省为 `banned by administrator` |

- **响应说明**:
    - 状态码: `200 OK`，返回创建的封禁记录（字段同 GET）
    - 参数非法: `400 Bad Request`


### DELETE /api/admin/security/bans

- **描述**: 清空所有封禁
- **鉴权**: 是（Security Admin · ScopeBans）
- **响应说明**:
    - 状态码: `204 No Content`


### DELETE /api/admin/security/bans/:key

- **描述**: 解除指定封禁
- **鉴权**: 是（Security Admin · ScopeBans）
- **请求参数**:

| 参数名   | 位置   | 类型     | 必选 | 说明 |
|-------|------|--------|:--:|------|
| `key` | Path | string | 是  | 封禁记录键（与 `GET /bans` 返回的 map key 一致，格式为 `IP` 或 `IP:username`）。若含 `:` 等保留字符，须按 [RFC 3986](https://datatracker.ietf.org/doc/html/rfc3986) 做 **路径段** URL 编码（如 `10.0.0.1:alice` → `10.0.0.1%3Aalice`） |

- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/admin/security/bans/allowlist

- **描述**: 查看封禁放行名单（`auth` 域 `banAllowlist` 配置）。命中后该来源不参与失败计数，且已有封禁对其不生效。
- **鉴权**: 是（Security Admin · ScopeBans）
- **响应说明**:
    - 状态码: `200 OK`，`data.entries` 为条目数组（每项为单个 IP 或 CIDR 前缀），恒为 `[]` 起步不返回 `null`


### POST /api/admin/security/bans/allowlist

- **描述**: 追加放行条目（已存在则幂等跳过）。底层为 `auth` 域局部更新：域内其他配置不受影响，更新后热生效（无需重启）。
- **鉴权**: 是（Security Admin · ScopeBans）
- **请求体**:

| 参数名 | 位置 | 类型 | 必选 | 说明 |
|---|---|---|:--:|---|
| `entries` | Body | string[] | 是 | 待追加条目，每项须为单个 IP（IPv4 / IPv6）或 CIDR 前缀；不支持 IP 区间写法 |

- **响应说明**:
    - 状态码: `200 OK`，返回更新后的完整条目列表
    - 条目非法: `422 Unprocessable Entity`，**整次变更不生效**（不部分写入）
    - `entries` 缺失或为空: `400 Bad Request`


### DELETE /api/admin/security/bans/allowlist

- **描述**: 移除单个放行条目（不存在则幂等跳过）。
- **鉴权**: 是（Security Admin · ScopeBans）
- **请求参数**:

| 参数名   | 位置   | 类型     | 必选 | 说明 |
|-------|------|--------|:--:|------|
| `entry` | Query | string | 是  | 要移除的条目，须与列表中的条目**完全一致**。含 `/` 时须做 **查询串** URL 编码（如 `10.0.0.0/8` → `10.0.0.0%2F8`） |

- **响应说明**:
    - 状态码: `200 OK`，返回更新后的完整条目列表
    - `entry` 缺失: `400 Bad Request`


### 文件管理（Files）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/security/files/download-requests`                    | 列出所有下载申请          | 是（Security Admin · ScopeFiles） |
| `GET`    | `/api/admin/security/files/download-requests/:id`                | 获取下载申请详情          | 是（Security Admin · ScopeFiles） |
| `POST`   | `/api/admin/security/files/download-requests/:id/review` | 审核下载申请            | 是（Security Admin · ScopeFiles） |

### GET /api/admin/security/files/download-requests

- **描述**: 列出所有用户的下载申请
- **鉴权**: 是（Security Admin · ScopeFiles）
- **请求参数**:

| 参数名         | 位置    | 类型     | 必选 | 默认值 | 说明     |
|-------------|-------|--------|:--:|:---:|--------|
| `status`    | Query | string | 否  |  -  | 按状态过滤  |
| `requester` | Query | string | 否  |  -  | 按申请人过滤 |

- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/security/files/download-requests/:id

- **描述**: 获取指定下载申请的详情
- **鉴权**: 是（Security Admin · ScopeFiles）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/security/files/download-requests/:id/review

- **描述**: 审核下载申请（批准或拒绝）
- **鉴权**: 是（Security Admin · ScopeFiles）
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明                   |
|------------|------|--------|:--:|:---:|----------------------|
| `decision` | Body | string | 是  |  -  | `approve` 或 `reject` |
| `reason`   | Body | string | 否  |  -  | 拒绝原因                 |

- **响应说明**:
    - 状态码: `200 OK`

---


### 用户管理（Users）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`   | `/api/admin/security/roles`                            | 角色目录               | 是（Security Admin）            |
| `GET`   | `/api/admin/security/roles/:role/members`              | 角色成员列表（分页）        | 是（Security Admin）            |
| `GET`   | `/api/admin/security/scopes`                           | 所有用户的 admin scope | 是（Security Admin）            |
| `PUT`   | `/api/admin/security/scopes/:username`                 | 更新用户 scopes       | 是（Security Admin）            |
| `GET`   | `/api/admin/security/scopes/schemas`                   | Scope 元数据 schema  | 是（Security Admin）            |
| `PUT`   | `/api/admin/security/users/:user/role`                 | 设置用户角色            | 是（Security Admin）            |

### GET /api/admin/security/roles

- **描述**: 获取角色目录（固定枚举，无成员）
- **鉴权**: 是（Security Admin）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/security/roles/:role/members

- **描述**: 获取指定角色的成员列表（分页）
- **鉴权**: 是（Security Admin）
- **请求参数**:
    - 路径参数 `role`：大小写不敏感的角色名，`normalUser`、`auditAdmin`、`securityAdmin`、`systemAdmin`、`superAdmin`。
    - `limit` / `offset` 同 [列表分页约定](./_index.md#列表分页limit--offset)。
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/security/scopes

- **描述**: 列出所有用户的 admin scope
- **鉴权**: 是（Security Admin）
- **响应说明**:
    - 状态码: `200 OK`


### PUT /api/admin/security/scopes/:username

- **描述**: 更新指定用户的 admin scopes
- **鉴权**: 是（Security Admin）
- **请求参数**:

| 参数名      | 位置   | 类型       | 必选 | 默认值 | 说明       |
|----------|------|----------|:--:|:---:|----------|
| `scopes` | Body | string[] | 是  |  -  | Scope 列表 |

- **响应说明**:
    - 状态码: `200 OK`

---


### GET /api/admin/security/scopes/schemas

- **描述**: 获取 scope 元数据 schema 定义
- **鉴权**: 是（Security Admin）
- **响应说明**:
    - 状态码: `200 OK`


### PUT /api/admin/security/users/:user/role

- **描述**: 设置用户角色
- **鉴权**: 是（Security Admin）
- **请求参数**:

| 参数名        | 位置   | 类型     | 必选 | 默认值 | 说明   |
|------------|------|--------|:--:|:---:|------|
| `role` | Body | string | 是  |  -  | 角色名，大小写不敏感：`normalUser`、`auditAdmin`、`securityAdmin`、`systemAdmin`、`superAdmin` |

- **响应说明**:
    - 状态码: `200 OK`


### 会话管理（Sessions）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/security/sessions`        | 查看所有在线会话      | 是（Security Admin · ScopeSessions） |
| `POST`   | `/api/admin/security/sessions/batch-delete` | 批量强制删除会话 | 是（Security Admin · ScopeSessions） |
| `DELETE` | `/api/admin/security/sessions`        | 按用户名清空会话      | 是（Security Admin · ScopeSessions） |
| `DELETE` | `/api/admin/security/sessions/:id`    | 强制删除任意会话      | 是（Security Admin · ScopeSessions） |

### GET /api/admin/security/sessions

- **描述**: 查看所有在线会话
- **鉴权**: 是（Security Admin · ScopeSessions）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 默认值 | 说明 |
|---|---|---|:--:|:---:|---|
| `limit` | Query | number | 否 | 500 | 分页大小，范围 1–2000（超出上限按 2000 截断） |
| `offset` | Query | number | 否 | 0 | 分页偏移（负值按 0） |

- **响应说明**:
    - 状态码: `200 OK`
    - 分页返回会话列表；每项包含 `id`、`username`、`ip`、`hostname`、`platform`、`osVersion`、`appVersion`、`deviceId`、`role`、`type`、`createdAt`、`expiresAt` 等字段，其中 `hostname` / `platform` / `osVersion` / `appVersion` / `deviceId` 为登录时客户端自报的值（登录请求体 `client` 对象），未上报时为空字符串。


### POST /api/admin/security/sessions/batch-delete

- **描述**: 按 ID 列表批量强制删除任意用户的会话
- **鉴权**: 是（Security Admin · ScopeSessions）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明                        |
|-------|------|----------|:--:|:---:|---------------------------|
| `ids` | Body | number[] | 是  |  -  | 要强制下线的会话 ID 列表            |

- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `400` `ids` 缺失或格式非法


### DELETE /api/admin/security/sessions

- **描述**: 按用户名删除该用户的**全部**在线会话（强制下线）。**必须**提供 `username` 查询参数；无 query 的集合 DELETE 不会清空全站会话。
- **鉴权**: 是（Security Admin · ScopeSessions）
- **请求参数**:

| 参数名        | 位置    | 类型     | 必选 | 默认值 | 说明        |
|------------|-------|--------|:--:|:---:|-----------|
| `username` | Query | string | 是  |  -  | 要清空会话的用户名（必填，缺失返回 `400`） |

- **响应说明**:
    - 状态码: `204 No Content`

---


### DELETE /api/admin/security/sessions/:id

- **描述**: 强制删除指定会话
- **鉴权**: 是（Security Admin · ScopeSessions）
- **请求参数**:

| 参数名  | 位置   | 类型     | 必选 | 默认值 | 说明    |
|------|------|--------|:--:|:---:|-------|
| `id` | Path | number | 是  |  -  | 会话 ID |

- **响应说明**:
    - 状态码: `204 No Content`

## Audit Admin（审计管理）

### 执行审计（Executions (Audit)）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/audit/executions`              | 分页查询执行记录（支持 serverNode / actionName / status / from / to 过滤） | 是（审计管理员） |
| `GET` | `/api/admin/audit/executions/:executionId` | 按 executionId 查询单条执行记录                                       | 是（审计管理员） |

### GET /api/admin/audit/executions

- **描述**: 分页查询执行记录，结果按 `createdAt` 降序；`createdAt` 相同时按 `id` 降序兜底（稳定分页）。
- **鉴权**: 是（审计管理员 / SuperAdmin）
- **Query 参数**:
    - `serverNode` (string, 可选): 按服务端节点过滤
    - `actionName` (string, 可选): 按 action 名称过滤
    - `status` (int, 可选): 按状态过滤，可选值 1=Pending, 2=Dispatched, 3=Succeeded, 4=Failed, 5=TimedOut, 6=Cancelled
    - `from` (RFC3339, 可选): 创建时间下界
    - `to` (RFC3339, 可选): 创建时间上界
    - `limit` (int, 可选): 每页条数，默认 500，最大 2000
    - `offset` (int, 可选): 偏移量，默认 0
- **响应说明**:
    - 状态码: `200 OK`
    - 响应示例:
      ```json
      {
        "data": {
          "items": [
            {
              "executionId": "...",
              "traceId": "...",
              "actionName": "USER_LIST",
              "serverNode": "node-1",
              "targetNode": "node-2",
              "status": 3,
              "success": true,
              "exitCode": 0,
              "createdAt": "...",
              "updatedAt": "..."
            }
          ],
          "total": 1,
          "limit": 500,
          "offset": 0
        }
      }
      ```


### GET /api/admin/audit/executions/:executionId

- **描述**: 按 executionId 查询单条执行记录
- **鉴权**: 是（审计管理员 / SuperAdmin）
- **响应说明**:
    - 状态码: `200 OK`
    - 状态码: `404 Not Found`（记录不存在时）

---


### 审计管理（Audit）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/audit/logs/:name` | 获取审计日志尾部 | 是（Audit Admin · 只读） |
| `GET` | `/api/admin/audit/logs/:name/stream` | 审计日志实时流（SSE tail-follow） | 是（Audit Admin · 只读） |

### GET /api/admin/audit/logs/:name

- **描述**: 读取审计日志或安全日志的尾部内容
- **鉴权**: 是（Audit Admin · 只读）

> 审计角色在所有 Admin 端点中**只读**（仅 GET/HEAD/OPTIONS）。

- **请求参数**:

| 参数名    | 位置    | 类型     | 必选 | 默认值 | 说明                       |
|--------|-------|--------|:--:|:---:|--------------------------|
| `name` | Path  | string | 是  |  -  | 日志名：`audit` 或 `security` |
| `tail` | Query | number | 否  | 50  | 返回的尾部行数                  |
| `page` | Query | number | 否  |  -  | 分页                       |

- **响应说明**:
    - 状态码: `200 OK`

### GET /api/admin/audit/logs/:name/stream

- **描述**: 审计日志实时流。从文件末尾开始（不推历史），每 **500ms** 轮询新增行并逐行推送；日志轮转（rename + 新建）时自动跟随新文件。
- **鉴权**: 是（Audit Admin · 只读）
- **响应格式**: `text/event-stream`，每帧 `data:` 为一行原始日志文本。

---
