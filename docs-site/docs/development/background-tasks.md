---
title: 后台任务
linkTitle: 后台任务
weight: 40
description: Server/Agent 周期性后台循环、状态同步与分布式租约机制
---

## 概述

系统包含各类由 `time.NewTicker` / `time.NewTimer` 驱动的周期性后台循环任务（Background Loops），用于状态同步、资源清理、心跳检测与分布式互斥。

> [!NOTE]
> 关于多节点缓存一致性、版本号轮询（`cache_versions` 表）与泛型 TTL 缓存决策，请参阅独立专题文档：**[缓存架构与决策模型](./cache.md)**。

## 后台任务全景表

### 1. Server 控制面任务

| # | 任务名称 | 调度驱动 | 执行周期 | 配置项 / 事实源 | 分布式互斥 (DB Lease) |
|---|---|---|---|---|---|
| 1 | **控制面选主** | 租约心跳 | 15s | `cluster.lease_duration` | ✅ `cluster_control_plane_leader` |
| 2 | **节点探活与证书续签** | 静态定时器 | 120s | `cluster.node_check_interval` | ✅ `cluster_node_status_check` |
| 3 | **Session 清理** | `settings` 动态读 | 动态 (默认 10m) | `session.cleanupInterval` | ✅ `session_cleanup` |
| 4 | **用户账号同步** | `settings` 动态读 | 动态 (默认 24h) | `auth.userSyncInterval` | ✅ `user_sync` |
| 5 | **连接超时终止** | `settings` 动态读 | 动态 (默认 480m) | `session.connectionTimeout` | ✅ 单 Server 执行 |
| 6 | **已删除连接物理清理** | `settings` 动态读 | 动态 (默认 24h) | `connection.cleanupInterval` | ✅ 单 Server 执行 |
| 7 | **过期验证码清理** | 静态定时器 | 10m | 内部固定 (超时阈值 30m) | ✅ 单 Server 执行 |
| 8 | **缓存失效轮询** | 静态节拍 | 15s | 内部固定节拍 | ❌ 各节点独立 (详见[缓存机制](./cache.md)) |
| 9 | **安全封禁刷盘** | 静态定时器 | 30s | 内部固定 | ❌ 本地内存 |
| 10 | **TTLCache 全局 Janitor** | 静态定时器 | 10m | 内部固定 `JanitorInterval` | ❌ 本地内存 |
| 11 | **Proxy 待定隧道清理** | 惰性与级联 | 30s 节流惰性清理 | 随 Session 清理附带 | ❌ 本地内存 |

### 2. Agent 计算节点任务

| # | 任务名称 | 调度驱动 | 周期 / 采样频率 | 触发机制 / 来源 | 运行特征 |
|---|---|---|---|---|---|
| 1 | **节点心跳 (Heartbeat)** | 静态定时器 | 60s (±20% 抖动) | 配置文件 `HeartbeatInterval` | 单 Agent 独立上报 |
| 2 | **资源采集 (ResourceReport)** | `settings` 动态读 | 动态 (默认 60s) | `agent.monitor.reportInterval` | 单 Agent 独立上报 |
| 3 | **共享存储探测 (Storage Probe)** | `settings` 动态读 | 动态 (默认 1h) | `storage.probeInterval` | 指定 Probe 节点探测 |

---

## 关键规律与分布式互斥

### 使用 `task_lease` 的分布式互斥循环（#1, #4, #6, #7, #12）

当集群部署多台 Server 节点并共享同一数据库时，以下任务必须保证全局单活执行：

- `cluster_control_plane_leader`：控制面选主，保证命令面 Single-Owner；
- `cluster_node_status_check`：标记心跳超时的 Agent 节点为离线，同时巡检全部节点的 `cert_expires_at` 进行 T-90/30/7 阶梯告警与主动下发在线自动续签（L3）；
- `session_cleanup`：清理过期登录会话与关联的临时数据；
- `user_sync`：LDAP / 系统账号与本地数据库用户状态同步。

这些循环通过数据库的 `task_leases` 表实现基于租约的排他锁，确保同一时刻只有一个 Server 节点执行该任务。

### 纯本地内存与单活循环

不使用 `task_lease` 的循环要么是纯本地内存维护操作（Pending 清理、连接心跳检查、封禁刷盘），要么是各 Agent 节点的独立上报行为（心跳、资源采集、存储探针），天然不存在多节点竞争。

---

## 间隔变更生效机制

后台循环的时间间隔根据配置来源具有不同的生效时机：

| 来源 | 生效时机 | 适用场景 |
|---|---|---|
| 硬编码 | 随程序编译固定 | 基础设施关键参数（keepalive、连接健康检查） |
| 配置文件（`config.toml`） | 重启 Server / Agent 生效 | 节点拓扑、通信端口与网络超时等静态环境参数 |
| 数据库 Settings（运行时 API） | 下一个 tick 自动生效 | 运行时可动态调整的业务周期参数 |

从 Settings 读取间隔的循环（如 #6 Session 清理、#7 用户同步、#13 存储探针）在每次 tick 时重新读取 Settings 值，变更后会在当前周期结束后以新间隔运行，无需重启进程。

---

## 代码映射

| 业务领域 | 核心入口代码 |
|---|---|
| Server 循环编排 | `internal/server/control/server.go` |
| Leader 选举租约 | `internal/server/control/leader/leader.go` |
| Session 清理任务 | `internal/server/service/session/cleanup.go` |
| 用户同步任务 | `internal/server/service/user/sync.go` |
| Agent 循环执行框架 | `internal/agent/telemetry.go` (`PeriodicRunner`) |
| Agent 循环注册与连接 | `internal/agent/agent.go` (`connectLoop`) |
