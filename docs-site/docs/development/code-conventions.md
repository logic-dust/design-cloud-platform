---
title: 代码规范与工程约束
linkTitle: 代码规范
weight: 20
description: 项目级约束、代码规范与架构协作边界
aliases:
  - /docs/development/guide/code-conventions
---

Design Cloud Engine 的项目级开发规范如下。本文既面向人类开发者，也作为 AI agent 的代码库约束说明。

## 目标

- 优先保持代码和配置语义简单、稳定、可读。
- 优先做根因修复，避免引入额外包袱。
- 变更代码时，同步更新示例配置、测试和相关文档。

## 架构边界

### Engine 与 DcExec

- `cmd/engine` 是主进程，负责编排 server/agent 能力。
- `cmd/exec` 只负责受控的本地特权执行。
- 需要节点本地能力的动作，优先遵循 `Service -> Control -> Agent` 链路，由 Agent 再决定 direct 还是 dc-exec。
- `dc-exec` 只处理需要身份切换或特权能力的动作，不承担普通本地执行。
- 纯共享状态、数据库读写、Portal 会话、control-plane 事件，以及像 LDAP 这类由 Service 直接完成的外部集成，才应直接在 Engine
  内处理。

### Cluster / Server / Portal / Control / Agent

- Cluster 表示节点拓扑与通信参数（配置段 `[cluster]`，struct `config.Cluster`）。
- Server 是服务端节点角色，承载 Portal、Control、Audit、领域 Service 与 Application 用例；由 `internal/server/assembly.go` 统一装配。
- Portal 是对外入口，位于 `internal/server/portal/`，专注于协议处理。
- Control 是 Server 侧对 cluster 协议的控制实现，位于 `internal/server/controlplane/`。
- Execution Layer 位于 `internal/execution/`，承载任务协议、Operation 编排与 Payload 定义。
- Agent 是执行端节点角色，承载 ClusterAgent、执行器与 dc-exec 协作能力。
- Control 作为 cluster 协议服务端，Agent 作为 cluster 协议客户端主动连接 Control。
- Agent 角色在全节点无条件常驻运行，承载本地工作载荷、监控采集与系统鉴权。
- 一个节点若匹配 `cluster.servers`，则同时运行 Server 角色。

## 配置规范

- 配置文件唯一主入口是 `config.toml`。
- 节点 Server 角色仅由本机主机名是否匹配 `cluster.servers` 决定；Agent 角色全节点常驻运行。
- 节点身份（NodeID）严格等于 `os.Hostname()`，不可通过配置文件或环境变量覆盖。Windows 上该值是 Physical DNS 主机名，通常不等于 `$env:COMPUTERNAME`。
- Cluster 协议使用 mTLS。
- 敏感信息优先使用 `*_file` 形式；例如数据库密码优先使用 `database.password_file`。
- 配置模型采用 TOML。
- 不要新增通用 env 覆盖入口，除非有明确批准。仅 `//go:build dev` 文件内的、有明确本地调试用途的环境变量不受此限制。

## 架构与代码原则 (AI Constraints)

1. **KISS & YAGNI 优先 (适度前瞻)**:
   坚决避免为了"未来有可能遇到的复杂场景"
   而提前引入过度设计，或盲目预留冗杂的扩展点。鼓励代码保持逻辑显式化、平铺直叙；但在符合既有架构边界的前提下，合理的职责分离与必要的、轻量级的接口抽象依旧是必须的。
2. **克制 DRY (Don't Repeat Yourself)**: 允许在项目发展初期存在少量的（1-2次）代码片段重复。
    - **重构红线**：仅当代码出现 **3 次及以上**重复调用，且这些代码块属于**同一业务语义**
      （绝非巧合的结构相似，如订单校验与登录校验）时，才允许将其重构合并为公共抽象。强行抽象"长得像但不相干"的代码会导致灾难性的逻辑耦合。
3. **务实的依赖注入 (DI)**: 叶子节点（如数据库 Repo、细分 service）必须使用极窄的特定参数注入以保持纯净解耦。但对于顶层的总编配环节（如
   server.NewServer），允许直接传入全局 Config 指针（`*config.Config`）。**强烈反对**为了表现出形式上的 DI
   而去创造无意义的庞大镜像结构体（Mirror Struct）来进行配置内容的简单拷贝。
4. 修改代码时遵循现有包边界和命名风格，不做无关重构。

### Service DTO 与 Model 复用边界

- Handler 层请求体优先绑定到 `internal/server/service` 暴露的 DTO，不要在 API 方法里长期保留匿名 `struct`。
- **允许** 使用 `type X = model.YBase` 的场景：`Create/Update` 这种"整对象写入"语义，且字段集合与模型可写基座完全一致。
- **禁止** 使用 `model` 别名复用的场景：`Patch`（可选字段/部分更新）、`Action/Command`（命令型接口）、带默认值合成或策略开关的请求。
- 对于禁止场景，必须定义独立 service DTO，并在 service 内显式做转换（例如 `ToRequest()`）以承载默认值与策略语义。

### `internal/contract` — 跨层中立契约与 DTO

`internal/contract` 是独立于 DB 模型 (`internal/model`) 和 service 层 DTO 的中立跨层契约包。它承载以下维度的抽象：

- **跨组件中立数据契约**：在 agent ↔ control ↔ service 等跨越多个系统边界的链路中传递的数据结构（如 `VNCSeatInfo`、`ConnectionReport`）。
- **中立解耦接口**：如 `CollectTrigger` / `CollectTarget` 抽象接口，使 `control` 面与 `service` 面在解耦的前提下协作。
- **特定领域契约子包与接口**：
    - `internal/contract/proto`：gRPC 消息与服务定义（`cluster.proto` 及其生成的代码）。
    - `internal/contract/service.go`：跨层解耦接口（如 `AlertEvaluator`、`CollectTrigger`、`AgentSettingsProvider`）；配置 Schema 位于 `internal/contract/schema/`。

与 service 层 DTO 的区别：

- **service DTO** 定义在 `internal/server/service/*/` 内，面向业务服务内部流转与 API handler 的请求/响应绑定。
- **`internal/contract`** 面向系统组件间跨界通信与契约解耦，不绑定具体业务实现或 HTTP 协议。

与 DB model 的区别：

- **DB model** (`internal/model`) 定义 GORM 持久化结构体，与数据库表结构一一对应。
- **`internal/contract`** 仅承载运行时契约与中立 DTO，无 GORM tag、无 `TableName()`，不与数据库存储直接耦合。

### Portal Handler 依赖约定

- `internal/server/portal/api.Handler` 作为顶层路由装配入口，经 `api.Dependencies` 接收显式依赖集合。
- 新增或重构具体业务路由时，优先拆出私有窄 handler，例如 `systemHandler`、`settingsHandler`、`nodeHandler`。
- 私有 handler 只声明自身需要的最小接口；不要把完整 `api.Dependencies` 继续传入具体业务 handler。
- 路由装配方法负责从 `api.Handler` 中取出依赖并构造私有 handler；业务处理方法挂在私有 handler 上。
- 涉及 config 的 handler 只传入实际需要的配置值或路径解析函数，避免为单个 handler 镜像整个 `config.Config`。
- 新拆出的 handler 应补一组轻量 HTTP 测试，使用 fake 窄接口验证路由行为和错误映射。

### 命名约定

- **DB 层**：`db.Store` 是唯一保留 `Store` 后缀的类型（数据库基础设施：连接池、迁移）。业务领域仓储统一使用 `XxxRepo` 后缀（如
  `UserRepo`、`SessionRepo`），与聚合体 `db.Repos` 字段名对齐。仓库内不再存在业务领域仓储的 `Store` 命名。
- **Service 层**：业务领域服务统一以 `Service` 结尾（如 `user.Service`、`session.Service`）；service 间依赖字段统一命名为
  `<domain>Svc`（如 `userSvc`、`auditSvc`），避免 `Mgr`/`Service`/裸名混用。
- **Manager 语义**：`Manager` 用于有状态生命周期管理的编排组件（内部编排角色），如 `session.AuthManager`（编排认证流程）、
  `agent.Manager`（管理 Agent 生命周期）。`Service` 与 `Manager` 可在同一包内共存，只要职责边界清晰。
- **Handler 层**：Portal 私有业务 handler 统一以 `xxxHandler` 结尾（如 `overviewHandler`、`sessionHandler`）；顶层
  `NewHandler` 只接收 `api.Dependencies` + `*config.Config` + `*slog.Logger` 用于装配，**禁止**把 `api.Dependencies`
  向下穿透到具体业务 handler（sub-handler 通过工厂函数从依赖集合提取所需窄依赖）。
- **Control 层**：请求分发组件命名为 `Dispatcher`（`internal/server/controlplane`），不叫 `DispatchService`，以免与 `service` 层
  `Service` 概念混淆；其职责是请求分发，不是业务服务。
- **内存缓存**：`security.BanCache` 是封禁记录的内存缓存（由 `db.BanRepo` 负责持久层），不叫 `BanStore`/`MemoryBanStore`
  ，避免与 DB 层 `Repo` 概念混淆。
- **系统状态聚合配置**：`internal/server/application/system.Service` 为生成系统状态快照持有只读 `*config.Config`，是当前唯一例外。该例外属于 application 聚合查询，不是领域 Service 的通用先例；领域 Service 仍优先接收窄配置或配置接口（如 `node.Config{NodeID, CertDir}`）。

### API 响应结构选型（序列化边界）

- `internal/server/portal/api.Response` 保持非泛型定义：`type Response struct { Data any; Message string }`。
- **理由**：API 响应是 JSON 序列化边界，`c.JSON()` 内部调用 `json.Marshal(v)`，`Data` 在此层必然退化为 `any`（接口类型）；即便使用
  `Response[T any]`，序列化后的 JSON 结构与前端契约完全一致，泛型不带来运行时收益。
- **禁止使用 `Response[T any]` 泛型化**：Go 泛型类型不能裸用（`Response{...}` 必须带类型参数），全量迁移涉及 90+
  处调用点且无法渐进（类型别名 `type Response = Response[any]` 与泛型类型同名冲突）。该改动属于伪需求，已明确延期并转为本规范。
- 真正需要类型安全的地方在 handler 内部（入参绑定、DB 查询、业务传参），而非出网那一跳。
- 若需减少重复，可提取 `Handler.ok(c, data)` / `Handler.fail(c, code, msg)` 辅助方法统一封装，而非引入泛型。

## 现代 Go 特性与代码规范 (Go 1.27+)

项目使用最新稳定版 (Current Stable, 至少 1.27+) 的特性，拒绝过时的写法。

### JSON（`encoding/json/v2`）

- 业务与 GORM `serializer:json` 统一经 `internal/platform/jsoncodec`（内部为 `encoding/json/v2`，并对 nil slice/map 保持历史 `null` 写出）。
- 禁止在 `internal/` / `cmd/` 业务代码中直接 `import "encoding/json"` 编解码载荷（生成的 `*.pb.go` / `protojson` 除外）。

### 1. 结构化日志 (`log/slog`)

服务逻辑强制使用结构化日志（使用 `internal/platform/logger` 的全局方法如 `logger.Warn("msg", "key", val)`）。避免在热点循环中频繁调用
`logger.WithContext()` 创建临时日志对象。CLI、生成命令、启动提示等允许使用 `fmt` 输出。禁止使用字符串拼接代替字段键值对。

### 2. 类型安全的错误断言 (`errors.AsType`)

Go 1.27 提供了泛型版本的高性能错误断言，项目统一使用 `errors.AsType`。

```go
if execErr, ok := errors.AsType[*execution.ExecutionError](err); ok { /*...*/ }
```

### 3. 迭代器与序列 (`iter` & `maps` & `slices` & `strings`)

优先使用迭代器模式处理集合与长字符串，减少中间分配。

```go
for k := range maps.Keys(myMap) { /*...*/ }
for line := range strings.SplitSeq(content, "\n") { /*...*/ }
```

### 4. 泛型与比较 (`cmp`)

优先使用 `cmp.Or` 简化默认值逻辑（例如 `port := cmp.Or(data.Port, 8080)`）。同时，在 `slices.SortFunc` 等排序逻辑中，**必须
**使用 `cmp.Compare` 代替手写的 `if/switch` 比较。

```go
slices.SortFunc(cmds, func (a, b command) int { return cmp.Compare(a.name, b.name) })
```

### 5. 指针初始化 `new()`

在使用指针类型（特别是 JSON 的 `omitempty` 或 Protobuf 通讯参数）时，推荐使用 `new(T)` 来直接分配基础类型指针。绝对不能传入字面量或常量（如
`new(0)` 编译会报错）。

### 6. Web 框架 (Echo v5)

项目已升级至 `github.com/labstack/echo/v5`。与 v4 版本有重大 API 差异：

- **处理器签名变更**: Handler 必须返回 `error`，**不能**直接返回 `nil` 而不是调用 `c.JSON()`。
- **分组路由**: 必须正确传递 `*echo.Group`，而不是返回。

### 7. 错误处理哲学

1. **显式处理**: 必须接收所有 `error` 返回值。如果确信可以忽略，必须显式赋值给 `_`。
    - **对于非 defer 语句的独立调用**：如一行孤立的 `tcpConn.Close()` 也会隐式忽略错误，也必须写死为 `_ = tcpConn.Close()`
      或正确检查错误。
    - **对于 `defer` 语句的特例说明**：形如 `defer f.Close()` 的调用会隐式忽略错误，这违反了明文显式忽略的原则。如果是只读文件或不关心关闭结果的连接，
      **必须**写为 `defer func() { _ = f.Close() }()`；对于写入关键数据的文件，则应在普通流程中显式 `Close` 并检查错误，而不是仅依赖
      `defer`。
2. **Wrap 优于 String**: 使用 `fmt.Errorf("...: %w", err)` 保留错误链。
3. **Fail Fast**: 尽早检查错误并返回，避免嵌套 `else`。
4. **统一校验**: 所有 `internal/execution.ExecuteRequest` 在处理前必须调用 `r.Validate()` 方法。该方法已在
   `internal/execution/execution.go` 中统一定义，包含对 TraceID 和 ActionName 等核心字段的检查。
5. **统一领域错误体系 (`contract.DomainError`)**:
   业务与领域服务层错误统一使用 `internal/contract` 提供的语义化构造器（如 `contract.NewNotFoundError`、`contract.NewForbiddenError`、`contract.NewUnauthorizedError`、`contract.NewValidationError`、`contract.NewInvalidParamError`、`contract.NewConflictError`、`contract.NewUnavailableError` 等）。
   - **Portal HTTP 层**：`httputil.ClassifyError` 自动提取其 HTTP 状态码与安全提示，无需在具体 Handler 中手写 `echo.NewHTTPError`；
   - **Control gRPC 层**：拦截器 `domainErrorUnaryServerInterceptor` 自动将其映射为对应的标准 gRPC Status Code。

## 安全架构与决策指南

本项目采用 **双进程隔离模型** (Engine + dc-exec)。在编写涉及系统操作的代码时，**必须**先进行安全决策分析。

### 决策流程图

1. **这个操作需要修改系统状态吗？**
    * **NO** (只读/计算) -> 进入下一步
    * **YES** -> 进入下一步
2. **这是共享状态 / 数据库 / Portal / control-plane 自身逻辑，或由 Service 直接完成的外部集成吗？**
    * **YES** -> **Engine 本地处理**
    * **NO** -> 进入下一步
3. **这是节点本地能力吗？** (如文件、作业、许可证、本地用户视图、节点侧命令)
    * **YES** -> **派发到 Agent**
    * **NO** -> 回到具体业务判断，但不要绕过既有包边界
4. **Agent 普通权限可以完成吗？**
    * **YES** -> **Direct Executor**
    * **NO** -> 进入下一步
5. **需要身份切换或特权能力吗？**
    * **YES** -> **dc-exec Executor** (由 Agent 调用 `dc-exec`)
    * **NO** -> 优先保持为 **Direct Executor**

> [!NOTE]
> **关于 Admin 操作的身份机制**
> 在设计 Admin API 时，必须区分操作对象：如果操作的是底层系统资源（如替其他用户操作作业、读写文件），必须通过 `dc-exec` 进行
**身份切换（Impersonation）**，以目标用户的身份执行。如果操作的是 Portal 自身的平台元数据（如审批文件下载、修改全局设置），则直接在
> Engine 本地处理，记录 Admin 自身的身份，**禁止进行无谓的底层身份切换**。

### 安全约束

- 不要把 CA 私钥、数据库、日志、证书、构建产物提交到仓库。
- 涉及认证、session、cluster、dcexec、node 注册的改动，默认按高风险修改处理。
- 对外部系统集成优先做"观察与接入"，避免把项目变成外部系统的控制器，除非这是明确目标。

- 测试优先通过包进行验证，例如：
    - 配置加载验证：`go test ./internal/platform/config/...`
    - 数据库操作验证：`go test ./internal/db/...`
    - 平台底座与契约验证：`go test ./internal/platform/... ./internal/contract/...`
- 影响启动链路、API、Cluster、dc-exec 时，继续补相关包测试或脚本验证。
- 执行集成测试前，确保已按照 [开发环境](./dev-environment.md)（别名 `/docs/development/guide/dev-environment`）配置好环境。

## 状态管理与缓存机制

编码时的简要纪律：

1. **读多写少的配置**：读穿透 + 写后主动失效本地缓存；**禁止**用 `Ticker` 定时全表扫库刷新配置。
2. **后台 `Ticker`**：仅用于需要主动改写持久化状态或垃圾回收的循环（清单见[后台任务](../architecture/domains/background-tasks.md)）。
3. **具体实例、DB+Bump / 仅 TTL、`cache_versions` 与 TTL 数值**：以[存储与缓存](../architecture/domains/storage-and-cache.md)为唯一事实源，不在本页重复列举。

## 文档边界

- `docs-site/docs/` 是项目事实和开发/部署文档的来源；`.agents/` 保存按需加载的 Agent 规则、技能与验证工具。
- 代码、Schema、配置和构建文件是实现事实源；冲突时先核实代码，再修正文档。

## Agent Harness

`.agents/` 当前结构：

- `rules/`：按任务加载的项目约束
- `skills/`：专项任务 SOP
- `verifiers/`：构建、测试、安全与架构验证
- `scripts/`：扫描和辅助工具

从根目录 `AGENTS.md` 与 `.agents/README.md` 进入规则路由。只读取当前任务相关的规则与技能。

API 路由或 Handler 变更时同步更新 `test/portal/*.http`，并运行 `.agents/scripts/scan_portal_api.py` 与 `.agents/scripts/verify_http_tests.py --strict`。Go 变更验证入口为 `./.agents/verifiers/verify-changed.sh`；Harness 变更运行 `./.agents/verifiers/verify-harness.sh`。
