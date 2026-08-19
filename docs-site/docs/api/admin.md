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

## System Admin（系统管理）

### 应用管理（Apps）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/system/apps`           | 列出所有应用          | 是（System Admin · ScopeApps） |
| `POST`   | `/api/admin/system/apps`           | 创建应用         | 是（System Admin · ScopeApps） |
| `PUT`    | `/api/admin/system/apps/:id`       | 更新应用         | 是（System Admin · ScopeApps） |
| `DELETE` | `/api/admin/system/apps/:id`       | 删除应用         | 是（System Admin · ScopeApps） |
| `GET`    | `/api/admin/system/apps/:id/users` | 获取应用的用户列表    | 是（System Admin · ScopeApps） |
| `PUT`    | `/api/admin/system/apps/:id/users` | 替换式设置应用用户    | 是（System Admin · ScopeApps） |
| `PATCH`  | `/api/admin/system/apps/:id/users` | 增量更新应用用户     | 是（System Admin · ScopeApps） |

### GET /api/admin/system/apps

- **描述**: 列出所有应用
- **鉴权**: 是（System Admin · ScopeApps）
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


### PUT /api/admin/system/apps/:id

- **描述**: 更新应用信息
- **鉴权**: 是（System Admin · ScopeApps）
- **请求参数**: 同创建应用的字段
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/system/apps/:id

- **描述**: 删除应用
- **鉴权**: 是（System Admin · ScopeApps）
- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/admin/system/apps/:id/users

- **描述**: 获取指定应用的可访问用户列表
- **鉴权**: 是（System Admin · ScopeApps）
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
| `GET`    | `/api/admin/system/connections`                         | 列出所有连接       | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections`                         | 创建 Fixed 连接       | 是（System Admin · ScopeConnections） |
| `GET`    | `/api/admin/system/connections/:id`                     | 获取任意连接详情    | 是（System Admin · ScopeConnections） |
| `DELETE` | `/api/admin/system/connections/:id`                     | 删除任意连接      | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/batch-delete`    | 批量删除任意连接    | 是（System Admin · ScopeConnections） |
| `PUT`    | `/api/admin/system/connections/batch-upsert`    | 批量创建/更新 Fixed 连接（Upsert） | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/cleanup`         | 执行连接清理      | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/nodes/refresh`  | 触发全节点状态即时采集 | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/storage/refresh` | 触发共享存储即时采集  | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/refresh`     | 触发全局 VNC 采集 | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/start`       | 启动 Fixed VNC 连接 | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/stop`        | 停止 Fixed VNC 连接 | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/restart`     | 重启 Fixed VNC 连接 | 是（System Admin · ScopeConnections） |
| `POST`   | `/api/admin/system/connections/:id/vnc-otp`     | 为任意 VNC 连接生成 OTP | 是（System Admin · ScopeConnections） |

### GET /api/admin/system/connections

- **描述**: 列出所有用户的连接
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/system/connections

- **描述**: 管理员为指定用户创建固定分配（Fixed）连接记录
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名         | 位置   | 类型     | 必选 | 默认值 | 说明              |
|-------------|------|--------|:--:|:---:|-----------------|
| `name`      | Body | string | 是  |  -  | 连接标识（如 cn01-display11） |
| `username`  | Body | string | 是* |  -  | 归属用户；共享连接（`shared=true`）时留空 |
| `shared`    | Body | boolean | 否  | false | 是否共享连接（仅 Fixed 可共享，所有用户可见；VNC 不支持共享） |
| `targetNodeId` | Body | string | 是  |  -  | 目标计算节点         |
| `host`      | Body | string | 是  |  -  | 节点 IP/主机名       |
| `protocol`  | Body | string | 是  |  -  | 协议（VNC/RDP/SSH） |
| `port`      | Body | number | 是  |  -  | 端口（1-65535）     |
| `display`   | Body | string | 否  |  -  | VNC display（如 :11） |
| `startOptions` | Body | string | 否 |  -  | VNC 启动附加选项（如 `-wm 2d -geometry 1920x1080`） |
| `otpEnabled`   | Body | boolean | 否 | true* | 是否允许动态签发 TurboVNC OTP（VNC 默认为 true） |

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/admin/system/connections/:id

- **描述**: 获取任意用户的连接详情
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/system/connections/:id

- **描述**: 删除任意用户的连接
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `204 No Content`


### POST /api/admin/system/connections/batch-delete

- **描述**: 批量删除任意用户的连接
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明           |
|-------|------|----------|:--:|:---:|--------------|
| `ids` | Body | string[] | 是  |  -  | 要删除的连接 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content` / `409 Conflict`


### PUT /api/admin/system/connections/batch-upsert

- **描述**: 批量创建或更新固定分配（Fixed）连接。幂等操作：存在则更新属性，不存在则创建。支持部分失败跳过（不整批回滚），自动校验目标节点存在性；`host` 选填（未填时自动填充目标节点的客户端 IP/主机名）；VNC 协议下支持 `port` 与 `display` 双向推导。
- **鉴权**: 是（System Admin · ScopeConnections）
- **请求参数**:

| 参数名 | 位置 | 类型 | 必选 | 默认值 | 说明 |
|---|---|---|:---:|:---:|---|
| `records` | Body | object[] | 是 | - | 固定连接表单数组 |
| `records[].username` | Body | string | 是* | - | 归属用户；共享连接（`shared=true`）时留空 |
| `records[].targetNodeId` | Body | string | 是 | - | 目标计算节点（必须是已注册节点） |
| `records[].port` | Body | number | 否 | 0 | 端口号（1-65535，0 表示未指定或占位） |
| `records[].display` | Body | string | 否 | - | VNC display（如 `:1`，支持自动补齐 `:` 前缀） |
| `records[].protocol` | Body | string | 否 | "VNC" | 协议（VNC/RDP/SSH，缺省为 VNC） |
| `records[].host` | Body | string | 否 | - | 节点 IP/主机名（选填，缺省从目标节点信息自动提取） |
| `records[].name` | Body | string | 否 | - | 连接标识（选填，缺省自动生成） |
| `records[].alias` | Body | string | 否 | - | 连接别名 |
| `records[].startOptions` | Body | string | 否 | - | 启动附加选项 |
| `records[].otpEnabled` | Body | boolean | 否 | true* | 是否允许签发 OTP（VNC 默认为 true） |
| `records[].shared` | Body | boolean | 否 | false | 是否共享连接（所有用户可见，VNC 不支持共享） |

- **响应说明**:
    - 状态码: `200 OK`
    - 返回数据：
      ```json
      {
        "data": {
          "created": 2,
          "updated": 1,
          "failed": [
            {
              "index": 3,
              "username": "charlie",
              "targetNodeId": "unknown-node",
              "reason": "target node \"unknown-node\" not found"
            }
          ]
        }
      }
      ```


### POST /api/admin/system/connections/cleanup

- **描述**: 执行连接清理（移除过期/无效连接）
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `200 OK`

---


### POST /api/admin/system/nodes/refresh

- **描述**: 触发全节点状态即时采集。广播 `CollectResourceCommand` 到所有在线 agent，各节点立即执行一次 resource_report。
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `202 Accepted`


### POST /api/admin/system/storage/refresh

- **描述**: 触发共享存储容量即时采集。广播 `CollectStorageCommand` 到所有在线 agent，仅配置了 probe 的节点上报。
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `202 Accepted`


### POST /api/admin/system/connections/refresh

- **描述**: 触发全局 VNC 连接采集。广播 `CollectProbeCommand` (probe_name: "vnc-probe") 到所有在线 agent，各节点独立采集并上报
  VNC 会话。
- **鉴权**: 是（System Admin · ScopeConnections）
- **响应说明**:
    - 状态码: `202 Accepted`（命令已下发，采集结果异步入 DB）


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

- **描述**: 列出所有许可证服务（含未启用的下线记录）。各条目 `status` 始终输出内存快照值（未探测或快照 miss 输出 `unknown`，不回源触发外部 CLI）。
- **鉴权**: 是（System Admin · ScopeLicenses）
- **响应说明**:
    - 状态码: `200 OK`

### GET /api/admin/system/licenses/:id

- **描述**: 获取指定许可证服务详情（含未启用的记录），叠加内存快照状态。
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
| `enabled` | Body | bool   | 否  | true | 是否启用（键缺省时默认 `true`；显式 false 为创建即下线） |
| `comment` | Body | string | 否  |  -  | 备注说明 |

- **响应说明**:
    - 状态码: `201 Created`
    - 错误码: `422 Unprocessable Entity`（业务规则校验失败，如非法 type、空 name/address、指定的 server 节点不存在）

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
| `GET`    | `/api/admin/system/nodes/:id`             | 获取节点详情      | 是（System Admin · ScopeNodes） |
| `PUT`    | `/api/admin/system/nodes/:id`             | 更新节点        | 是（System Admin · ScopeNodes） |
| `PUT`    | `/api/admin/system/nodes/:id/certificate` | 更新节点证书      | 是（System Admin · ScopeNodes） |
| `DELETE` | `/api/admin/system/nodes/:id/certificate` | 吊销节点证书      | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/config`      | 获取 Agent 配置 | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/settings`        | 获取节点配置覆盖与最终生效配置 | 是（System Admin · ScopeNodes） |
| `PUT`    | `/api/admin/system/nodes/:id/settings`        | 局部覆盖更新节点配置并在线热更推送 | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/settings/schema` | 获取节点配置覆盖 JSON Schema  | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/settings/batch`      | 批量按选择器更新节点配置（支持预演与在线推送） | 是（System Admin · ScopeNodes） |
| `GET`    | `/api/admin/system/nodes/:id/status`          | 获取节点运行状态    | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/:id/tokens`          | 签发注册令牌      | 是（System Admin · ScopeNodes） |
| `POST`   | `/api/admin/system/nodes/certificates`        | 创建集群证书      | 是（System Admin · ScopeNodes） |

### GET /api/admin/system/nodes

- **描述**: 列出所有集群节点
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应节点对象包含 `os` 字段：由 Agent 在注册/入群时经 `SystemInfo.os` 上报并落库（如 `windows` / `linux`）；未上报前可能为空字符串


### POST /api/admin/system/nodes

- **描述**: 创建新节点
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**:

| 参数名               | 位置   | 类型     | 必选 | 默认值  | 说明                                                   |
|-------------------|------|---------|:--:|:----:|------------------------------------------------------|
| `nodeId`          | Body | string  | 是  |  -   | 节点 ID                                                |
| `isServer`        | Body | bool    | 否  | false| 是否为 Server 复合节点（Portal+Agent）；false 表示纯 Agent 计算节点   |
| `status`          | Body | string  | 是  |  -   | 节点状态                                                 |
| `clientHost`      | Body | string  | 否  |  -   | 客户端主机                                                |
| `labels`          | Body | object  | 否  |  -   | 标签                                                   |
| `allowConnections`| Body | bool    | 否  | true | 是否允许接入连接（节点连接准入开关）                                   |
| `maxConnections`  | Body | integer | 否  |  0   | 节点并发连接上限（0 表示不限）                                     |

- **响应说明**:
    - 状态码: `201 Created`


### GET /api/admin/system/nodes/:id

- **描述**: 获取指定节点的详细信息
- **鉴权**: 是（System Admin · ScopeNodes）
- **响应说明**:
    - 状态码: `200 OK`
    - 响应节点对象中包含 `settings` 字段，存储节点级的个性化覆盖配置（若未配置则为 `null` 或空对象）
    - 响应节点对象中包含 `os` 字段（来源同列表接口：Agent `SystemInfo.os` 落库；取值如 `windows` / `linux`）
    - 响应节点对象中包含 `allowConnections` (bool) 与 `maxConnections` (uint) 字段


### PUT /api/admin/system/nodes/:id

- **描述**: 更新节点信息
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**: 同创建节点（支持更新 `allowConnections` 与 `maxConnections`）
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

- **描述**: 获取指定节点的原始个性化覆盖配置（`override`）与融合全局后的最终生效配置（`effective`）。`effective` 始终包含完整三域（`agent`、`alert`、`license`）
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
              "monitor": { "resourceReportInterval": 60 }
            }
          },
          "effective": {
            "agent": {
              "launcherPath": "/opt/dc/launcher",
              "vnc": { "binDir": "/usr/bin", "scanEnabled": true, "scanInterval": 120, "serverArgs": [] },
              "monitor": { "resourceReportEnabled": true, "resourceReportInterval": 60, "diskMounts": [], "nvidiaSmiPath": "" }
            },
            "alert": {
              "enabled": true,
              "cpu": { "enabled": false, "maxUsedPercent": 0, "cooldownInterval": 300 },
              "load": { "enabled": false, "maxLoadPercent": 0, "cooldownInterval": 300 },
              "memory": { "enabled": true, "maxUsedPercent": 85, "minFreeSpace": 52428800, "cooldownInterval": 300 },
              "disk": { "enabled": true, "maxUsedPercent": 90, "minFreeSpace": 0, "cooldownInterval": 300 },
              "sharedStorage": { "enabled": true, "maxUsedPercent": 90, "minFreeSpace": 0, "cooldownInterval": 1800 }
            },
            "license": {
              "statusCacheTTL": 60,
              "featuresCacheTTL": 60,
              "lmutilPath": "/opt/flexnet/bin/lmutil"
            }
          }
        }
      }
      ```


### PUT /api/admin/system/nodes/:id/settings

- **描述**: 局部覆盖（Patch）更新节点的个性化覆盖配置，保存后立即向在线 Agent 推送 `UpdateSettingsCommand` 进行零停机热更。
    - **Patch 语义**：仅更新请求中声明的域和字段；未声明的域和字段保持原有覆盖；空对象 `{}` 不会清除已有覆盖。
    - **删除语义**：显式指定 `null` 可清除对应域或字段的覆盖（例如 `{"agent": null}` 或 `{"license": {"lmutilPath": null}}`）。
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


### POST /api/admin/system/nodes/settings/batch

- **描述**: 节点舰队批量修改配置覆盖。根据多维度选择器（`nodeIds`、`labels`、`os`、`status`）匹配节点并逐节点应用稀疏 Patch；支持预演模式（`dryRun=true`）。修改成功后精确失效各节点的覆盖缓存，并向在线 Agent 推送生效快照。
- **鉴权**: 是（System Admin · ScopeNodes）
- **请求参数**:
  - `selector` (object, required): 节点匹配条件（`nodeIds`、`labels`、`os`、`status`，**不能全为空**，全空返回 `422`）
  - `patch` (object, required): 稀疏覆盖内容，仅支持 `agent`、`alert`、`license` 域（**禁止 `storage`**，出现返回 `422`）；支持 `null` 恢复全局默认
  - `dryRun` (boolean, optional): 为 `true` 时仅预演计算匹配数与变更数，不写库、不推送
- **请求体示例**:
  ```json
  {
    "selector": {
      "nodeIds": ["gpu01", "gpu02"],
      "labels": { "pool": "gpu" },
      "os": ["linux"],
      "status": ["online"]
    },
    "patch": {
      "agent": {
        "monitor": {
          "resourceReportInterval": 30,
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
    - `data` 字段：`nodeID`、`token`、`tokenCreatedAt`


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
| `GET`  | `/api/admin/system/scheduler/jobs`                       | 列出所有作业          | 是（System Admin · ScopeSchedulers） |
| `GET`  | `/api/admin/system/scheduler/jobs/:id`                   | 获取任意作业详情    | 是（System Admin · ScopeSchedulers） |
| `POST` | `/api/admin/system/scheduler/jobs/:id/:op`       | 控制任意用户作业（kill/suspend/resume/top/bottom/switch） | 是（System Admin · ScopeSchedulers） |
| `PATCH` | `/api/admin/system/scheduler/jobs/:id`        | 修改任意作业名称    | 是（System Admin · ScopeSchedulers） |
| `POST` | `/api/admin/system/scheduler/jobs/batch-control` | 批量控制任意用户作业  | 是（System Admin · ScopeSchedulers） |

### GET /api/admin/system/scheduler/jobs

- **描述**: 列出所有用户的作业
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **响应说明**:
    - 状态码: `200 OK`


### GET /api/admin/system/scheduler/jobs/:id

- **描述**: 获取任意用户的作业详情
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/system/scheduler/jobs/:id/:op

- **描述**: 控制任意用户的作业，额外支持指定 `user` 参数
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名           | 位置   | 类型     | 必选 | 默认值 | 说明            |
|---------------|------|--------|:--:|:---:|---------------|
| `operation`   | Body | string | 是  |  -  | 操作类型          |
| `user`        | Body | string | 否  |  -  | 目标用户名（默认当前用户） |
| `targetQueue` | Body | string | 否  |  -  | 目标队列          |

- **响应说明**:
    - 状态码: `202 Accepted`


### PATCH /api/admin/system/scheduler/jobs/:id

- **描述**: 修改任意用户的作业属性
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名     | 位置   | 类型     | 必选 | 默认值 | 说明    |
|---------|------|--------|:--:|:---:|-------|
| `queue` | Body | string | 否  |  -  | 新队列名  |
| `name`  | Body | string | 否  |  -  | 新作业名称 |
| `user`  | Body | string | 否  |  -  | 目标用户名 |

- **响应说明**:
    - 状态码: `202 Accepted`


### POST /api/admin/system/scheduler/jobs/batch-control

- **描述**: 批量控制任意用户的作业
- **鉴权**: 是（System Admin · ScopeSchedulers）
- **请求参数**:

| 参数名           | 位置   | 类型       | 必选 | 默认值 | 说明       |
|---------------|------|----------|:--:|:---:|----------|
| `jobIds`      | Body | string[] | 是  |  -  | 作业 ID 列表 |
| `operation`   | Body | string   | 是  |  -  | 操作类型     |
| `user`        | Body | string   | 否  |  -  | 目标用户名    |
| `targetQueue` | Body | string   | 否  |  -  | 目标队列     |

- **响应说明**:
    - 状态码: `202 Accepted`

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
    - 显式传某字段的 `0`（仅对 `allowzero` 字段合法）要分两轴看：
      - **持久化（是否 `omitempty`）**：
        - **无 `omitempty`**：显式 `0` 原样落库（如 `userSyncInterval`、`maxConnectionsPerUser`）；
        - **有 `omitempty`**：写库序列化省略该键，端到端与未携带等效（如 `connectionListCacheTTL`、`jobCacheTTL`，以及同 tag 组合的 `probeInterval` / `storageQueryWindow` / dashboard `*RefreshInterval` / `resourceReportInterval`）。
      - **业务含义（与 tag 正交，以字段约定为准）**：可能是「回退默认」「关闭功能」或「合法零值（配额/阈值）」；`allowzero` 本身不编码这三种含义。
    - 这不是全量覆盖接口；请求体可仅包含要修改的字段，未列字段会保留当前值。
    - 闭环保证：任何 `GET` 成功返回的域快照，都能原样 `PUT` 回去而不被拒绝。
- **鉴权**: 是（System Admin · ScopeSettings）
- **请求参数**: 请求体为待修改字段的 JSON 对象（可只含要改的字段）
- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `422` 越界 / 跨字段规则不满足 / 未知字段

---


### GET /api/admin/system/settings/domains

- **描述**: 列出所有可用的设置域（共 13 个活跃域：`platform`, `auth`, `connection`, `tunnel`, `file`, `dashboard`, `storage`, `scheduler`, `license`, `agent`, `alert`, `desktop`, `cert`；VNC 与 Monitor 已合流至 `agent` 复合域）
- **鉴权**: 是（System Admin · ScopeSettings）
- **响应说明**:
    - 状态码: `200 OK`
    - 返回结构: `{ "data": ["platform", "auth", "connection", "tunnel", "file", "dashboard", "storage", "scheduler", "license", "agent", "alert", "desktop", "cert"] }`



### GET /api/admin/system/settings/:domain/schema

- **描述**: 获取指定设置域的 JSON Schema 定义
- **鉴权**: 是（System Admin · ScopeSettings）
- **请求参数**:
  - `domain`: 设置域名称（如 `agent`、`alert`）
- **响应说明**:
  - 状态码: `200 OK`

---

### 系统告警历史（Alerts History）

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

- **描述**: 获取系统整体健康状态
- **鉴权**: 是（已登录用户）
- **响应说明**:
    - 状态码: `200 OK`

---


### 存储管理（Storage）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET` | `/api/admin/system/storage`                     | 获取存储快照 | 是（已登录用户） |
| `GET` | `/api/admin/system/storage/:id/history` | 获取存储历史 | 是（已登录用户） |

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
| `days` | Query | number | 否  |  7  | 获取最近的天数 |

- **响应说明**:
    - 状态码: `200 OK`

---


### 工单管理（Tickets）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`    | `/api/admin/system/tickets`                      | 列出所有工单       | 是（System Admin · ScopeTickets） |
| `POST`   | `/api/admin/system/tickets/batch-delete`         | 批量软删除工单      | 是（System Admin · ScopeTickets） |
| `POST`   | `/api/admin/system/tickets/batch-delete-permanent` | 批量永久删除工单（不可恢复） | 是（System Admin · ScopeTickets） |
| `DELETE` | `/api/admin/system/tickets/:id`                  | 软删除任意工单      | 是（System Admin · ScopeTickets） |
| `DELETE` | `/api/admin/system/tickets/:id/permanent`        | 永久删除工单       | 是（System Admin · ScopeTickets） |
| `GET`    | `/api/admin/system/tickets/projects`             | 列出所有项目       | 是（System Admin · ScopeTickets） |
| `POST`   | `/api/admin/system/tickets/projects`             | 创建项目         | 是（System Admin · ScopeTickets） |
| `PUT`    | `/api/admin/system/tickets/projects/:id`         | 更新项目         | 是（System Admin · ScopeTickets） |
| `DELETE` | `/api/admin/system/tickets/projects/:id`         | 删除项目         | 是（System Admin · ScopeTickets） |
| `PATCH`  | `/api/admin/system/tickets/projects/:id/archive` | 归档/取消归档项目    | 是（System Admin · ScopeTickets） |

### GET /api/admin/system/tickets

- **描述**: 列出所有工单（可包含已删除的）
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**:

| 参数名              | 位置    | 类型     | 必选 | 默认值 | 说明      |
|------------------|-------|--------|:--:|:---:|---------|
| `includeDeleted` | Query | bool   | 否  |  -  | 是否包含已删除 |
| `projectId`      | Query | string | 否  |  -  | 项目 ID   |
| `type`           | Query | string | 否  |  -  | 工单类型    |
| `status`         | Query | string | 否  |  -  | 状态      |
| `limit`          | Query | number | 否  |  -  | 分页大小    |
| `offset`         | Query | number | 否  |  -  | 分页偏移    |

- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/system/tickets/batch-delete

- **描述**: 批量软删除工单
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明             |
|-------|------|----------|:--:|:---:|----------------|
| `ids` | Body | number[] | 是  |  -  | 要删除的工单 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `400` `ids` 缺失或格式非法


### POST /api/admin/system/tickets/batch-delete-permanent

> ⚠️ 破坏性操作：批量**永久删除**工单及其数据，删除后不可恢复。

- **描述**: 批量永久删除工单（等同对每个 ID 调用 permanent delete）
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**:

| 参数名   | 位置   | 类型       | 必选 | 默认值 | 说明                 |
|-------|------|----------|:--:|:---:|--------------------|
| `ids` | Body | number[] | 是  |  -  | 要永久删除的工单 ID 列表 |

- **响应说明**:
    - 状态码: `204 No Content`
    - 错误状态码: `400` `ids` 缺失或格式非法


### DELETE /api/admin/system/tickets/:id

- **描述**: 软删除任意工单
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**:
    - 状态码: `204 No Content`


### DELETE /api/admin/system/tickets/:id/permanent

- **描述**: 永久删除工单
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**:
    - 状态码: `204 No Content`


### GET /api/admin/system/tickets/projects

- **描述**: 列出所有项目（可包含已归档/已删除的）
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**:

| 参数名               | 位置    | 类型     | 必选 | 默认值 | 说明      |
|-------------------|-------|--------|:--:|:---:|---------|
| `includeArchived` | Query | bool   | 否  |  -  | 是否包含已归档 |
| `includeDeleted`  | Query | bool   | 否  |  -  | 是否包含已删除 |
| `limit`           | Query | number | 否  |  -  | 分页大小    |
| `offset`          | Query | number | 否  |  -  | 分页偏移    |

- **响应说明**:
    - 状态码: `200 OK`


### POST /api/admin/system/tickets/projects

- **描述**: 创建新项目
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**:

| 参数名           | 位置   | 类型     | 必选 | 默认值 | 说明   |
|---------------|------|--------|:--:|:---:|------|
| `name`        | Body | string | 是  |  -  | 项目名称 |
| `description` | Body | string | 否  |  -  | 项目描述 |

- **响应说明**:
    - 状态码: `201 Created`


### PUT /api/admin/system/tickets/projects/:id

- **描述**: 更新项目信息
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/system/tickets/projects/:id

- **描述**: 删除项目
- **鉴权**: 是（System Admin · ScopeTickets）
- **响应说明**:
    - 状态码: `204 No Content`


### PATCH /api/admin/system/tickets/projects/:id/archive

- **描述**: 归档或取消归档项目
- **鉴权**: 是（System Admin · ScopeTickets）
- **请求参数**:

| 参数名        | 位置   | 类型   | 必选 | 默认值 | 说明   |
|------------|------|------|:--:|:---:|------|
| `archived` | Body | bool | 是  |  -  | 是否归档 |

- **响应说明**:
    - 状态码: `200 OK`

---


### 用户管理（Users）

### 接口列表

| 方法 | 路径 | 用途 | 鉴权 |
|---|---|---|---|
| `GET`   | `/api/admin/system/users`                              | 列出所有用户            | 是（System Admin · ScopeUsers） |
| `GET`   | `/api/admin/system/users/:user`                        | 获取单用户详情           | 是（System Admin · ScopeUsers） |
| `PATCH` | `/api/admin/system/users/:user`                        | 更新用户记录            | 是（System Admin · ScopeUsers） |
| `POST`  | `/api/admin/system/users/:user/reset-password` | 重置用户密码            | 是（System Admin · ScopeUsers） |
| `POST`  | `/api/admin/system/users/sync`                 | 同步 LDAP 用户        | 是（System Admin · ScopeUsers） |
| `POST`  | `/api/admin/system/users/id-mappings/refresh` | 刷新 UID/GID 名字缓存 | 是（System Admin · ScopeUsers） |
| `GET`   | `/api/admin/system/users/groups`                       | 获取用户组列表           | 是（System Admin · ScopeUsers） |
| `GET`   | `/api/admin/system/users/groups/:group`                | 获取特定组成员           | 是（System Admin · ScopeUsers） |

### GET /api/admin/system/users

- **描述**: 列出所有用户
- **鉴权**: 是（System Admin · ScopeUsers）
- **请求参数**:

| 参数名      | 位置    | 类型     | 必选 | 默认值 | 说明   |
|----------|-------|--------|:--:|:---:|------|
| `limit`  | Query | number | 否  | 50  | 分页大小 |
| `offset` | Query | number | 否  |  0  | 分页偏移 |

- **响应说明**:
    - 状态码: `200 OK`


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
| `terminalAccess` | Body | bool   | 否  |  -  | 终端访问权限 |
| `maxConnections` | Body | number | 否  |  -  | 最大连接数  |

- **响应说明**:
    - 状态码: `200 OK`


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
| `DELETE` | `/api/admin/security/bans`      | 清空所有封禁 | 是（Security Admin · ScopeBans） |
| `DELETE` | `/api/admin/security/bans/:key` | 解除指定封禁 | 是（Security Admin · ScopeBans） |

### GET /api/admin/security/bans

- **描述**: 获取所有封禁记录
- **鉴权**: 是（Security Admin · ScopeBans）
- **响应说明**:
    - 状态码: `200 OK`


### DELETE /api/admin/security/bans

- **描述**: 清空所有封禁
- **鉴权**: 是（Security Admin · ScopeBans）
- **响应说明**:
    - 状态码: `204 No Content`


### DELETE /api/admin/security/bans/:key

- **描述**: 解除指定封禁
- **鉴权**: 是（Security Admin · ScopeBans）
- **响应说明**:
    - 状态码: `204 No Content`


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
| `role` | Body | string | 是  |  -  | 角色标识 |

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
- **响应说明**:
    - 状态码: `200 OK`


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

- **描述**: 按用户名删除所有会话
- **鉴权**: 是（Security Admin · ScopeSessions）
- **请求参数**:

| 参数名        | 位置    | 类型     | 必选 | 默认值 | 说明        |
|------------|-------|--------|:--:|:---:|-----------|
| `username` | Query | string | 是  |  -  | 要清空会话的用户名 |

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

- **描述**: 分页查询执行记录，结果按 `createdAt` 倒序
- **鉴权**: 是（审计管理员 / SuperAdmin）
- **Query 参数**:
    - `serverNode` (string, 可选): 按服务端节点过滤
    - `actionName` (string, 可选): 按 action 名称过滤
    - `status` (int, 可选): 按状态过滤，可选值 1=Pending, 2=Dispatched, 3=Succeeded, 4=Failed, 5=TimedOut, 6=Cancelled
    - `from` (RFC3339, 可选): 创建时间下界
    - `to` (RFC3339, 可选): 创建时间上界
    - `limit` (int, 可选): 每页条数，默认 50，最大 500
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
          "limit": 50,
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
| `GET` | `/api/admin/audit/logs/:name?stream=true` | 审计日志实时流（SSE tail-follow） | 是（Audit Admin · 只读） |

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

### GET /api/admin/audit/logs/:name?stream=true

- **描述**: 审计日志实时流。从文件末尾开始（不推历史），每 **500ms** 轮询新增行并逐行推送；日志轮转（rename + 新建）时自动跟随新文件。
- **鉴权**: 是（Audit Admin · 只读）
- **响应格式**: `text/event-stream`，每帧 `data:` 为一行原始日志文本。

---
