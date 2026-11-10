---
title: 扩展特权 Action 与调度驱动
linkTitle: Action 扩展
weight: 40
description: gRPC 协议定义、Direct/dc-exec Action注册边界与调度驱动扩展指南
aliases:
  - /docs/development/guide/execution-development
---

本文汇总 Agent 执行链路的开发约定：协议定义与代码生成、Direct/dc-exec Action边界、执行策略与实时通信。环境与构建见 [开发环境](./dev-environment.md)。

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
make proto
```

编译生成的文件包括：

- `internal/contract/proto/cluster.pb.go` - 消息定义
- `internal/contract/proto/cluster_grpc.pb.go` - gRPC 服务定义

如需修改通信接口，直接编辑 `internal/contract/proto/cluster.proto`。

### gRPC 服务实现

gRPC 控制服务端位于 `internal/server/controlplane/`，Agent 端客户端位于 `internal/agent/` 相关实现中。协议语义见[集群协议](../architecture/cluster/protocol.md)。

## Direct Executor 动作

Agent 内 Direct Executor（`internal/agent/executor/direct/`）处理无需提权的动作，直接以 Agent 进程身份执行。当前已实现的 Direct 动作包括：

| 动作类型 | 说明 |
|---|---|
| `JOB_QUERY` | 查询作业状态（调度器） |
| `LICENSE_GET_STATUS` | 查询 License Server 状态 |
| `LICENSE_LIST_FEATURES` | 列出 License 特性及用量 |
| `USER_LIST` | 列出系统用户 |
| `USER_GET` | 获取指定用户信息 |
| `GROUP_GET` | 获取指定组信息 |
| `GROUP_LIST` | 列出所有组 |

需要提权、身份切换或经 dc-exec 执行的作业/文件/连接类动作（含 `JOB_SUBMIT`、`JOB_CONTROL`、`JOB_MODIFY` 等）见[启动器动作](../architecture/dcexec/actions.md)。

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

## 编码与领域文档

日常开发规范、Portal API 分层、缓存纪律与依赖装配见：

- [开发规范](./code-conventions.md)
- [依赖装配](./dependency-wiring.md)
- [日志系统](../deployment/logging.md)

新增 Portal 端点时同步更新 `test/portal/*.http`，并运行 `python3 .agents/scripts/verify_http_tests.py --strict`（详见 [测试体系](./testing.md)）。

## 实时通信与 WebSocket 开发

项目中的实时数据通信按场景划分为两类：

1. **远程桌面协议代理 (WebSocket)**：
   - 专用于 VNC / RDP / SSH 远程桌面的二进制数据流中转；
   - 统一由 `GET /api/connections/:id/proxy-connection`（WebSocket Upgrade + Portal Session）一次握手建隧，基于 `coder/websocket` 做字节桥接；
   - 详细设计与实现参考[协议代理文档](../architecture/remote-access/proxy.md)。

2. **系统状态与通知推送 (Server-Sent Events / SSE)**：
   - 作业状态变更、系统通知与安全告警等单向推送统一使用 SSE 协议；
   - 详细协议规范参考[事件与实时流](../architecture/domains/events-and-overview.md)。
