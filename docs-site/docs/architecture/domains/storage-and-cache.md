---
title: 数据库、事务与缓存
linkTitle: 存储与缓存
weight: 20
description: 数据库选型（PostgreSQL/SQLite）、GORM AutoMigrate 演进、领域 Repo 边界与零 Redis 进程内缓存决策模型
aliases:
  - /docs/development/database
  - /docs/development/cache
---

Design Cloud Engine（DCE）在持久化存储与缓存架构上遵循**简洁、自包含与最小外部依赖**的设计哲学：在多节点生产环境中以 PostgreSQL 作为全局单一事实源，开发与边缘单机部署支持 SQLite；全系统**坚决不引入 Redis 等额外缓存中间件**，通过进程内有界副本与基于数据库全局版本号（`cache_versions`）的轮询机制实现跨节点缓存一致性。

---

## 1. 数据库选型与驱动支持

系统通过 `config.toml` 中的 `[database]` 块配置底层存储引擎：

| 驱动类型 (`database.type`) | 适用场景 | 核心特征与连接参数 |
|:---|:---|:---|
| `postgres` | **生产环境 / 多 Server 高可用集群** | 唯一的集群全局事实来源。支持 `host`, `port`, `user`, `name`, `password` / `password_file`, `ssl_mode`。支持 `pg_trgm` 模糊索引插件。 |
| `sqlite` | **开发测试 / 边缘单机轻量部署** | 嵌入式单文件数据库，默认路径 `/var/lib/design-cloud/dce.sqlite.db`，无任何外部服务依赖。 |

### 连接池安全边界

引擎在启动期对数据库连接池施加硬性安全约束：
- 约束关系：`max_idle_conns <= max_open_conns`。
- 上限控制：`max_open_conns` 限制在 `1..500` 区间内，防止连接风暴拖垮数据库。
- 空闲生命周期：`conn_max_lifetime` 为 0（不限）或 `600..86400` 秒。

---

## 2. 表结构演进与 Repo 边界

### 2.1 启动期自动对齐 (AutoMigrate)

项目**不使用独立的带有版本号的 SQL 增量迁移脚本**，而是在引擎每次启动时由 GORM `AutoMigrate` 自动对齐表结构：
- **执行入口**：`internal/db/db.go` 的 `Store.Migrate()`。
- **模型集中声明**：迁移范围严格限定在 `internal/model` 中显式注册的模型列表；新增数据表必须显式加入该方法。
- **无物理外键约束**：系统全表一律采用**逻辑外键**（字段加 B-Tree 索引 + 业务层显式事务保障），`AutoMigrate` 绝不生成数据库层物理外键，避免在高并发和分布式删除时产生严重行锁争用。
- **索引与扩展增强**：建表后自动在 `cache_versions` 预置主题行；对于 PostgreSQL，若检测到存在 `pg_trgm` 插件，则为 `users.username` 创建 GIN Trigram 快速模糊查询索引。

### 2.2 仓储层边界 (Repository Pattern)

- **`db.Store` 职责收敛**：仅负责物理连接池初始化、事务开启、连接关闭、健康检查与系统级缓存版本维护，绝不承载具体业务 CRUD。
- **领域 Repo 独立封装**：具体业务数据读写由 `internal/db/*.go` 下的各个 `XxxRepo` 承载（如 `UserRepo`、`SessionRepo`、`NodeRepo` 等）。
- **防御性分页规范**：所有面向用户或界面的列表方法 `List(ctx, limit, offset)` 强制要求 `limit > 0`；全量读取必须调用语义显式的 `ListAll` 或 `ListForSync`，杜绝未加限制的 `SELECT *` 导致 OOM。

---

## 3. 缓存决策模型与无 Redis 架构

系统针对缓存提出三个根本判定问题：

```
Q1: 要不要 Cache？
    ├─ 否 ──> 直查数据库或外部事实源（如文件树、工单多条件检索、审计日志）
    └─ 是 ──> 副本仅常驻本机进程内内存 ──> Q2: 多 Server 节点要不要对齐？
              ├─ 要对齐（权威在 PostgreSQL） ──> DB 写事务 + Bump 递增版本号（§3.1）
              ├─ 日常可不对齐（权威在外部命令） ──> 本机 TTLCache / 采样 Cell（§3.2）
              └─ 权威在外部但需全集群清空 ──> 本机清空 + 发送广播清空信号（§3.3）
```

### 3.1 权威在 DB：DB + Bump 机制

当数据权威在 PostgreSQL（如 `settings` 平台配置、`sessions` 会话、`bans` 封禁列表）：
1. **写节点**：在数据库写事务中更新数据，同时在**同一事务**内调用 `BumpTx(topic)` 递增 `cache_versions` 对应主题的版本号；提交后立即清空或重载本机缓存。
2. **读节点**：其他 Server 节点的后台 `Invalidator` 线程（固定 15s 轮询周期）探测到版本号变更，自动丢弃自身的进程内缓存。
3. **下一次读取**：触发 read-through，从数据库加载最新数据并写入本机内存。

### 3.2 权威在外部/采样：纯 TTL 节流

当数据权威在外部命令或探针且更新极高频（如 `bjobs` / `squeue` 调度器作业查询、`lmstat` 许可证查询、节点系统指标采样）：
- 直接采用本地 `TTLCache` 或 `Cell` 机制进行短周期保护（如 5s ~ 30s）。
- 不记录到 `cache_versions` 中，依靠 TTL 自然过期实现最终一致性。

### 3.3 封禁拦截：已持久化记录 vs 内存临时计数

系统处理恶意暴力破解采用双层分工模型：
- **内存失败计数器**：每个客户端 IP/用户的失败尝试计数仅保存在接收请求的本机内存中，累积达阈值前异步批量刷盘。
- **持久化封禁状态**：一旦失败达到封禁阈值，立即向数据库写入封禁记录并触发 `BumpTx("bans")`；全集群所有 Server 节点在下一个轮询周期内即刻同步加载该封禁条目，杜绝跨节点绕过拦截。
