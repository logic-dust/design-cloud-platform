---
title: 分布式后台任务与租约机制
linkTitle: 后台任务
weight: 50
description: Server/Agent 周期性后台循环、状态同步与分布式租约机制
aliases:
  - /docs/development/background-tasks
---

## 概述

系统包含各类由 `time.NewTicker` / `time.NewTimer` 驱动的周期性后台循环任务（Background Loops），用于状态同步、资源清理、心跳检测与分布式互斥。

> [!NOTE]
> 关于多节点缓存一致性、版本号轮询（`cache_versions` 表）与泛型 TTL 缓存决策，请参阅[数据库、事务与缓存](/docs/architecture/domains/storage-and-cache)。

## 后台任务全景表

### 1. Server 控制面任务

| # | 任务名称 | 调度驱动 | 执行周期 | 配置项 / 事实源 | 分布式互斥 (DB Lease) |
|---|---|---|---|---|---|
| 1 | **Server 控制 Leader** | 租约心跳（±20% 抖动） | 默认 15s | `cluster.renew_interval` / `cluster.lease_ttl`（0 用默认，见 [Control 高可用](/docs/architecture/cluster/control-ha)） | ✅ `cluster_server_control_leader` |
| 2 | **节点探活与证书续签** | 静态定时器 | 120s | `cluster.node_check_interval` | ✅ `cluster_node_status_check` |
| 3 | **Session 清理** | `settings` 动态读 | 动态 (默认 10m) | `auth.sessionCleanupInterval` | ✅ `session_cleanup` |
| 4 | **用户账号同步** | `settings` 动态读 | 动态 (默认 24h) | `auth.userSyncInterval` | ✅ `user_sync` |
| 5 | **缓存失效轮询** | 静态节拍 | 15s | 内部固定节拍 | ❌ 各节点独立 (详见[存储与缓存架构](/docs/architecture/domains/storage-and-cache)) |
| 6 | **安全封禁刷盘** | 脏驱动延迟定时器 | 有脏后 30s；脏标记满 10 立即 | 内部固定 `flushInterval` / `flushDirtyThreshold` | ❌ 本地内存 |
| 7 | **封禁记录过期清理** | 静态定时器 | 15m | 内部固定 `pruneInterval` | ❌ 本地内存 |
| 8 | **TTLCache 全局 Janitor** | 静态定时器 | 10m | 内部固定 `JanitorInterval` | ❌ 本地内存 |
| 9 | **Proxy 待定隧道清理** | 惰性与级联 | 30s 节流惰性清理 | 随 Session 清理附带 | ❌ 本地内存 |
| 10 | **执行记录与存储样本清理** | 静态定时器 | 1h | 终态执行记录保留期可配 `cluster.execution_retention_days`（默认 7 天）；非终态滞留 24h 标为 timedOut、不删除（`executionStaleAfter`）；同一循环顺带清理 90 天前的 `storage_reports` 样本 | ✅ `execution_records_prune` |
| 11 | **连接 stopped 行清理** | 静态定时器 | 24h | 固定保留 90 天（`StoppedConnectionRetentionDays`）；**仅**物理删除 DB 中 `status=stopped` 且 `updated_at` 超期的行（释放配额）；不管 running/pending/terminating | ✅ `connection_stopped_prune` |

> [!NOTE]
> 连接对账清理（`connection_cleanup`）属于管理员按需触发的维护动作（`POST /api/admin/system/connections/cleanup`），执行时同样通过 `task_leases` 排他互斥，不作为常驻 Ticker 周期运行；它也会顺带执行一次 stopped 行保留清理。
>
> `connectionReconcileWorker` 是常驻的非 Ticker 工作协程：它按 nodeID 合并 Agent 的 `ConnectionReport`，把连接对账从 Sync 收流路径挪到后台执行（最新报告覆盖旧报告）。它不持有 DB lease（只消费本节点收到的报告）。

### 2. Agent 计算节点任务

| # | 任务名称 | 调度驱动 | 周期 / 采样频率 | 触发机制 / 来源 | 运行特征 |
|---|---|---|---|---|---|
| 1 | **节点心跳 (Heartbeat)** | 静态定时器 | 60s (±20% 抖动) | 配置文件 `HeartbeatInterval` | 单 Agent 独立上报 |
| 2 | **资源指标采集 (Metrics)** | `settings` 动态读 | 动态 (默认 60s) | `agent.monitor.metricsInterval` | 单 Agent 独立上报 |
| 3 | **共享存储探测 (Storage Probe)** | `settings` 动态读 | 动态 (默认 1h) | `storage.probeInterval` | 指定 Probe 节点探测 |
| 4 | **连接席位探测 (Connection Probe)** | `settings` 动态读 | 动态 (默认 60s，±20% 抖动) | `agent.vnc.scanInterval`（0 关闭） | 单 Agent 采集 VNC/XRDP 席位后上报 `ConnectionReport` |

---

## 工作协程（非 Ticker）

| 组件 | 触发方式 | 说明 |
|---|---|---|
| `connectionReconcileWorker` | 每次收到 `ConnectionReport` 唤醒 | 按 nodeID 合并（最新覆盖旧），在 Sync 收流路径之外执行连接对账；队列深度过高时打 Warn 日志 |

---

## 关键规律与分布式互斥

### 使用 `task_lease` 的分布式互斥循环（#1, #2, #3, #4, #10, #11）

当集群部署多台 Server 节点并共享同一数据库时，以下任务必须保证全局单活执行：

- `cluster_server_control_leader`：选出持有 Agent 命令连接的 Server（控制 Leader），保证命令面 Single-Owner；
- `cluster_node_status_check`：标记心跳超时的 Agent 节点为离线，同时巡检全部节点的 `cert_expires_at` 进行 T-90/30/7 阶梯告警与主动下发在线自动续签（L3）；
- `session_cleanup`：清理过期 session 与关联的临时数据；
- `user_sync`：LDAP / 系统账号与本地数据库用户状态同步；
- `execution_records_prune`：按保留期（`cluster.execution_retention_days`，默认 7 天）回收终态执行记录，并顺带清理 90 天前的 `storage_reports` 样本；
- `connection_stopped_prune`：回收 90 天前的 stopped 连接行（释放对应配额）。

这些循环通过数据库的 `task_leases` 表实现基于租约的排他锁，确保同一时刻只有一个 Server 节点执行该任务。此外，按需执行的运维操作如 `connection_cleanup` 亦使用该机制防止并发冲突。

### 纯本地内存与单活循环

不使用 `task_lease` 的循环要么是纯本地内存维护操作（Pending 清理、连接心跳检查、封禁刷盘与过期清理），要么是各 Agent 节点的独立上报行为（心跳、资源采集、存储探针、桌面运行态探测），天然不存在多节点竞争。

---

## 间隔变更生效机制

后台循环的时间间隔根据配置来源具有不同的生效时机：

| 来源 | 生效时机 | 适用场景 |
|---|---|---|
| 硬编码 | 随程序编译固定 | 基础设施关键参数（keepalive、连接健康检查） |
| 配置文件（`config.toml`） | 重启 Server / Agent 生效 | 节点拓扑、通信端口与网络超时等静态环境参数 |
| 数据库 Settings（运行时 API） | 下一个 tick 自动生效 | 运行时可动态调整的业务周期参数 |

从 Settings 读取间隔的循环（如 Server #3 Session 清理、#4 用户同步，Agent #3 存储探针、#4 桌面运行态探测）在每次 tick 时重新读取 Settings 值，变更后会在当前周期结束后以新间隔运行，无需重启进程。

---

## 代码映射

| 业务领域 | 核心入口代码 |
|---|---|
| Server 循环编排 | `internal/server/controlplane/server.go` |
| Leader 选举租约 | `internal/server/controlplane/leader/leader.go` |
| Session 清理任务 | `internal/server/service/session/cleanup.go` |
| 用户同步任务 | `internal/server/service/user/sync.go` |
| 执行记录/存储样本清理 | `internal/server/controlplane/execution_prune.go`、`internal/server/controlplane/storage_prune.go` |
| 连接 stopped 行清理 | `internal/server/service/connection/prune_stopped.go` |
| Connection 报告合并协程 | `internal/server/controlplane/connection_reconcile.go` |
| Agent 循环执行框架 | `internal/agent/telemetry.go` (`PeriodicRunner`) |
| Agent 循环注册与连接 | `internal/agent/agent.go` (`connectLoop`) |
