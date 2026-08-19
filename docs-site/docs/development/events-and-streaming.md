---
title: 全局事件与实时流推送
linkTitle: 事件与实时流
weight: 56
description: 基于 HTTP SSE 的全局事件通知流、审计日志增量流与客户端失效驱动契约
---

本文档定义 Design Cloud Engine 面向前端（Web 门户 / 桌面客户端）提供的 Server-Sent Events (SSE) 实时推送协议规范与数据契约。

---

## 1. 核心设计原则

1. **单向事件流 (HTTP SSE 标准)**：系统采用标准的 HTTP `text/event-stream` 单向长连接，复用现有 API 端口与 `Authorization: Bearer <token>` 鉴权体系，规避 WebSocket 握手开销与企业内网网关代理穿透痛点。
2. **数据失效驱动 (Signal-Driven Refresh)**：
   - 推送链路的核心职责是下发**轻量失效信号（Invalidation Signal）**与**用户提醒（Toast）**，而非直接传输大体积业务实体快照；
   - 客户端收到 `refresh` 信号后，通过标准 REST API 按需重拉最新数据。这保证了数据事实源始终收敛在后端数据库，规避客户端本地拼接增量更新造成的状态错乱。
3. **分层长连接管理**：平台将长连接按生命周期隔离为全局流与页面级流，避免单一连接阻塞或资源泄露。

---

## 2. 全局事件流 (`GET /api/events`)

### 2.1 连接管理
- **端点**：`GET /api/events`
- **生命周期**：客户端在用户登录成功后建立连接，保持常驻；用户登出或退出应用时主动断开。
- **断线重连**：网络抖动断开后，客户端应采用指数退避机制自动重连。
- **保活心跳**：服务端每 15 秒下发一次注释行心跳（`: ping`），客户端应忽略注释行；每个实际事件帧附带递增的 `id:` 序列号。

### 2.2 数据契约与载荷结构

SSE 帧由**原生事件名 (`event:`)** 与 **JSON 载荷 (`data:`)** 组成。载荷符合统一的规范契约：

```json
{
  "action": "refresh",
  "targetId": "42",
  "notify": {
    "title": "通知标题",
    "message": "通知详细说明",
    "level": "info"
  },
  "extra": {}
}
```

#### 字段语义说明

| 字段 | 类型 | 必选 | 说明 |
|---|---|:---:|---|
| `action` | string | 是 | 核心操作指令：<br>• `refresh`：数据失效信号，客户端需重拉对应业务数据；<br>• `logout`：账号异地登录或被管理员终止会话，客户端应立即清理 Token 并登出；<br>• `notify`：纯提示消息，无需拉取数据。 |
| `targetId` | string | 否 | 触发变更的实体唯一 ID。存在时表示**局部更新**对应行；为空时表示**全量刷新**当前列表。 |
| `notify` | object | 否 | Toast 弹窗对象（为 `null` 时表示静默数据刷新，界面不弹出 Toast）。包含 `title`、`message` 以及级别 `level` (`info` / `success` / `warning` / `error`)。 |
| `extra` | object | 否 | 业务扩展透传元数据。 |

### 2.3 SSE 事件名与路由规则

服务端按业务领域划分不同的 SSE `event` 路由标签，并实施严格的权限过滤：

| SSE `event` 标签 | 触发场景 | 接收受众 | 客户端标准响应动作 |
|---|---|---|---|
| **`job`** | HPC 作业提交、取消、状态流转 | 作业归属用户 | 若处于作业列表页，按 `targetId` 局部更新或全量拉取作业状态。 |
| **`ticket`** | 工单新建、状态流转、流转审批 | 该工单的报告人与处理人 | 局部刷新或全量重载工单列表。 |
| **`node`** | 集群计算节点上线、下线、失联告警 | 仅限系统管理员 (System Admin) | 更新节点监控列表视图。 |
| **`connection`** | 远程桌面连接建立、终止、代理断开 | 连接归属用户 | 更新对应桌面连接的状态。 |
| **`session`** | 会话过期或管理员强制踢下线 | 目标用户本人 | 捕获 `action="logout"`，执行客户端登出并清理凭证。 |
| **`system`** | 系统维护广播、全局存储/资源超限告警 | 仅限系统管理员 (System Admin) | 弹出系统级通知 Toast。 |

---

## 3. 审计日志增量流 (`GET /api/admin/audit/logs/{name}?stream=true`)

面向审计人员提供实时日志排查能力。

- **端点**：`GET /api/admin/audit/logs/{name}?stream=true`（`{name}` 可选 `audit` 或 `security`）
- **鉴权**：仅限审计管理员（Audit Admin，需具备 `ScopeAudit` 权限）。
- **生命周期**：仅在用户进入审计日志查看页面时建立，离开页面时立即断开。
- **机制与帧格式**：服务端按增量追加形式（Tail-Follow）持续下发文本行，使用默认消息事件（仅 `data:` 行，无自定义 `event` 名），客户端接收字符串直接追加至日志控制台末尾。

---

## 4. 相关参考

- **Dashboard 态势监控流 (`GET /api/dashboard?stream=true`)**：涉及全集群资源采样与版本升级热刷新的专用长连接规范，请参阅专题文档：**[Dashboard 数据架构与流式推送契约](./dashboard.md#4-实时推送流协议-get-apidashboardstreamtrue)**。
