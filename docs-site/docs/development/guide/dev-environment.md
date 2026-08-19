---
title: 开发环境
linkTitle: 开发环境
weight: 60
description: 本地开发、编译与调试方式
---

## 前置要求

开发环境需要以下软件：

- **Go 1.27**
- **Protocol Buffers 编译器** - 用于生成 gRPC 代码
  ```bash
  # Arch Linux
  sudo pacman -S protobuf

  # 安装 Go 插件
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  ```
- **Database** - 开发与调试时需要一个可用数据库后端；可按场景选择具体驱动
- **Linux 操作系统** - 项目仅支持 Linux 运行环境
- **Git** - 版本控制

可选：

- **LSF/Slurm/OpenLava** - 用于作业调度集成开发和测试
- **LDAP 服务器** - 用于 LDAP 用户管理功能开发和测试
- **VNC Server**如TurboVNC/TigerVNC - 用于图形界面远程访问功能开发和测试
- **RDP Server** - 用于RDP远程访问功能开发和测试

## 项目结构

参看架构文档 [系统架构](../architecture.md) 了解项目整体结构和模块划分。

开始修改代码前，建议先阅读 [开发规范](./code-conventions.md)。这份文档定义了当前项目的开发规范、架构边界、配置约束和测试约定。

## 依赖边界约束

运行时依赖治理约定如下：

- `db.Store` 只允许出现在 composition root 和测试初始化代码中。
- 允许的运行时代码入口包括：`internal/server/server.go`、`internal/agent/manager.go`。
- `internal/server/service/user`、`internal/server/service/file`、`internal/server/service/proxy`、
  `internal/server/service/session` 不允许直接读取 `config.Config`，需要通过构造函数注入配置快照或窄接口。
- 新增 service 时，数据库依赖必须通过领域 Store 或窄接口显式传入，不直接传 `*db.Store`。
- `db.NewRepos(store)` 在同一个装配根内只调用一次；不要在同一条装配链里重复创建领域 Store 集合。

数据库装配和领域 Store 约定见 [依赖装配](./dependency-wiring.md)。

可使用下面的检查命令验证这些约束：

```bash
python3 .agents/verifiers/verify-arch.py
```

## 快速开始

### 依赖库

```bash
cd design-cloud-engine

# 如要更新依赖
go get -u ./...

# 安装 Go 依赖
go mod tidy
```

如要重新生成 Protocol Buffers 代码（在修改 .proto 文后）：

```bash
# 生成 gRPC 代码
make proto
```

### 配置管理

配置文件默认名为 `config.toml`，平时可以直接复制开发配置模板修改：

```bash
cp config.dev.toml config.toml
```

详细内容请参考 [配置文件](../../deployment/config.md)。

## 编译模式 (Build Modes)

项目通过 Go 的 `build tags` 机制区分开发和生产环境，实现在不修改源代码的情况下切换内部行为。

### 生产模式

直接使用 `go build` 编译时，系统处于**生产模式**：

- 日志级别遵循配置文件设定（通常是 `INFO`）。
- 编译标签文件：`cmd/engine/run_prod.go`。
- Cluster 通信仍然强制使用 mTLS。

```bash
go build -o build/design-cloud-engine ./cmd/engine
```

### 开发模式 (Dev Mode)

使用 `-tags dev` 进行编译：

- 应用日志默认输出到 **stdout**，便于开发调试。
  若需写入日志文件，设置环境变量 `DCE_DEV_LOG_TO_FILE=true`（或 `1`）即可回退到 `log.dir/app.<hostname>.log`。
- 会自动准备开发环境所需的部分本地资源（如 mock launcher、开发数据、开发证书）。
- 将编译各种dev标签文件：`*_dev.go`。

```bash
go build -tags dev -o build/design-cloud-engine-dev ./cmd/engine
```

或使用 Makefile：

```bash
make engine-dev
```

**调试日志输出示例：**

```bash
# 输出到 stdout（开发默认）
go run -tags dev ./cmd/engine --config config.toml

# 输出到文件
DCE_DEV_LOG_TO_FILE=true go run -tags dev ./cmd/engine --config config.toml
```

## 进程控制命令

Engine 提供了内置的进程管理命令，用于在非 systemd 环境（如开发模式）下控制进程：

```bash
# 停止运行中的进程
./design-cloud-engine service stop

# 查看进程状态
./design-cloud-engine service status
```

**注意事项：**

1. 这些命令基于 PID 文件进行进程管理（默认 `${XDG_RUNTIME_DIR}/design-cloud-engine/dc-engine.pid`；若 `XDG_RUNTIME_DIR`
   不存在则回退到 `/tmp/design-cloud-engine-<uid>/dc-engine.pid`）
2. `stop` 命令发送 SIGTERM 信号，等待进程优雅退出（30秒超时），超时则发送 SIGKILL
3. 裸跑场景如需“重启”，请先执行 `stop`，再重新运行原启动命令
4. **生产环境强烈建议使用 systemd 管理进程**（`systemctl restart design-cloud-engine.service`）
5. systemd 提供更完善的进程监控、自动重启、资源限制等特性

### 配置数据库

#### 使用轻量数据库（开发快速开始）

默认示例使用本地文件型数据库，并在程序运行目录中生成数据文件。

可以直接编辑 `config.toml` 中的数据库路径来切换开发数据库。

#### 使用外部数据库（验证接近生产的环境）

```bash
# 复制开发配置模板
cp config.dev.toml config.toml

# 编辑 config.toml，配置数据库连接信息
vi config.toml

# 启动程序，程序会在正常启动链路中连接数据库
go run ./cmd/engine --config config.toml
```

### 构建和运行

```bash
# 构建所有组件（Engine + Launcher）
make all

# 或者分别构建
make engine    # 构建主程序
make launcher  # 构建 Launcher

# 本地开发构建
make engine-dev
make launcher-dev

# 编译并本地运行（开发版）
make run
```

### 默认测试账号

开发模式下会自动创建一个测试用户（非系统用户），用户名为 `mock`，密码`mock123`。

## 构建系统

项目使用 `Makefile` 管理构建过程，支持自动化版本注入。

### 构建目标

```bash
# 生成 Protocol Buffers 代码
make proto

# 构建所有组件
make all

# 构建主程序
make engine

# 构建 Launcher
make launcher

# 本地开发构建
make engine-dev
make launcher-dev

# 打包（engine + launcher 单包；正式版 / 开发快照）
make package          # VERSION 纯净，默认 GOARCH=amd64 PKG_TYPE=rpm
make package-dev      # 版本带 git 短 hash
# 交叉架构示例：GOARCH=arm64 make package
make print-version

# 清理构建产物
make clean
```

### 版本管理

版本号由 `Makefile` 统一计算并通过 `ldflags` 注入：

- `VERSION`：版本号，X.Y.Z格式，所有构建目标（正式版/dev 版）统一使用
- git 追溯信息不拼进版本串，由独立元数据注入：`COMMIT`（git 短 hash）、`DATE`（构建日期 UTC）

`make print-version` 输出版本号；`/api/version` 通过 `version` + `commit` + `releaseDate`
三个字段组合唯一定位一次构建。

### 构建

```bash
# 构建 Linux 版本（默认）
GOOS=linux GOARCH=amd64 make engine
```

## gRPC 开发

### Protocol Buffers 定义

gRPC 服务定义在 `internal/contract/proto/cluster.proto` 中：

```protobuf
service ControlService {
	// Agent 主动连接并保持持久双向流
	rpc Sync(stream AgentMessage) returns (stream ServerCommand);

	// 节点间转发命令分发与集群事件同步
	rpc ForwardDispatch(ForwardDispatchRequest) returns (ForwardDispatchResponse);
	rpc ForwardClusterEvent(ForwardClusterEventRequest) returns (ForwardClusterEventResponse);
}
```

### 生成代码

每次修改 `internal/contract/proto/cluster.proto` 后，需要重新生成代码：

```bash
# 修改 cluster.proto 后，重新生成 Go 代码
make proto
```

编译生成的文件包括：

- `internal/contract/proto/cluster.pb.go` - 消息定义
- `internal/contract/proto/cluster_grpc.pb.go` - gRPC 服务定义

> 💡 **提示**：框架在生成 Go 代码时包含了详细的底层序列化逻辑，如需修改通信接口，直接编辑
`internal/contract/proto/cluster.proto`。

### gRPC 服务实现

gRPC 控制服务端位于 `internal/server/control/`，Agent 端客户端位于 `internal/agent/` 相关实现中。实际协议定义见
`internal/contract/proto/cluster.proto`。

## Direct Executor 动作

Agent 内 Direct Executor（`internal/agent/executor/direct/`）处理无需提权的动作，直接以 Agent 进程身份执行。当前已实现的
Direct 动作包括：

| 动作类型                    | 说明                   |
|-------------------------|----------------------|
| `JOB_QUERY`             | 查询作业状态（调度器）          |
| `JOB_SUBMIT`            | 提交作业                 |
| `JOB_CONTROL`           | 控制作业（暂停/恢复/终止等）      |
| `LICENSE_GET_STATUS`    | 查询 License Server 状态 |
| `LICENSE_LIST_FEATURES` | 列出 License 特性及用量     |
| `USER_LIST`             | 列出系统用户（直接读取，无需提权）    |

| `USER_GET`              | 获取指定用户信息 |
| `GROUP_GET`             | 获取指定组信息 |
| `GROUP_LIST`            | 列出所有组 |

需要提权或身份切换的动作（如密码修改、文件操作）由 Launcher Executor 处理，经 `dc-launcher`
执行，参见 [启动器动作](../launcher/actions.md)。

完整 Operation 定义位于 `internal/execution/actions.go`，执行路由在 `internal/agent/executor/executor.go`。

### 执行策略选择

在实现 Agent 功能时，关于**使用 Go 直接调用（Native Exec）还是外挂脚本（External Script）**，遵循以下原则：

#### 1. 首选 Go 直接调用 (Native Exec)

对于文件操作、系统用户管理、以及标准化的工具调用（如 `lmstat`），必须优先使用 Go 标准库函数或 `exec.Command` 直接调用二进制程序。

- **安全性**：避免 Shell 注入风险，参数作为纯字符串数组传递
- **性能**：省去 Shell 解释器启动开销
- **标准化**：核心逻辑不依赖外部环境配置

#### 2. 审慎使用外挂脚本 (External Script)

仅在以下场景使用外挂脚本模式（如 `JOB_SUBMIT`）：

* **业务逻辑高度动态**：如对接 LSF/Slurm/OpenLava 等作业调度系统，不同现场环境差异巨大（环境变量、wrapper 脚本等）。
* **需要现场适配**：允许运维人员在不重新编译 Engine 的情况下调整执行逻辑。

**安全约束**：

* 脚本路径必须由配置文件指定（白名单）。
* 禁止以 Root 权限执行脚本。
* 强制 TargetUID 检查。

## 代码开发规范

### 包结构

- 每个包应有明确的职责（SRP）
- 避免包之间的循环依赖
- 内部使用的包可用 `internal` 目录

### 代码风格

遵循 Go 官方的代码规范：

```bash
# 格式化代码
go fmt ./...

# 检查代码规范
go vet ./...

# 静态分析
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

### 命名约定

- 包名使用小写字母
- 函数和变量名使用 camelCase
- 常量使用 PascalCase 或 UPPER_SNAKE_CASE
- 接口名以 `er` 结尾（如 `Reader`, `Writer`）

### 错误处理

```go
// 良好实践
if err := someFunction(); err != nil {
logger.Error("operation failed", err)
return err
}

// 使用结构化日志
logger.Info("operation success", "user_id", userID, "duration_ms", duration)
```

### 现代 Go 约定

- 项目使用 Go 1.27，代码中使用 `errors.AsType` 等现代写法。
- 错误处理优先保留错误链：`fmt.Errorf("...: %w", err)`。
- 日志统一使用结构化日志，禁止使用 `fmt.Println` 风格的业务日志。

## 日志

项目使用 `log/slog` 包提供结构化日志。根据编译模式自动切换格式：

- **Dev 模式**：人类友好的文本格式，`[component]` 前置显示
- **Prod 模式**：标准 JSON 格式，便于日志采集

```go
import "design-cloud-engine/internal/logger"

// 快捷函数
logger.Info("User login", "user_id", userID)
logger.Error("Operation failed", err)

// 带上下文的子 logger
log := logger.WithContext("component", "portal_server")
log.Info("Server started", "port", 8080)
```

详细文档请参考 [日志系统](../../deployment/logging.md)。

## 数据库操作

项目使用 GORM 作为 ORM 框架。

### 查询示例

```go
var userRecord model.User
db.Where("username = ?", username).First(&userRecord)

var users []model.User
db.Where("status = ?", "active").Find(&users)
```

### 新增/更新

```go
// 新增
db.Create(&userRecord)

// 更新
db.Model(&userRecord).Update("status", "inactive")

// 批量更新
db.Model(&model.User{}).Where("role = ?", "admin").Update("permissions", newPerms)
```

## API 开发

### 添加新的 API 端点

1. 在 `internal/server/portal/api/` 目录创建相应的处理文件（如 `xxx.go`）
2. 定义处理函数和 API 路由
3. 在 `internal/server/portal/api/api.go` 中注册路由

```go
// 在 api/api_myfeature.go
func (app *App) handleMyFeature(c *echo.Context) error {
// 参数解析
var req MyRequest
if err := c.Bind(&req); err != nil {
return c.JSON(400, echo.Map{"error": "invalid req"})
}

// 业务逻辑
b, err := app.myService.Process(req)
if err != nil {
return c.JSON(500, echo.Map{"error": err.Error()})
}

return c.JSON(200, b)
}

// 在 api/api.go 中注册
func (app *App) setupRoutes(g *echo.Echo) {
g.POST("/api/myfeature", app.handleMyFeature)
}
```

## 实时通信与 WebSocket 开发

项目中的实时数据通信按场景划分为两类：

1. **远程桌面协议代理 (WebSocket)**：
   - 专用于 VNC / RDP / SSH 远程桌面的二进制数据流中转；
   - 统一由 `GET /api/connection-proxy/ws` 承载，基于 `coder/websocket` 与 OTP 一次性凭证进行数据中转；
   - 详细设计与实现参考 [协议代理文档](../remote-access/proxy.md)。

2. **系统状态与通知推送 (Server-Sent Events / SSE)**：
   - 作业状态变更、系统通知与安全告警等单向推送统一使用 SSE 协议；
   - 详细协议规范参考 [全局事件与实时流推送](../events-and-streaming.md)。

## 测试

### 运行测试

```bash
# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./internal/server/service/user/...

# 测试覆盖率
go test -cover ./...

# 生成覆盖率报告
go test -coverprofile=coverage.stdout ./...
go tool cover -html=coverage.stdout
```

### 集成测试

# 运行项目增量测试门禁（根据变更选择测试范围）
./.agents/verifiers/verify-changed.sh

# 或执行全量测试
./.agents/verifiers/verify-tests.sh --all
./test/run_all.sh

## 部署和配置

### 角色配置

**节点角色**：Agent 角色全节点无条件常驻运行；Server 角色由 `config.toml` 中的 `cluster.servers` 拓扑决定。

系统通过以下方式决定节点角色：

```toml
# 单机开发：本机同时承担两种角色
[cluster]
servers = ["devbox"]

# 分布式：共享 config.toml，通过拓扑自动识别
# mgt01 在 servers 中，启动 Server + Agent；cn[01-10] 不在 servers 中，仅运行 Agent
[cluster]
servers = ["mgt01"]
```

### 生成配置文件

```bash
# 生成配置模板
./build/design-cloud-engine gen-config
```

### 配置结构

配置文件对外采用结构化 `TOML`，内部仍按领域结构组织：

- **公共配置**：`[log]`, `[cluster]` (`cluster.servers`)
- **运行配置**：`[portal]`, `[cluster]`, `[database]`, `[user]`, `[file]`
- **Agent 配置**：`[cluster.agent]`
- **调度器配置**：`[scheduler]`

详见 [配置文件](../../deployment/config.md)。

### 自动化部署

```bash
# 完整安装
sudo ./deployment/install.sh
```

## 常见问题

### Q: 如何调试程序？

A: 使用 Delve 调试器：

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
dlv debug ./cmd/engine -- --config config.toml
```

### Q: 如何重新生成 gRPC 代码？

A: 运行 proto 目标：

```bash
make proto
```

### Q: 如何处理数据库迁移？

A: 项目启动时自动执行数据库初始化。使用 GORM 的自动迁移功能。

### Q: 如何添加新的依赖？

A:

```bash
go get github.com/some/package@latest
go mod tidy
```

### Q: 构建时出现 protoc 错误？

A: 确保安装了 Protocol Buffers 编译器和 Go 插件：

```bash
# 检查 protoc 版本
protoc --version

# 检查 Go 插件
which protoc-gen-go
which protoc-gen-go-grpc
```

## 性能调优

### pprof 性能分析

使用内置的 pprof 端点进行性能分析：

```bash
# CPU 分析
go tool pprof http://localhost:8080/debug/pprof/profile

# 内存分析
go tool pprof http://localhost:8080/debug/pprof/heap
```

### 常见优化点

- 减少数据库查询，使用 JOIN 或预加载
- 使用连接池管理数据库连接
- 启用 gzip 压缩响应
- 实现缓存策略
- 优化 gRPC 连接复用
