---
title: Agent 内部架构与核心工作流
linkTitle: Agent 内部架构
weight: 85
description: Agent 端的详细控制流、执行流与遥测探针流水线
---

Design Cloud Engine 的 Agent 是一个结构极其精密、职责边界分明的服务端点，负责与 Control Server 进行双向通信，管理本地进程，以及汇报节点健康状态。

本文档将详细阐述 Agent 的实体骨架以及其内部运转的 **三大黄金链路**。

## 通信边界 (Communication Boundary)

- Agent 与外部世界的唯一通道是到 Control Server 的双向 gRPC 流（`Sync`）。
- Agent **不直接访问数据库**，不持有 DB 凭据；所有数据交互（配置下发、状态上报、命令执行）均经 Control Server 中转。
- 数据流：
  - **下行（配置下发）**：Control 读 DB → gRPC `agent_settings` / `UpdateSettingsCommand` → Agent 内存
  - **上行（状态上报）**：Agent → gRPC `AgentMessage`（心跳/资源/VNC/存储探针/执行结果）→ Control 写 DB
  - **命令执行**：Control → gRPC `CommandRequest` → Agent 本地 executor/launcher → 结果回传 → Control 落库
- 安全意义：Agent 只持有节点本地权限，DB 凭据仅存在于 Control 节点；节点即使被攻破也无法直接访问数据库。

## 架构俯瞰图

```text
               [ Control Server ]
                      ▲ │
           (双向 gRPC 流由 stream.go 维持)
                      │ ▼
              ┌────────────────┐
              │    Agent 核心    │ (agent.go / manager.go)
              └────────────────┘
                ▲            │
     主动上报     │            │ 下发指令 (command.go)
 (telemetry.go) │            ▼
                │        ┌───────┐── 拦截内部指令 (配置更新 / 取消任务 / VNC探测)
                │        │
                │        ▼
 ┌──────────┐   │     执行派发
 │   probe  │◄──┘    (execute.go)
 │ (无状态探针)│          │
 └──────────┘          ▼
                ┌────────────┐
                │  executor  │ (业务执行引擎：降权启动 / 运行脚本 / 校验配额)
                └────────────┘
```

## 核心实体文件

Agent 的顶层由以下两个文件负责管理生命周期：

1. **`agent.go`（主轴）**：定义了 `Agent` 结构体，维护核心上下文，包括无锁并发控制器（`activeTasks`）以及读写分离的动态配置中心（
   `SettingsManager`）。
2. **`manager.go`（外壳）**：对外暴露的服务外壳。负责编排 Agent 核心引擎的启动与优雅停机（Graceful Shutdown）。

---

## 三大核心工作链路

整个 Agent 围绕着一条双向网络流和三套业务管线在运转。

### 链路 1：网络基石与下行控制流 (Downstream Control)

这套链路的核心是 **“接收指令并进行业务路由”**。

- **`stream.go`**：网络底层。负责 mTLS 拨号、心跳维护、断线后的指数退避重连。它只负责维持底层 `ControlService_SyncClient`
  流的稳定性，对业务数据透明。
- **`command.go`**：业务指令网关。负责监听下行流，拆包 `ServerCommand` 并路由：
    - `UpdateSettings`：就地更新内存配置。
    - `CollectProbe`（`probe_name` ∈ `vnc-probe` / `resource-report` / `storage-probe`）：触发对应周期任务立即执行。
    - `CancelCommand`：取消正在运行的任务。
    - `CommandRequest`：将重型任务流转给执行桥梁（`execute.go`）。

### 链路 2：重型任务执行流 (Data / Execution Flow)

这套链路负责处理 Server 派发的、需要调用宿主机资源的业务任务。

- **`execute.go`**：作为接线员和监工。从 `command.go` 接收 `CommandRequest`（其中的 `trace_id` 由 Server 端
  `execution.NewExecuteRequest` 在创建执行请求时生成并随命令下发，Agent 不自行分配），并启动独立的
  Goroutine，将其派发给底层的 `executor` 包。
- **`executor` 包**：真正的执行引擎。根据任务类型决定是采用 `direct` 模式（直接运行系统级指令）还是采用 `launcher` 模式（切换到
  User Namespace 降权执行用户 App）。
- **结果回传**：执行完毕后，`execute.go` 将捕获到的日志和退出码打包成 `CommandResult` 塞进上行网络流，Server 确认后回传 `CommandResultAck`。

### 链路 3：自主遥测上报流 (Upstream Telemetry)

这套链路独立于控制指令之外，是 Agent 自主维持健康的本能反应。

- **`telemetry.go`（遥测引擎）**：包含一个轻量级的内置定时器（PeriodicRunner）。它负责按配置间隔唤醒系统。
- **`probe` 包（底层探针）**：无状态的纯工具箱（包含 `status.go`, `storage.go` 等）。它们不依赖 Agent 的任何状态，纯粹负责执行
  `statfs`、读取 `/proc/stat` 等系统调用。
- **打包上报**：`telemetry.go` 引擎定时调用 `probe` 探针拿到瞬间的操作系统快照后，拼装为 `HeartbeatRequest` 或
  `ResourceReport`，塞进上行网络流向 Server 汇报。

---

## 配置收敛与热推机制 (Settings Convergence & Hot-Push)

Agent 运行时的所有行为参数均受控于服务端的强类型配置体系：

1. **层级化融合架构 (Layered Convergence)**：
   - **全局基底 (`DomainAgent`)**：涵盖全局默认的 Launcher 路径、VNC 参数（`BinDir`、`ScanInterval`、`ServerArgs`）与 Monitor 配置（`DiskMounts`、`ResourceReportInterval`、`NvidiaSmiPath`）。
   - **节点覆盖 (`NodeSettings.Agent`)**：单个节点可根据异构硬件/路径环境，通过 `PUT /api/admin/system/nodes/:id/settings` 声明稀疏覆盖（Sparse Override）。未声明项自动继承全局。
   - **路径决议 (Path Resolution)**：统一以 `BinDir` 为唯一基准路径，系统内部自动拼接推导 `vncserver` 与 `vncpasswd` 绝对路径，杜绝路径多源冗余与环境变量 PATH 查找歧义。
2. **单节点与集群级在线热推**：
   - 当节点级配置更新时，Control Server 通过 `PushNodeSettingsUpdate` 向该特定 Agent 即时派发 `UpdateSettingsCommand`；
   - 在 Control HA 多节点集群下，Standby 节点通过 `ForwardClusterEvent` 自动将 `cluster:node_settings_updated` 转发至 Leader 节点执行下推；
   - 目标 Agent 收到命令后，`command.go` 路由至 `SettingsManager` 实现零停机内存热更新。

---

## Windows 节点 Agent 架构与跨平台解耦

Windows 系统在平台中定位为单纯的**受管计算/图形工作站节点（Agent 模式）**，主要承载工业级 3D CAD/CAM 图形软件（如 CATIA、SolidWorks、UG/NX 等）与 NVIDIA GPU 渲染加速。

### 1. 核心架构设计原则

- **职责克制与单向受管**：Windows 节点仅运行 Agent，绝不承担集群管控、数据库存储或 API 网关职责；`dc-launcher` 及其全部动作实现（`cmd/launcher`、`internal/launcher/**`）以 `//go:build !windows` 显式声明为 Linux-only，Windows 构建整体剥离，portal/control 服务面亦不参与 Windows 构建。
  说明：`internal/db` 与 `internal/server/service/{settings,settings/schema,node,push,cache}` 仍在 Windows 依赖图中——Agent 的 SettingsManager 复用服务端的设置领域服务；要彻底剥离需先为该服务面做平台裁剪。
- **编译期物理文件隔离（Zero-Runtime-Branching）**：利用 Go 编译工具链原生约定的 `*_windows.go` 与 `*_linux.go` 后缀实现跨平台系统调用解耦，严禁在共享链路中进行运行时 `runtime.GOOS == "windows"` 分支判断。
- **执行器 Direct-Only 剥离**：由于特权沙箱 `dc-launcher` 深度依赖 Linux 内核特性（`setuid`、`cgroups v2`、`namespaces` 等），Windows 构建彻底剥离 launcher 模块。所有受控 Action 统一通过 `directExec` 直接调用 Windows CLI 执行；Action 注册表（`actions_windows.go`）在编译期仅注册 `LICENSE_GET_STATUS` 与 `LICENSE_LIST_FEATURES`。
- **原生 Win32 探针与诚实指标**：
  - **内存**：调用 Win32 API `GlobalMemoryStatusEx` 获取物理内存；
  - **磁盘**：调用 Win32 API `GetDiskFreeSpaceExW` 与 `GetLogicalDriveStringsW` 遍历系统有效盘符；
  - **CPU 利用率**：调用 Win32 API `GetSystemTimes` 两次采样差值计算；
  - **运行时间**：调用 Win32 API `GetTickCount64` 计算系统开机秒数；
  - **诚实原则**：Windows 内核中不存在的数据维度（如 Unix Load Average 与固定 Swap 分区）直接保持 Proto3 零值缺省，不伪造指标。
- **远程桌面原生 RDP 机制**：
  - 依托 Windows 操作系统原生远程桌面服务（`TermService`），监听端口读取注册表 `WinStations\RDP-Tcp\PortNumber`（缺省回退 **TCP 3389**，不写死）；
  - Agent 探针在策略允许时要求三级同时成立：`fDenyTSConnections == 0`、`TermService` 为 `SERVICE_RUNNING`、本地监听端口 TCP 握手成功。策略明确拒绝时直接判定不可用。仅当策略与服务状态都读不到时才视为不可判定、不上报；服务运行但端口不通（或反过来）会上报不可用，而不是当成可用。
  - 就绪状态随心跳 `HeartbeatRequest.rdp`（`RDPStatus{ available, port }`）上报；消息字段自带 presence，Linux 节点不填。Server 在 `connections` 表中同步静态共享 RDP 连接记录；
  - Agent 侧不扫描进程、不动态配端口，不参与远程桌面接入数据面。
- **Windows Service 原生生命周期**：
  - 封装官方 `golang.org/x/sys/windows/svc` 运行库，支持注册为 Windows 原生系统常驻服务；
  - 提供 CLI 子命令（`service install / start / stop / status / uninstall`）；
  - 运行时临时状态与 PID 锁文件统一收敛至 `%PROGRAMDATA%\DesignCloudEngine`，基于 Win32 `LockFileEx` 实现跨进程排他锁。
