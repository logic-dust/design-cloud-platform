---
title: 本地开发环境准备
linkTitle: 开发环境
weight: 10
description: 本地开发、编译依赖、数据库与调试方式
aliases:
  - /docs/development/guide/dev-environment
---

## 前置要求

开发环境需要以下软件：

- **Go 1.27.2+**（`go.mod` 声明 `toolchain go1.27.2`；静态检查 `make lint vuln`，`lint` 含 `gofmt -l` 格式门禁与 staticcheck）
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

参看架构文档 [系统架构](../overview/architecture.md) 了解项目整体结构和模块划分。

开始修改代码前，建议先阅读 [开发规范](./code-conventions.md)。这份文档定义了当前项目的开发规范、架构边界、配置约束和测试约定。

## 依赖边界约束

运行时依赖治理约定如下：

- `db.Store` 只允许出现在 composition root 和测试初始化代码中。
- 允许的运行时代码入口包括：`internal/server/server.go`、`internal/agent/manager.go`。
- 领域 Service 不应为方便而持有完整 `config.Config`；按实际需要注入配置快照或窄接口。
- 新增 Service 时，数据库访问通过领域 Repo 或窄接口显式传入，不直接传 `*db.Store`。
- `db.NewRepos(store)` 在同一个装配根内只调用一次；不要在同一条装配链里重复创建 Repo 集合。

数据库装配和 Repo 约定见[依赖装配](./dependency-wiring.md)。

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

详细内容请参考[配置文件](../deployment/config.md)。

## 编译模式 (Build Modes)

项目通过 Go 的 `build tags` 机制区分开发和生产环境，实现在不修改源代码的情况下切换内部行为。

### 生产模式

直接使用 `go build` 编译时，系统处于**生产模式**：

- 日志级别遵循配置文件设定（通常是 `INFO`）。
- 编译标签文件：`cmd/engine/run_prod.go`。
- Cluster 通信仍然强制使用 mTLS。

```bash
go build -o build/dc-engine ./cmd/engine
```

### 开发模式 (Dev Mode)

使用 `-tags dev` 进行编译：

- 应用与访问日志默认输出到 **stdout**（与生产 journald 模型一致）；审计/安全仍写 `log.dir`。
- 会自动准备开发环境所需的部分本地资源（如 mock dc-exec、开发数据、开发证书）。
- 将编译各种dev标签文件：`*_dev.go`。

```bash
go build -tags dev -o build/dc-engine-dev ./cmd/engine
```

或使用 Makefile：

```bash
make engine-dev
```

**调试日志输出示例：**

```bash
# 应用/访问 → stdout（开发与生产默认）
go run -tags dev ./cmd/engine --config config.toml
# 或：make run
```

## 进程控制命令

Engine 提供了内置的进程管理命令，用于在非 systemd 环境（如开发模式）下控制进程：

```bash
# 停止运行中的进程
./dc-engine service stop

# 查看进程状态
./dc-engine service status
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
# 构建所有组件（Engine + DcExec）
make all

# 或者分别构建
make engine    # 构建主程序
make dcexec  # 构建 DcExec

# 本地开发构建
make engine-dev
make dcexec-dev

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

# 构建 DcExec
make dcexec

# 本地开发构建
make engine-dev
make dcexec-dev

# 打包（engine + dcexec 单包；正式版 / 开发快照）
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

## gRPC 与执行链路开发

Protocol Buffers 定义、代码生成、Direct Executor 动作与实时通信开发见 [执行链路开发](./execution-development.md)。

## 测试

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

分层策略、门禁矩阵与 Portal HTTP 契约测试见 [测试体系](./testing.md)。

生产部署与 `config.toml` 字段说明见[安装与首次部署](../deployment/install.md)与[配置文件](../deployment/config.md)。

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

缓存与后台循环的优化边界见[存储与缓存](../architecture/domains/storage-and-cache.md)与[后台任务](../architecture/domains/background-tasks.md)。
