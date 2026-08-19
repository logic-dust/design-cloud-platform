---
title: 执行幂等
linkTitle: 执行幂等
weight: 90
description: execution_id 去重、执行记录与跨 Server 重试语义
---

## 核心概念

当前代码中，**只有 `RunModeAsyncTracked` 会把 `execution_id` 持久化到 `execution_records` 并据此去重**。`RunModeSync` 也会携带
`execution_id`，但只用于协议透传与日志关联：不写库、不查库，也不提供跨请求幂等。

### execution_id / action_name / trace_id / X-Request-Id 对比

| 字段             | 生成方                       | 作用域                      | 语义                                    |
|----------------|---------------------------|--------------------------|---------------------------------------|
| `execution_id` | 调用方                       | 全局唯一；仅 async-tracked 持久化 | 执行业务键；`RunModeAsyncTracked` 下作为幂等去重依据 |
| `action_name`  | 调用方                       | 单次 action                | 操作名称（如 `JOB_SUBMIT`），用于路由与审计          |
| `trace_id`     | execution 请求创建方或上游转发方     | 单次执行                     | 单次执行实例关联 ID，用于日志、审计、结果回传与取消控制         |
| `X-Request-Id` | Portal HTTP 中间件或 HTTP 调用方 | 单次 HTTP 请求               | HTTP 入口关联 ID，只用于访问日志和 API 审计，不参与执行幂等  |

`execution_id` 只在 async-tracked 路径上参与持久化去重。`trace_id` 不参与幂等判断，但会贯穿单次执行实例，用于日志关联、Agent
结果回传和取消控制。`X-Request-Id` 是 HTTP 层相关 ID，不应作为执行 `execution_id` 复用。

## 两条执行路径

| 路径                    | Dispatch 实现       | 调用方行为                     | 是否写 `execution_records` | 是否按 `execution_id` 去重 | Agent `CommandResultAck.persisted` |
|-----------------------|-------------------|---------------------------|-------------------------|-----------------------|------------------------------------|
| `RunModeSync`         | `dispatchSync()`  | 阻塞等待最终结果                  | 否                       | 否                     | 通常为 `false`                        |
| `RunModeAsyncTracked` | `dispatchAsync()` | 命令发出后立即返回 `Accepted=true` | 是                       | 是                     | 有对应记录时为 `true`                     |

### 同步路径（`RunModeSync`）

1. `Dispatcher` 在 `AgentRegistry` 中按 `trace_id` 创建内存态 pending request
2. 向 Agent 发送 `CommandRequest`
3. 阻塞等待 `CommandResult`
4. 收到结果后直接返回给调用方

特点：

- **不创建 `ExecutionRecord`**
- **不对重复 `execution_id` 做去重**
- Agent 结果到达时，Server 仍会回 `CommandResultAck`，但若没有 async-tracked 记录，`persisted=false`

### 异步可跟踪路径（`RunModeAsyncTracked`）

1. 先在 DB 创建 `execution_records` 记录，初始状态为 `pending`
2. 成功发送命令后，将状态更新为 `dispatched`
3. 立即返回 `{Accepted: true, Success: true}`
4. Agent 后续上报结果时，Server 通过 `trace_id` 把记录推进到终态
5. 相同 `execution_id` 的重复请求命中唯一约束后，会走 DB 查找并返回已有结果或 `errInFlight`

## 数据库表结构

```sql
CREATE TABLE execution_records
(
    id            BIGINT PRIMARY KEY,
    execution_id  VARCHAR(128) UNIQUE NOT NULL,
    action_name   VARCHAR(64),
    trace_id      VARCHAR(128) UNIQUE NOT NULL,
    server_node   VARCHAR(64)         NOT NULL,
    target_node   VARCHAR(64),
    status        INTEGER             NOT NULL, -- 1=pending 2=dispatched 3=succeeded 4=failed 5=timed_out 6=cancelled
    success       BOOLEAN,
    exit_code     INTEGER,
    system_error  TEXT,
    finished_at   TIMESTAMP,
    created_at    TIMESTAMP,
    updated_at    TIMESTAMP
);
```

> `ExecutionRecord` 当前**不持久化** `stdout`、`stderr` 或 `duration`。这些字段只存在于实时的 gRPC `CommandResult` /
`ExecuteResponse` 中；从 DB 恢复重复请求结果时，可回放的是 `trace_id`、`success`、`exit_code`、`system_error` 等持久化字段。

## Async-tracked 状态机

仅 `RunModeAsyncTracked` 进入下面的持久化状态机；`RunModeSync` 不会创建这些状态。

```
         ┌──────────┐
         │ pending  │ ← ExecutionStore.Create
         └────┬─────┘
              │ SendCommand to agent
              ▼
         ┌──────────────┐
         │  dispatched  │
         └──────┬───────┘
                │ agent finishes
     ┌──────────┼──────────┬──────────┐
     ▼          ▼          ▼          ▼
┌─────────┐ ┌────────┐ ┌──────────┐ ┌──────────┐
│succeeded│ │ failed │ │timed_out │ │cancelled │
└─────────┘ └────────┘ └──────────┘ └──────────┘
         (terminal states)
```

状态推进规则：

- `dispatchAsync()` 创建记录后，发送成功则调用 `MarkDispatched()`
- `handleCommandResult()` 调用 `MarkTerminal()`：
    - `success=true` → `succeeded`
    - `success=false` 且 `system_error=="timeout"` → `timed_out`
    - `success=false` 且 `system_error=="cancelled"` → `cancelled`
    - 其他失败 → `failed`
- 若命令尚未发出就发送失败，`dispatchAsync()` 会直接把记录标记为终态失败

## 重复请求处理语义

### Async-tracked：执行中的重复请求（in-flight）

```
T0  Server1 收到 execution_id=X → 创建 ExecutionRecord(status=pending)
T1  Server2 收到 execution_id=X → Create 命中唯一约束
T2  Server2 查库发现 status=pending/dispatched → 返回 errInFlight
T3  Agent 执行完毕 → status → succeeded
```

### Async-tracked：执行完成后的重复请求

```
request arrives → look up execution_id in DB
  → status in {succeeded, failed, timed_out, cancelled}
  → return cached terminal result immediately
```

返回的缓存结果来自 `lookupActionResult()`，当前包含：

- `execution_id`
- `trace_id`
- `success`
- `exit_code`
- `system_error`

结果默认长期保留，不设 TTL。

### Sync：不做持久化幂等

`RunModeSync` 每次调用都会重新下发命令；即使 `execution_id` 相同，也不会命中 DB 去重，因为同步路径没有 `ExecutionRecord`。

## 构造 execution_id

若调用方需要幂等重试语义，除了设置稳定的 `execution_id`，还需要显式使用 `RunModeAsyncTracked`；默认模式仍是 `RunModeSync`。

```go
// 方式一：自动生成 execution_id，并启用 async-tracked（推荐）
req := execution.NewExecuteRequest(
	execution.ActionJobSubmit,
	"",
	execution.WithTargetUser(username),
	execution.WithPayload(payload),
)
req.Mode = execution.RunModeAsyncTracked

// 方式二：显式业务键（自定义幂等场景）
executionID := fmt.Sprintf("job-%s-%d", jobID, time.Now().UnixNano())
req := execution.NewExecuteRequest(
	execution.ActionJobSubmit,
	executionID,
	execution.WithTargetUser(username),
	execution.WithPayload(payload),
)
req.Mode = execution.RunModeAsyncTracked
```

`execution_id` 应保持全局唯一。若本次请求本质上是一次新的执行（例如允许重新派发到其他节点），应使用新的 `execution_id`。

## 多 Server 场景

共享数据库是 **async-tracked** 幂等的基础：

**场景：客户端在不同 Server 上重试**

```
Client → Standby Server: execution_id=X
Standby → Leader: ForwardDispatch(execution_id=X)
Leader → DB: Create / lookup execution_records
```

关键语义：

- `execution_id` 的唯一约束由共享数据库强制，所有 Server 实例共享同一个 `execution_records` 表
- Standby 转发失败后，只有 `RunModeAsyncTracked` 才会尝试按 `execution_id` 回查 DB
- 该回查只会恢复**已完成的终态结果**；若记录仍处于 `pending/dispatched`，则不会伪造同步成功响应
- `RunModeSync` 没有持久化记录，因此不存在跨 Server 的幂等恢复能力
