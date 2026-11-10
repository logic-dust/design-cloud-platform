---
title: 集群协议
linkTitle: 集群协议
weight: 20
description: Control 与 Agent 的 Cluster 通信协议和连接模型
aliases:
  - /docs/development/cluster/protocol
---

Design Cloud Engine 使用基于 gRPC 的 `ControlService` 协议，实现 Control 与 Agent 之间的安全双向通信；Server 侧由 Control
组件承载该协议。

## 架构概览

```text
+-----------------------+                         +-----------------------+
|   Control / Portal    |                         |         Agent         |
+-----------------------+                         +-----------------------+
|                       |                         |                       |
| - Portal API          |                         | - ClusterAgent        |
| - Control gRPC Server | <=====================> |   (gRPC client)       |
| - Dispatcher     |  Cluster Protocol mTLS  | - Executor            |
|                       |                         | - Background Loops    |
+-----------------------+                         +-----------------------+
```

## 安全配置

### mTLS 双向认证

Cluster gRPC 通信使用 mTLS，证书由 cluster CA 签发，并在数据库中绑定指纹：

- **Server 角色实例证书**: 向 Agent 标识 Server 身份
- **Agent 角色实例证书**: 向 Server 标识 Agent 身份
- **CA 证书**: 验证证书链有效性
- **指纹校验**: Server 接收 Agent 的 mTLS 连接时，会校验证书指纹与数据库记录一致

### 配置示例

```toml
[cluster]
port = 52255

heartbeat_interval = 60
connect_timeout = 30
node_heartbeat_timeout = 180
node_check_interval = 120
tls_server_name = ''

servers = [
  "server-primary",
]
```

> 上例 `[cluster]` 数值与 [部署配置 `config.md`](/docs/deployment/config.md) 中的默认值一致（非最小合法取值示例）。

> Agent 会自动展开 `Cluster.Servers` 中的所有项并拼接 `Cluster.Port` 形成连接目标列表。
> 集群证书路径基于 **DataDir**（配置文件模式：`data_dir`，空则回退 BaseDir；Configless：`--data-dir`）动态延展：
> `certs/cluster/ca.crt`（CA 公钥平铺，0644）、`certs/cluster/ca/ca.key`（CA 私钥，0600）、
> `certs/cluster/<nodeID>/<nodeID>.{crt,key}`（节点证书与私钥）；`certs/cluster` 必须为 0755 以保证共享存储上的穿透读取。

## 节点身份识别 (Node Identity)

Engine 节点在接入集群时，必须具备唯一的身份标识（Node ID）。该标识用于证书匹配、心跳上报以及作业派发。

### 当前实现

当前代码在启动阶段由 `config.resolve()` 直接调用 `os.Hostname()` 生成 `Runtime.NodeID`，并将其作为本机唯一节点身份。当前没有
`DCE_NODE_ID` 或配置文件字段覆盖入口。

1. `internal/platform/config/resolve.go` 将 `cfg.Runtime.NodeID` 设为 `os.Hostname()`
2. 同一值还会与 `Cluster.Servers` 展开的主机名比较，用于决定本机是否启用 Server 角色
3. `internal/agent/manager.go` 使用该值构造 Agent 配置以及节点证书/私钥路径
4. `ClusterAgent.sendRegister()` 将该值写入 `RegisterRequest.node_id`

Windows 上 `os.Hostname()` 来自 `GetComputerNameEx(ComputerNamePhysicalDnsHostname)`，通常不等于 `$env:COMPUTERNAME`（NetBIOS，多为全大写）。登记节点、证书文件名和运行时 NodeID 必须用同一个字符串，不能单独转大小写。

> [!TIP]
> 运行中的节点主机名必须与集群登记使用的 `node_id`、节点证书标识以及证书文件名保持一致，否则 Agent 无法完成注册后的 mTLS
> 接入。

### mTLS 要求

Cluster gRPC 使用 mTLS：

- Server 角色实例必须加载本机 cluster 证书与 CA
- Agent 角色实例必须加载本机 cluster 证书与 CA
- Server 总是执行证书指纹绑定校验

### 首次注册流程 (Initial Registration)

当前 OTP 引导流程是“领证 + 绑定”一次完成：节点持一次性 `token` 调用注册 API 后，Server 会立即签发节点证书、回写证书元数据，并把启动
Agent 所需的 TLS 材料直接返回给节点。

流程步骤如下：

1. **创建身份**：管理员在 Server 创建节点，获取一次性注册 `token`。
2. **OTP 引导**：节点调用 `POST /api/nodes/:nodeID/registration`，提交 `{ "token": "..." }`。
3. **Server 签发并回写**：Server 校验节点存在且 token 未过期后签发证书。有效 token 即授权首次签发或重新签发。回写
   `cert_fingerprint`、`cert_subject`、`cert_expires_at`，清空 token；运行状态列保持为空（未接入），直到 Agent 首次建立 `ControlService/Sync`。节点证书的 Organization 是固定的 `Design Cloud Engine`，不是签发方节点 ID。
4. **返回引导材料**：API 通过 `Response{data: ...}` 返回 `nodeId`、`serverAddr`、`certPem`、`keyPem`、`caPem`、
   `certExpiresAt`。
5. **节点本地落盘**：Agent 将证书材料写入本地文件。
6. **后续 mTLS 接入**：Agent 使用刚领取的证书建立 `ControlService/Sync`；节点状态不会在 OTP API 成功时立即变为 `online`
   ，而是在后续 gRPC 注册/心跳阶段推进。

```mermaid
sequenceDiagram
    participant Server as Server (Portal/CA/Control)
    participant Admin as 管理员/脚本
    participant Agent as Agent Node
    Admin ->> Server: 1. 创建节点 (POST /api/admin/system/nodes)
    Server -->> Admin: 返回注册 Token
    Agent ->> Server: 2. OTP registration (POST /api/nodes/:nodeID/registration)
    Server -->> Agent: 3. Response{data:{cert/key/ca/serverAddr}}
    Agent ->> Agent: 4. 落盘证书材料
    Agent ->> Server: 5. 使用已登记证书建立 mTLS 连接
    Server -->> Agent: RegisterAck
```

### OTP 节点注册示例

下面的 `curl` 脚本演示节点如何使用注册 token 领取证书材料：

```shell
NODE_HOSTNAME="agent-01"
REGISTER_TOKEN="abc-123-xyz"

# 1. 调用 OTP 节点注册 API
curl -X POST "https://portal.example.com/api/nodes/${NODE_HOSTNAME}/registration" \
  -H "Content-Type: application/json" \
  -d "{
    \"token\": \"${REGISTER_TOKEN}\"
  }"
```

成功响应体示例：

```json
{
  "data": {
    "nodeId": "agent-01",
    "serverAddr": "server-01:52255",
    "certPem": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
    "keyPem": "-----BEGIN EC PRIVATE KEY-----\n...\n-----END EC PRIVATE KEY-----\n",
    "caPem": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
    "certExpiresAt": "2027-03-25T00:00:00Z"
  }
}
```

### 连接验证流程 (Connection Verification)

Agent 发起 gRPC 连接时，Server 执行严格的零信任验证。

1. **注册表检查**: 根据节点 hostname / nodeID 查找节点记录。
2. **mTLS 握手**: 验证 Agent 证书是否由受信任 CA 签发。
3. **指纹比对**: 在 TLS 模式下，提取客户端证书指纹，与数据库中已注册的指纹进行比对。

```mermaid
flowchart TD
	Agent[Agent 连接] -->|mTLS 握手| TLS{证书有效?}
	TLS -->|否| Reject[拒绝连接]
	TLS -->|是| Registry{已在注册表?}
	Registry -->|否| Reject
	Registry -->|是| Registered{已注册指纹?}
	Registered -->|否| RejectMsg[拒绝: 需先注册]
	Registered -->|是| Match{指纹匹配?}
	Match -->|否| Mismatch[拒绝: 指纹不匹配]
	Match -->|是| Success((连接建立))
	style Success fill: #c8e6c9, stroke: #2e7d32
	style Reject fill: #ffcdd2, stroke: #c62828
	style RejectMsg fill: #ffcc80, stroke: #ef6c00
	style Mismatch fill: #ffcdd2, stroke: #c62828
```

> **安全说明**：证书指纹一旦保存，后续连接必须使用相同证书。如需更换证书，必须由管理员调用 API 清除或更新数据库中的
`cert_fingerprint` 字段。

### 安全原理解析 (Why Double Verification?)

为何在 mTLS 验证通过后，还需要进行应用层的指纹比对？这是为了构建 **纵深防御 (Defense in Depth)** 体系，主要防范以下风险：

1. **防范 CA 签发失控**:
    * **风险**: 如果内部 CA 私钥泄露或系统被入侵，攻击者可以签发技术上合法的伪造证书。
    * **防御**: mTLS 层面会因签名合法而放行，但应用层指纹比对会因指纹不匹配而拒绝连接，从而阻断攻击。

2. **即时权限撤销**:
    * **风险**: 传统 CRL/OCSP 撤销机制存在延迟或维护成本高。
    * **防御**: 通过数据库清除节点指纹，可实现**毫秒级**的权限吊销。即使 Agent 持有合法证书，也会立即被拒绝接入。

3. **身份强绑定 (Identity Binding)**:
    * **风险**: 仅仅持有合法证书不足以证明 "你是名单上的那个人"。
    * **防御**: 指纹比对建立了 `Hostname` ↔ `Certificate` 的强绑定关系。确保只有特定的证书才能以特定的 Hostname
      身份接入，防止合法证书的冒用或混用。

## 服务定义

### ControlService

**核心双向流服务**：Agent 主动连接 Server，建立持久双向流用于心跳、命令下发和执行结果回传。

```protobuf
service ControlService {
	// Agent 连接并保持持久双向流
	rpc Sync(stream AgentMessage) returns (stream ServerCommand);

	// Standby Control 转发派发请求给 Leader Control
	rpc ForwardDispatch(ForwardDispatchRequest) returns (ForwardDispatchResponse);

	// Control 节点间跨机转发 Push 通知（广播/定向用户或角色）
	rpc ForwardClusterEvent(ForwardClusterEventRequest) returns (ForwardClusterEventResponse);
}
```

**通信模型**：

- Agent 发送：`RegisterRequest` (注册) → `HeartbeatRequest` (心跳) → `CommandResult` (命令执行结果) → `Metrics` (资源指标) → `ConnectionReport` (连接席位运行态上报) → `StorageProbeReport` (共享存储探测结果) → `RenewCertRequest` (证书续签 CSR) → `RenewCertResult` (证书安装结果回报)
- Server 发送：`RegisterResponse` (注册响应) → `HeartbeatResponse` (心跳响应) → `CommandRequest` (执行命令) → `CancelCommand` (取消命令) → `RedirectCommand` (Leader 重定向) → `CommandResultAck` (执行结果确认) → `CollectProbeCommand` (按需触发节点探针即时采集: connection-probe/metrics/storage-probe) → `UpdateSettingsCommand` (配置推送) → `RenewCertCommand` (请求续签) → `RenewCertResponse` (回传签发证书)

## 消息定义

### Agent → Server 消息

```protobuf
message AgentMessage {
	oneof payload {
		RegisterRequest register_request = 1;           // 注册请求（首次连接）
		HeartbeatRequest heartbeat_request = 2;         // 心跳
		CommandResult command_result = 3;               // 命令执行结果
		Metrics metrics = 4;             // 资源指标
		ConnectionReport connection_report = 5;               // 连接席位运行态（VNC/XRDP）
		StorageProbeReport storage_probe_report = 6;     // Storage probe report
		RenewCertRequest renew_cert_request = 7;         // 证书续签：Agent 生成密钥对并上送 CSR
		RenewCertResult renew_cert_result = 8;           // 证书续签：本地安装结果回报
	}
}

// StorageProbeReport 由 Agent 定期或按需对所分配的共享存储进行容量采集上报
message StorageProbeItem {
	string storage_id = 1;
	int64 total = 2;       // 总容量，单位 KB (KiB)
	int64 used = 3;        // 已使用，单位 KB (KiB)
}

message StorageProbeReport {
	repeated StorageProbeItem items = 1;
	int64 reported_at = 2; // 上报数据时间（agent 采集时打点，Unix 秒）
}
```

> StorageProbeReport 由 Agent 收到 `AgentSettings.storages`（仅含 `probeNodes`
> 匹配本节点条目）后自驱探测并上报。

### `AgentSettings` 与节点配置

Agent 在注册响应 (`RegisterResponse`) 或运行时配置热更 (`UpdateSettingsCommand`) 中接收由 Server 下发的节点本地运行配置 `AgentSettings`：

```protobuf
message AgentSettings {
	string exec_bin_path = 1;            // dc-exec 可执行文件路径
	VNCConfig vnc = 2;                   // VNC 服务与扫描配置
	MonitorConfig monitor = 3;           // 节点监控配置（GPU / 磁盘 / 采样周期）
	repeated StorageConfig storages = 4; // 分配由本节点负责探测的共享存储列表
	int32 probe_interval = 5;            // 共享存储探测间隔，单位秒（0 = 使用默认值 1h）
	string lmutil_path = 6;              // 节点生效的 lmutil 可执行文件绝对路径
}

message VNCConfig {
	string bin_dir = 1;                  // TurboVNC bin 目录
	int32 scan_interval = 2;             // VNC 进程扫描周期，单位秒，默认 1800 (30min)；0=不启用周期扫描
	bool scan_enabled = 3;               // 是否启用 VNC 周期扫描，默认 true
	repeated string server_args = 4;     // vncserver 启动默认参数
}

message MonitorConfig {
	string nvidia_smi_path = 1;          // GPU 相关配置（nvidia-smi 路径等）
	repeated MountTarget disk_mounts = 2;// 额外磁盘挂载监听列表
	int32 metrics_interval = 3;  // 资源指标上报间隔，单位秒
	bool metrics_enabled = 4;    // 是否启用资源指标上报
}

message MountTarget {
	string name = 1;                     // 显示名，如"项目盘"
	string path = 2;                     // 挂载路径，如 /mnt/project
	bool enabled = 3;                    // 是否启用采集，默认 true
	string os = 4;                       // 适用操作系统 (linux / windows / 空表示全部)
}

message StorageConfig {
	string id = 1;
	string mount_point = 2;
}
```

### 连接席位上报与调和 (ConnectionReport)

Agent 周期性扫描节点上的 VNC 席位与 XRDP X 席位，通过 `ConnectionReport` 向 Server 上报运行态切片。

```protobuf
message VNCSeatInfo {
	int64 connection_id = 1;
	string name = 2;
	string username = 3;
	int32 port = 4;
	int32 pid = 5;
	optional int32 display = 6; // 显示号。未设置表示无；0 表示 :0
	optional int64 started_at = 7;
}

message XRDPSeatInfo {
	int64 connection_id = 1;
	string name = 2;
	string username = 3;
	int32 pid = 4;
	optional int32 display = 5;
	bool session_ending_confirmed = 6; // 席位仍在但 logind 会话缺失/closing；断线保活不会置位
	optional int64 started_at = 7;
}

message ConnectionReport {
	int64 reported_at = 1;
	repeated VNCSeatInfo vnc_seats = 2;
	repeated XRDPSeatInfo xrdp_seats = 3;
}
```

#### 状态调和机制 (Reconciliation)

Server 端的 `SyncConnectionReport` 在接收到 Agent 的 `ConnectionReport` 后执行 VNC/XRDP 对账：

1. **VNC 席位调和 (`reconcileVNCSeats`)**：
   - **精确匹配与信息更新**：优先通过 `target_node_id + username + port` 对齐数据库记录；更新数据库中连接的运行时状态（标记为 `RUNNING`），回填实际进程 PID 与 Host 地址。
   - **外部连接自动发现 (Discovered)**：若上报的 VNC 席位在数据库中不存在对应记录，且满足节点准入规则，系统可自动创建 `discovered` 连接记录方便纳管。
   - **缺失连接停止与优雅期 (Grace Period)**：对原本处于运行态但在本轮上报中缺失的受管连接，系统在确认超出控制优雅期（Grace Period）后将其状态标记为 `STOPPED`，避免短时抖动导致误杀。
2. **XRDP 席位调和 (`reconcileXRDPSeats`)**：按 username（及 display）回写席位 `display`/`pid`；席位从探针消失时清字段，**不**自动把行打成 `stopped`（3389 共享监听仍可拨号）。
3. **即时采集触发**：
   - Server 可通过下发 `CollectProbeCommand(probe_name="connection-probe")` 指令，通知目标节点立即执行一次本地探测并回送最新 `ConnectionReport`。

### Server → Agent 消息

```protobuf
message ServerCommand {
	oneof payload {
		RegisterResponse register_response = 1;       // 注册响应
		HeartbeatResponse heartbeat_response = 2;     // 心跳响应
		CommandRequest command_request = 3;           // 执行命令
		CancelCommand cancel_command = 4;             // 取消任务命令
		RedirectCommand redirect_command = 5;         // leader 重定向命令
		CommandResultAck command_result_ack = 6;       // 执行结果确认
		CollectProbeCommand collect_probe_command = 7; // 触发一次节点探针即时采集 (connection-probe/metrics/storage-probe)
		UpdateSettingsCommand update_settings_command = 8; // 更新 Agent 配置
		RenewCertCommand renew_cert_command = 9;       // 证书续签：请求 Agent 生成密钥对与 CSR
		RenewCertResponse renew_cert_response = 10;    // 证书续签：回传签发后的证书与 CA
	}
}

message CollectProbeCommand {
	string probe_name = 1;    // 探针标识 (如 "connection-probe", "metrics", "storage-probe")
	string trace_id = 2;
	int64 requested_at = 3;
}

// 证书续签相关命令消息
message RenewCertCommand {
	string reason = 1; // 触发原因，如 "expiring"
}

message RenewCertResponse {
	bytes cert = 1;    // PEM 编码的已签发节点证书
	bytes ca = 2;      // PEM 编码的集群 CA 证书
}

message RenewCertRequest {
	bytes csr = 1;     // PEM 编码的证书签名请求（私钥不出节点）
}

message RenewCertResult {
	bool ok = 1;
	string message = 2;
}
```

### 命令执行

```protobuf
message CommandRequest {
	string trace_id = 1;                      // 唯一追踪标识符
	string execution_id = 2;                  // 业务幂等标识符
	string target_user = 3;                   // 目标用户名
	string action_name = 4;                   // 操作名称 (ActionName)
	string payload_json = 5;                  // 结构化参数 (JSON)
	map<string, string> env = 6;             // 环境变量
	google.protobuf.Duration timeout = 7;     // 超时时间
}
```

**说明**：

- `payload_json` 是 `internal/execution` 执行载荷的 JSON object 传输形态，Agent 收到后会按动作类型解码成具体 payload。
- `env` 只表示真实环境变量，不承载 `payload_json` 或其他内部传输字段。
- 调度器和 dc-exec 使用 `CommandSpec` 表达结构化命令，避免 Shell 拼接。
- `timeout` 使用 protobuf Duration，单位由字段类型表达。
- 采集类命令（VNC / status / storage）统一使用 `trace_id` 作为日志关联标识，不参与执行幂等，也不会暴露为 Portal HTTP 响应体字段。

```protobuf
message CommandResult {
	string trace_id = 1;              // 对应的追踪 ID
	bool success = 2;                // 执行成功状态（exit_code == 0）
	int32 exit_code = 3;              // 进程退出码
	string stdout = 4;               // 标准输出
	string stderr = 5;               // 标准错误（命令输出的错误，如 "file not found"）
	google.protobuf.Duration duration = 6;  // 执行耗时
	string system_error = 7;          // Agent 系统错误（如 fork failed, user not found, timeout）
}

message CommandResultAck {
	string trace_id = 1;              // 对应的追踪 ID
	bool persisted = 2;              // Server 是否已持久化结果
}
```

补充说明：

- Server 侧对同一 agent stream 的下行消息（执行命令、心跳 ACK、结果 ACK、redirect）统一串行发送，避免并发 `Send()`。
- 当 agent 连接被 health checker 判定为 stale 并移除时，server 会尽快同步数据库离线状态。
- 挂起中的 dispatch 请求在 server 停机或对应 agent 断连时会更快失败。

### Agent 注册与心跳

```protobuf
message RegisterRequest {
	string node_id = 1;           // 节点标识
	string version = 2;           // Agent 版本
	int64 registered_at = 3;      // 注册时间戳（Unix 秒）
	SystemInfo system_info = 4;   // 系统信息
}

message SystemInfo {
	string os = 1;                // 操作系统家族：Agent 编译目标 runtime.GOOS
	string arch = 2;              // CPU 架构：Agent 编译目标 runtime.GOARCH
	int64 cpu_cores = 3;          // CPU 核心数
	string os_name = 4;           // 操作系统产品名（连接注册时探测一次）
	string os_version = 5;        // 操作系统版本（连接注册时探测一次）
	string kernel_version = 6;    // 内核版本（可选；Windows 可为空）
}
```

Agent 在连接注册时通过 `probe.DetectSysInfo()`（`internal/agent/probe/sysinfo_*.go`）探测产品名/版本/内核，填入 `system_info`；心跳不更新这些字段。

```protobuf
message HeartbeatRequest {
	int64 reported_at = 1;        // 上报数据时间（agent 采集时打点，Unix 秒）
}


message RegisterResponse {
	bool accepted = 1;                       // 是否接受注册
	string message = 2;                      // 接受/拒绝消息
	string leader_node_id = 3;                 // 当前 leader 节点 ID（如有）
	string leader_addr = 4;                   // 当前 leader gRPC 地址（如有）
	AgentSettings agent_settings = 5;         // 下发给 Agent 的工具路径等配置
}

message HeartbeatResponse {
	int64 server_timestamp = 1;               // Server 时间戳
}

message Metrics {
	double cpu_util = 1;              // CPU 平均利用率 (0-1 比例数值，所有核平均，如 0.0131 表示 1.31%)
	int64 mem_total = 2;              // /proc/meminfo MemTotal，单位 KB (KiB)
	int64 mem_used = 3;               // /proc/meminfo MemTotal - MemAvailable，单位 KB (KiB)
	int64 swap_total = 4;             // /proc/meminfo SwapTotal，单位 KB (KiB)
	int64 swap_free = 5;              // /proc/meminfo SwapFree，单位 KB (KiB)
	double load1 = 6;                // /proc/loadavg 1 分钟负载
	double load5 = 7;                // /proc/loadavg 5 分钟负载
	double load15 = 8;               // /proc/loadavg 15 分钟负载
	int64 uptime = 9;                 // 运行时长
	int64 active_tasks = 10;          // 当前活跃任务数
	repeated GPUInfo gpus = 11;       // GPU 设备列表
	int64 cpu_cores = 12;             // CPU 核心数
	repeated DiskUsage disk_usages = 13; // 磁盘使用情况（自定义监控对象）
	int64 reported_at = 14;           // 上报数据时间（agent 采集时打点，Unix 秒）
}

// DiskUsage 磁盘使用情况（df 输出结构化）
message DiskUsage {
	string mount_point = 1;           // 挂载点，如 /
	int64 total = 2;                 // 总容量，单位 KB (KiB)
	int64 used = 3;                  // 已使用，单位 KB (KiB)
	string monitor_name = 4;         // 监控对象显示名（来自模板或自定义配置）
	string filesystem = 5;           // 文件系统类型，如 ext4, xfs, nfs4
}
```

### Server ↔ Server 转发消息

在多 Control 节点高可用架构中，Standby 节点将任务派发委托给 Leader，或各节点在收到内部推送时向集群其余 Server 广播事件：

```protobuf
enum NodeAffinity {
	NODE_AFFINITY_LOCAL_PREFERRED = 0;
	NODE_AFFINITY_ANY = 1;
}

enum RunMode {
	RUN_MODE_SYNC = 0;
	RUN_MODE_ASYNC_TRACKED = 1;
}

message ForwardDispatchRequest {
	CommandRequest request = 1;
	string source_node_id = 2;
	string target_node = 3;
	repeated string node_selector = 4;
	NodeAffinity node_affinity = 5;
	RunMode run_mode = 6;
}

message ForwardDispatchResponse {
	CommandResult result = 1;
	string execution_id = 2;
	bool accepted = 3;
}

message ForwardClusterEventRequest {
	string source_node_id = 1;
	string target_type = 2; // "user", "role", "broadcast"
	repeated string user_ids = 3;
	int32 role = 4;
	string event_name = 5;
	string payload_json = 6;
}

message ForwardClusterEventResponse {
	bool success = 1;
}
```

> [!TIP]
> **状态调和 (Reconciliation)**:
> `HeartbeatResponse` 只负责心跳确认和时间漂移检测。连接席位调和由 agent `connection-probe` 周期任务或
> `CollectProbeCommand` (probe_name="connection-probe")
> 触发后发送 `ConnectionReport`，server 侧 `SyncConnectionReport` 以 DB 为基线完成 VNC/XRDP 调和。

## Agent 连接行为

Agent 建立 cluster 连接时遵循固定顺序：

1. 读取 `Cluster.Servers` 列表
2. 按顺序拼接 `host:Cluster.Port`
3. 为目标地址建立 mTLS gRPC 连接
4. 打开 `ControlService/Sync` 双向流
5. 先发送 `RegisterRequest`
6. 再持续发送 `HeartbeatRequest`、`Metrics`、`ConnectionReport` 和 `CommandResult`
7. 持续接收 `RegisterResponse`、`HeartbeatResponse`、`CollectProbeCommand`、`CommandRequest`、`CancelCommand`、`RedirectCommand` 与 `RenewCertCommand`

> [!NOTE]
> **可靠性验证**: 该连接行为及其故障转移（Failover）逻辑已通过 `internal/agent/agent_test.go` 中的
`TestClusterAgent_Failover` 进行了内存网络级自动化验证，确保在 Leader 切换或网络重连时，Agent 能够正确执行 Strict Ordered
> Failover 逻辑。

### 重复连接替换 (Duplicate Connection Kick)

Server 对每个 `NodeID` 只保留最新一条 `Sync` 流。当同一 `NodeID` 的 Agent 重新连接时，Server 会执行以下操作：

1. 将旧 `AgentConnection` 标记为 `replaced`。
2. 向旧流发送 `RedirectCommand`（尽力而为，后台 goroutine 发送）。
3. 旧 `Sync` 处理循环在下一次 `Recv()` 返回后检测到 `replaced` 标志，立即退出并清理资源。

这一机制确保 Agent 重启或网络抖动引发的重连不会遗留僵尸 stream goroutine，同时也保证 `Unregister` 在退出时不会误删新连接。

## 错误处理

### gRPC 状态码

Cluster 协议使用标准 gRPC 状态码：

- `OK` (0): 成功
- `CANCELLED` (1): 操作取消
- `INVALID_ARGUMENT` (3): 无效请求参数
- `DEADLINE_EXCEEDED` (4): 操作超时
- `PERMISSION_DENIED` (7): 权限不足
- `UNAUTHENTICATED` (16): 需要认证
- `UNAVAILABLE` (14): 服务不可用
- `INTERNAL` (13): 内部服务器错误

### 错误处理示例

```go
if err != nil {
if st, ok := status.FromError(err); ok {
log.Printf("gRPC 错误: %s (代码: %s)", st.Message(), st.Code())

switch st.Code() {
case codes.Unauthenticated:
log.Println("需要认证")
case codes.PermissionDenied:
log.Println("权限不足")
case codes.DeadlineExceeded:
log.Println("操作超时")
case codes.Unavailable:
log.Println("Agent 不可用")
}
}
}
```

## 性能优化

### 请求超时

为不同操作设置合适的超时时间：

- **认证**: 5-10 秒
- **短命令**: 30 秒
- **长命令**: 5-30 分钟
- **健康检查**: 5 秒

### 连接复用

启用 gRPC 连接复用：

```go
conn, err := grpc.Dial(address,
grpc.WithTransportCredentials(creds),
grpc.WithKeepaliveParams(keepalive.ClientParameters{
Time:                10 * time.Second,
Timeout:             3 * time.Second,
PermitWithoutStream: true,
}),
)
```

## 运维与排障（外链）

监听端口、`[cluster]` 配置项、防火墙方向与证书落盘属于部署运维，不在此重复：

- [配置文件](../../deployment/config.md) — `[cluster]` 段落
- [节点注册与引导](../../deployment/node-registration.md) — OTP、mTLS 材料与在线续签
- [故障排查 · 证书握手失败](../../deployment/troubleshooting.md#证书握手失败) — CA 不一致、连接被拒、指纹未注册等

协议语义与消息行为见上文各节；gRPC 连通性可在节点上用 `grpcurl` 对 `ControlService/Sync` 做 mTLS 探测（证书路径以本机 `certs/cluster/` 为准）。
