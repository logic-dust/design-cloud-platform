---
title: 执行幂等与重试保证
linkTitle: 执行幂等
weight: 60
description: execution_id 去重、执行记录与跨 Server 重试语义
aliases:
  - /docs/development/cluster/execution-idempotency
  - /docs/architecture/cluster/execution-idempotency
---

## 核心概念

`RunModeSync` 与 `RunModeAsyncTracked` **都会**把 `execution_id` 持久化到 `execution_records` 并据此去重：
`execution_id` 是全部运行模式的唯一幂等键，重复请求命中唯一约束后回查已有结果（执行中返回 in-flight 错误，已终结返回缓存的终态结果）。

### execution_id / action_name / trace_id / X-Request-Id 对比

| 字段             | 生成方                       | 作用域                      | 语义                                    |
|----------------|---------------------------|--------------------------|---------------------------------------|
| `execution_id` | 调用方                       | 全局唯一；全模式持久化 | 执行业务键；所有运行模式下作为幂等去重依据 |
| `action_name`  | 调用方                       | 单次 action                | 操作名称（如 `JOB_SUBMIT`），用于路由与审计          |
| `trace_id`     | execution 请求创建方或上游转发方     | 单次执行                     | 单次执行实例关联 ID，用于日志、审计、结果回传与取消控制         |
| `X-Request-Id` | Portal HTTP 中间件或 HTTP 调用方 | 单次 HTTP 请求               | HTTP 入口关联 ID，只用于访问日志和 API 审计，不参与执行幂等  |

`trace_id` 不参与幂等判断，但会贯穿单次执行实例，用于日志关联、Agent
结果回传和取消控制。`X-Request-Id` 是 HTTP 层相关 ID，不应作为执行 `execution_id` 复用。

## 两条执行路径

| 路径                    | Dispatch 实现       | 调用方行为                     | 是否写 `execution_records` | 是否按 `execution_id` 去重 | Agent `CommandResultAck.persisted` |
|-----------------------|-------------------|---------------------------|-------------------------|-----------------------|------------------------------------|
| `RunModeSync`         | `dispatchSync()`  | 阻塞等待最终结果                  | 是                       | 是                     | 有对应记录时为 `true`                  |
| `RunModeAsyncTracked` | `dispatchAsync()` | 命令发出后立即返回 `Accepted=true` | 是                       | 是                     | 有对应记录时为 `true`                     |

### 同步路径（`RunModeSync`）

1. 先在 DB 创建 `execution_records` 记录（初始状态 `pending`）；`execution_id` 命中唯一约束时回查已有结果（见下文重复请求语义）
2. `Dispatcher` 在 `AgentRegistry` 中按 `trace_id` 创建内存态 pending request
3. 向 Agent 发送 `CommandRequest`，成功后将记录更新为 `dispatched`
4. 阻塞等待 `CommandResult`
5. 收到结果后将记录推进到终态（`succeeded` / `failed`；等待超时标记 `timed_out`；发送失败或取消标记 `failed`），再把结果返回给调用方

### 异步可跟踪路径（`RunModeAsyncTracked`）

1. 先在 DB 创建 `execution_records` 记录，初始状态为 `pending`
2. 成功发送命令后，将状态更新为 `dispatched`
3. 立即返回 `{Accepted: true, Success: true}`
4. Agent 后续上报结果时，Server 通过 `trace_id` 把记录推进到终态
5. 相同 `execution_id` 的重复请求命中唯一约束后，会走 DB 查找并返回已有结果或 `errInFlight`

> 这里的去重保证是“同一 `execution_id` 最多进入一次派发流程”，不等于外部副作用恰好执行一次。记录先写入数据库，再发送 Agent 命令；若进程在创建记录后、结果落库前崩溃，该记录会留在 `pending` 或 `dispatched`。每小时清理会把 `updated_at` 超过 24 小时仍非终态的行标为 `timedOut`（`system_error` 说明 abandoned），**不删除**，避免同 ID 重试被当成从未执行而再次派发。之后同 ID 回查得到终态失败；真正重做必须换新的 `execution_id`。若命令已经产生外部副作用、结果却没持久化，Server 记录也无法判断外部系统是否已完成。

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
    exit_code     INTEGER,
    system_error  TEXT,
    finished_at   TIMESTAMP,
    created_at    TIMESTAMP,
    updated_at    TIMESTAMP
);
```

> `ExecutionRecord` 当前**不持久化** `stdout`、`stderr` 或 `duration`，也**不持久化**布尔 `success` 列——成功与否由 `status` 派生（`status == succeeded`）。这些字段只存在于实时的 gRPC `CommandResult` /
`ExecuteResponse` 中；从 DB 恢复重复请求结果时，可回放的是 `trace_id`、`status`、`exit_code`、`system_error` 等持久化字段。

## 状态机

两种运行模式共用同一个持久化状态机；区别在于同步路径在等待结果后同步推进终态，异步路径由 Agent 结果上报驱动。

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

- `dispatchAsync()` / `dispatchSync()` 创建记录后，发送成功则调用 `MarkDispatched()`
- `handleCommandResult()` 调用 `MarkTerminal()`：
    - 命令成功（`CommandResult.success=true`）→ `succeeded`
    - 失败且 `system_error=="timeout"` → `timed_out`
    - 失败且 `system_error=="cancelled"` → `cancelled`
    - 其他失败 → `failed`
- 若命令尚未发出就发送失败，`dispatchAsync()` / `dispatchSync()` 会直接把记录标记为终态失败；同步路径等待结果超时标记 `timed_out`

## 重复请求处理语义

两种运行模式一致：

### 执行中的重复请求（in-flight）

```
T0  Server1 收到 execution_id=X → 创建 ExecutionRecord(status=pending)
T1  Server2 收到 execution_id=X → Create 命中唯一约束
T2  Server2 查库发现 status=pending/dispatched → 返回 errInFlight
T3  Agent 执行完毕 → status → succeeded
```

### 执行完成后的重复请求

```
request arrives → look up execution_id in DB
  → status in {succeeded, failed, timed_out, cancelled}
  → return cached terminal result immediately
```

返回的缓存结果来自 `lookupActionResult()`，当前包含：

- `execution_id`
- `trace_id`
- `success`（由 `status == succeeded` 派生，非持久化列）
- `exit_code`
- `system_error`

结果默认长期保留，不设 TTL。

### HTTP 客户端重试仍是 at-least-once

服务端幂等保证的是“同一 `execution_id` 不重复执行”。但 Portal API 契约没有幂等键入参，HTTP 客户端每次重试都会生成**全新的 `execution_id`**，因此从客户端视角整体语义为 at-least-once。例如当前调度器的 `runSchedulerAction()` 每次调用都生成新的 `execution_id`，并沿用 `NewExecuteRequest` 的默认 `RunModeSync`：DCE 不会跨 HTTP 请求合并或去重作业提交，每次用户提交都按一次独立操作处理，LSF / Slurm 接受作业并返回 job ID 后即完成提交。若通信超时发生在调度器接受作业之后，调用方可能暂时拿不到 job ID；是否重新提交由调用方按业务需要决定，重复提交会创建另一个作业。

若将来产品要求“客户端安全重试”（同一业务请求的重试返回原 job ID），需要单列契约变更：Portal API 增加可选 `Idempotency-Key`，引擎以其替代内部生成的 `execution_id`——该项按破坏性变更流程独立评审，不在当前幂等实现范围内。

## 构造 execution_id

需要对同一业务操作去重时，调用方必须为该操作生成稳定的业务键，并在重试时复用；不能用每次请求都会变化的时间戳或随机 ID。`RunModeAsyncTracked` 只适用于调用方接受立即得到 `Accepted`、之后查询状态的接口，而且当前只持久化状态和少量结果字段，不持久化 stdout / stderr。

```go
// stableOperationID 应由业务操作标识组成，并在该操作的所有重试中保持不变。
executionID := "operation-" + stableOperationID
req := execution.NewExecuteRequest(
	actionName,
	executionID,
	execution.WithTargetUser(username),
	execution.WithPayload(payload),
)
req.Mode = execution.RunModeAsyncTracked
```

像当前调度器的作业提交接口需要同步读取 stdout 并解析作业 ID，因此不能直接照搬这个异步示例。若该类接口要支持可靠重试，需要另外设计业务键复用和结果恢复。

`execution_id` 应在不同业务操作之间全局唯一。同一操作的重试复用原 ID；一次全新的执行使用新 ID。

## 多 Server 场景

共享数据库是全模式幂等的基础：

**场景：客户端在不同 Server 上重试**

```
Client → Standby Server: execution_id=X
Standby → Leader: ForwardDispatch(execution_id=X)
Leader → DB: Create / lookup execution_records
```

关键语义：

- `execution_id` 的唯一约束由共享数据库强制，所有 Server 实例共享同一个 `execution_records` 表
- Standby 转发失败时先做**有界重试**（刷新 Leader 视图后重试 1 次，仅对 `Unavailable` / `DeadlineExceeded` / `FailedPrecondition`）；仍失败则按 `execution_id` 回查 DB——覆盖全部运行模式（转发可能已到达 Leader 并完成执行）
- 该回查只会恢复**已完成的终态结果**；若记录仍处于 `pending/dispatched`，则不会伪造同步成功响应
- 恢复结果只包含 outcome 字段（`status` 派生的 `success` / `exit_code` / `system_error`）：`execution_records` 不持久化 stdout/stderr，因此恢复出的响应中输出为空。依赖 stdout 解析业务标识（如调度器 job ID）的调用方不得把空输出当作成功载荷；引擎在该路径上会打印 WARN 日志说明输出缺失
- 回查仍无结果时返回 503（`Unavailable`），由调用方决定是否重试（HTTP 层重试语义见上节）
