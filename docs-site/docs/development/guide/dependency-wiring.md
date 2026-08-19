---
title: 依赖装配与 Repo 约定
linkTitle: 依赖装配
weight: 65
description: 说明 composition root、db.Store 与领域 Repo 的职责边界
---

## 总原则

- `cmd/engine`、`internal/server/server.go`、`internal/server/service/service.go` 是主要装配根。
- `db.Store` 只负责数据库基础设施：建连、迁移、关闭，以及迁移内部初始化。
- 业务读写必须挂在对应领域 **Repo**（`XxxRepo`）上，禁止直接挂在 `db.Store` 上。
- 主链路中 `db.NewRepos(store)` 只在 `server.NewServer` 构建一次，然后把 `repos` 分发给 `service.NewContainer`
  ，由后者再分发给下游 service。另有两处独立构建实例：开发引导 `cmd/engine/run_dev.go`（由 `cmd/engine/main.go`
  启动期调用，为种子数据预创建独立 `repos`）与独立 CLI `cmd/engine/commands/node_token.go`（自建 store + repos），
  二者均不参与服务端主链路装配。
- `db.Store` 是唯一保留 `Store` 后缀的类型；业务领域仓储统一使用 `XxxRepo` 后缀，与 `db.Repos` 聚合体字段名对齐。

## `db.Store` 的职责

`db.Store` 允许保留：

- `NewStore(...)`
- `Close()`
- `Migrate()`
- `InitSchema(...)` 等纯表结构或系统级别基础数据（无业务意图）准备。

`db.Store` 禁止包含：

- app/user/session/proxy/node/license 这类业务 CRUD/query 方法
- task lease、control 这类已有领域 Repo 或 infra adapter 归属的业务方法

## Repo 划分

当前 `internal/db` 中的主要领域 Repo 约定如下（`db.Repos` 聚合结构体字段名为 `User`/`Session`/...，类型名为 `XxxRepo`）：

- `AppRepo`: app 与 app-user 关联
- `UserRepo`: user、role、app-user 查询辅助
- `SessionRepo`: session 读写与会话失效（踢人、按用户/ID 删除、过期清理）
- `BanRepo`: 封禁记录持久化（IP/用户封禁落库，`security.BanCache` 的后端）
- `FileRepo`: 下载记录与 file 侧最小用户查询
- `ConnectionRepo`: connection 领域查询与写入
- `NodeRepo`: node、node resource、node 同步
- `LicenseRepo`: license service 配置读写；`license.Service` 依赖 `repos.License`、`r.Dispatcher`、`r.Settings`（动态 TTL）与 `license.WithNodeRepo(repos.Node)`（校验 server 节点），内部持有 status / features 双 `TTLCache`
- `SettingRepo`: runtime setting 落库读写
- `ExecutionRepo`: 记录 Dispatcher 请求与执行任务映射
- `TaskLeaseRepo`: task lease 分布式任务租约（leader 选举等场景的互斥获取）
- `HealthRepo`: DB health / pool stats
- `StorageRepo`: 共享存储监控查询与统计
- `TicketRepo`: 工单查询与统计
- `TicketProjectRepo`: 工单项目关联表
- `CacheVersionRepo`: 缓存版本号读写与原子递增（`cache_versions` 表）

## 装配要求

- `db.NewRepos(store)` 在主链路由 `server.NewServer` 创建一次，作为 `*db.Repos` 传入 `service.NewContainer(repos)`，service
  内部不再重复调用 `NewRepos`。开发引导（`run_dev.go`）与 `node_token` CLI 各自独立构建实例，生命周期互不共享。
- service 构造函数优先接收对应领域 Repo 或极窄接口，而不是 `*db.Store`。
- `leader`、`control` 这类基础设施层也应接领域 Repo 适配对象，禁止依赖 `*db.Store` 承载业务接口。

## 新增 DB 方法时的规则

新增 DB 方法时，按下面顺序判断：

1. 这是建连、迁移、关闭相关吗？
2. 如果不是，它属于哪个领域 Repo？
3. 如果跨多个领域，它是不是应该放到专门的 infra adapter？

- 如果一个新方法找不到清晰领域 Repo 归属，先停下来确认边界，而不是继续把方法加回 `db.Store`。

## 多节点缓存同步

> 详细的缓存四要素决策模型与全系统缓存矩阵见专项设计文档：[缓存架构与决策模型](/docs/development/cache)。

`internal/server/cache/cache.go` 实现了基于 **DB 版本号轮询**的缓存同步机制，用于解耦多节点间的缓存一致性，不引入消息中间件。

### 原理

```
节点 A 写入（settings / session / ban / id_mappings）
  → 同一事务内递增 cache_versions 表对应主题的版本号 (BumpTx)
  → 节点 B 的 Invalidator 按共享节拍（15s）轮询 cache_versions
  → 检测到版本号变化 → 执行注册的失效回调清理本地缓存
```

### 影响范围

需要跨节点协调失效的写路径（**谁在写事务里 `BumpTx`**）：

| 主题 | 写路径触发 |
|:---|:---|
| `settings` | `UpdateByDomain`（settings 域写）；另有 `security.authSettingsCache` 注册同一主题做派生缓存失效 |
| `session` | 删除 / 踢人 / 用户角色或 scope 变更 |
| `ban` | 封禁 / 解封 / 失败计数 |
| `id_mappings` | 管理员 `id-mappings/refresh`（唯一广播触发点）；`/users/sync` 走本机单键失效、不广播 |

各缓存的**原语、TTL 来源与默认值、失效方式**统一维护在
[缓存体系 · 缓存实例清单](../cache.md#缓存实例清单唯一事实源)；此处只保留"谁触发 BumpTx"
这一接线信息，不再重复枚举 TTL（历史上多处枚举已多次不同步）。

### 编程模型

其中两个基于 `cache_versions` 同步的缓存保持各自的特殊实现（`session` 的双索引、`ban` 的异步 flush）。`settings` 与其余纯
TTL 缓存统一使用泛型封装：

```go
// internal/server/cache/cache.go
cache := cache.NewTTLCache[string, MyData](30 * time.Second)
val, err := cache.Get(ctx, key, func(ctx context.Context, k string) (MyData, error) {
    return someRepo.Load(ctx, k)
})
cache.Invalidate(key)  // 写路径主动失效
```

内置 double-check 防并发穿透 + fetch 失败降级 stale。

### 装配

在 `service.NewContainer` 内完成：

```go
r.Invalidator = cache.NewInvalidator(repos.CacheVersion, log)

// 各服务在自身构造函数中注册回调（约定优于配置）
// Settings:  invalidator.Register("settings", func() { s.cache.Clear() })
// Session:   invalidator.Register("session",  func() { cache.clear() })
// Ban:       invalidator.Register("ban",      func() { ReloadFromDB() })
```

### 单节点优化

`len(cfg.Cluster.Servers) <= 1` 时自动跳过轮询，减少不必要的 DB 查询。
