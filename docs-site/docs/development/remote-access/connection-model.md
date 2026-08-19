---
title: 远程访问模型
linkTitle: 连接模型
weight: 150
description: connection 事实模型与 direct/proxy 接入
---

后端不负责规定 GUI “如何发起远程桌面连接”，它只负责维护连接记录并在需要时返回连接事实与受控访问能力。

对前端 GUI 来说，远程访问的核心对象只有一个：

- `connection`：后端保存的连接记录，包含目标地址、端口、用户名、状态等事实字段

---

## EDA 图形架构与桌面治理模型

在半导体与 IC 设计场景中，研发人员重度依赖图形化 EDA 工具（如 Cadence Virtuoso、Synopsys Verdi 等）。平台将图形交互划分为两个职责正交的体系：**交互常驻桌面（Fixed Workspace）** 与 **仿真作业 GUI（Job-attached GUI）**。

### 1. 核心维度对比

| 维度 | ① 交互常驻桌面 (Fixed VNC Workspace) | ② 仿真作业 GUI (Job-attached GUI) |
|---|---|---|
| **核心定位** | 工程师的**日常交互工作台**（画原理图、版图编辑、代码编写、任务分发） | 批处理仿真任务附带的**临时图形查看窗口**（波形查看、交互调试） |
| **生命周期** | **长周期常驻**（数天至数周），随下班休眠、上班重连 | **短周期临时**（随仿真 Job 生灭），Job 结束即销毁 |
| **资源特征** | **CPU/内存波动态**（操作时突发，阅读/思考时低负载） | **算力密集型**（多核满载、大内存占用） |
| **治理机制** | **固定节点分配 + cgroup 弹性上限**（不占调度器 Slot） | **LSF / SLURM 调度器统一排队分发**（受 Queue / Core 配额管理） |
| **中断影响** | 必须保证持续可用，防止未保存数据丢失 | 作业结束自然退出，窗口关闭不影响后续流程 |

---

### 2. 架构设计考量：为什么常驻桌面采用 Fixed VNC + cgroup？

将常驻办公桌面与 HPC 调度器解耦，并采用 **Fixed VNC + cgroup 动态上限**，主要基于以下工程考量：

1. **算力资源最大化利用（避免 Slot 空转）**：
   - HPC 调度器按 Slot/Core 刚性锁定算力。若将常驻桌面作为 Job 提交，桌面在空闲时段会持续占死 Slot 额度。
   - 采用 Fixed 节点 + cgroup 模式，桌面在空闲时自动将 CPU 释放给同节点的其他进程或后台计算，兼顾资源共享与突发性能。
2. **保障研发交互连续性**：
   - 研发人员需要开机即用、随连随走的工作台。独立常驻桌面免去了向调度队列排队等待资源的过程，且不受队列 `RUNLIMIT` 强制超时中断的影响。
3. **隔离单点异常**：
   - 通过 `systemd-run` 挂载 `user-<uid>.slice` 为每个桌面设置内存上限，防止单个 EDA 工具内存泄漏耗尽物理机资源；配合 `runuser --login` 初始化完整用户登录环境（加载家目录、`~/.bashrc`、`/etc/profile.d` 与 D-Bus 会话），既能实现资源配额管控，又规避了普通提权拉起桌面时因缺失 `$HOME` 导致的黑屏和权限异常。

---

### 3. 仿真作业 GUI 的集成方式

当作业运行过程中产生图形界面时，通过以下两种方式接入，不与 `connections` 桌面表产生数据耦合：

- **方式 1：X11 Remote DISPLAY 自动回投（行业主流）**：
  - 作业在常驻桌面内提交时，自动注入 `DISPLAY=<fixed-node>:<display>`。
  - 算力节点运行的仿真工具直接将 X11 窗口渲染回用户的常驻桌面上，平台无需维护额外会话。
- **方式 2：轻量临时 Web/客户端直连（包装脚本自闭环）**：
  - 算力节点通过标准包装脚本（`deployment/scripts/app/vnc/job-gui-wrapper.sh`）拉起临时会话：
    1. 启动 `vncserver -desktop dce-job-${JOB_ID}` 并校验 `$?` 确认启动成功；
    2. 自动从启动日志提取分配的 Display 与 Port；
    3. 写入当前工作目录私有元数据 `.dce_job_${JOB_ID}.json`；
    4. 注册 `trap` 退出陷阱，作业结束或被终止时自动清理 VNC 进程与临时文件；
  - 节点 Agent 探针识别到以 `dce-job` 命名的会话时自动跳过，不录入 `connections` 核心表；
  - 用户需要接入时，服务端直接依据作业目录下的元数据建立即时代理通道。

---

## 设计边界

### 后端负责什么

- 创建、保存、查询、删除 `connection`（针对常驻工作台与静态接入点）
- 维护连接生命周期状态（DB 状态为 `unknown` / `running` / `stopped`；`deleted` / `accessed` 是事件语义）
- 提供连接生命周期控制接口（`POST /api/connections/:id/start|stop|restart`）：经由目标节点 Launcher 启动/停止 VNC 会话
  （普通用户直接以目标身份运行 vncserver；提权场景走 systemd-run cgroup + runuser 登录环境变体），并乐观更新本地状态
- 在需要时返回连接详情与受控下载能力
- 删除或停止连接时的副作用清理：可定位节点+display 时向目标节点下发远程 `vncserver stop` 防止孤儿进程；关闭该连接活跃的 proxy 隧道
- 对远程访问提供可选的 proxy 能力（创建代理会话、签发 OTP、暴露 WebSocket 入口），当前支持 VNC、RDP、SSH 等协议

### 后端不负责什么

- 不规定 GUI 是否使用本地 Viewer、内嵌控件还是其他客户端
- 不规定 GUI 如何拼接自定义协议 URL
- 不接管 GUI 到目标端的直连行为
- 不把“远程访问产品体验”拆成多套后端模式
- 不把 HPC 调度器的作业当做 Connection 存储或跨域管理

换句话说：后端只维护事实和能力，前端自己决定接入方式。

---

## Connection 作为统一后端事实

远程访问相关的后端事实记录统一落在 `connection`。

典型字段包括：

- 目标地址 `host`
- 目标端口 `port`
- 用户名 `username`
- 连接状态（`unknown` / `running` / `stopped`）
- 最后访问时间
- 目标节点 `targetNodeId`
- Direct 客户端访问地址由前端根据 connection 字段计算：优先 `nodeClientHost`，否则 `host`，最后回退 `targetNodeId`

当前约束：

- DB 是 connection **运行态**的唯一事实源；列表查询前置一层内存缓存（默认 60s，跟随 `connection.connectionListCacheTTL`，带全局 Janitor），写入路径实时失效，短暂陈旧属预期
- VNC 明文密码不持久化在 connection 记录中
- 前端不直接获取 VNC passwd 文件；direct VNC 通过 TurboVNC 协议层 OTP 启动 Viewer，proxy 模式通过 Engine proxy OTP 完成 WebSocket 认证
- 连接来源 `source` 共三类：
    - `fixed`：管理员预置连接（管理员经 `POST /api/admin/system/connections` 创建，配套 admin 侧 OTP / start-stop-restart / refresh 能力）；仅管理员可删除
    - `manual`：用户手工创建（`POST /api/connections`，填 `alias`，`name` 由后端生成）；不参与节点状态同步，也不计入连接上限
    - `discovered`：agent VNC 探针采集发现未在 DB 中登记的物理 VNC 会话时自动补录
- `shared` 连接对所有用户可见可连（典型如共享 Windows RDP）；共享连接变更经 SSE 广播全员，私有连接变更只推送 owner
- 手工创建 connection 的 `alias` 必填且有长度上限；同一目标的 `host+port` 组合受唯一性约束，重复创建报"连接已存在"
- connection 清理为手动触发（`POST /api/admin/system/connections/cleanup`），不自动执行以避免静默删除；定期清理探针发现的废弃连接
- agent VNC 采集发现无 `connectionId` 的本地 VNC 进程时，会按节点、用户、端口补录为 `discovered` connection，避免同一运行时重复入库
- **Windows 静态共享 RDP 连接**：Windows 节点不依赖多进程 VNC 扫描。节点以 `os=windows` 完成集群注册时，Server 创建一条静态共享连接（`protocol: "RDP"`、`shared: true`、`source: fixed`），此时尚未探测，状态为 `unknown`。心跳携带 `HeartbeatRequest.rdp`（`RDPStatus.available` 与注册表 `WinStations\RDP-Tcp\PortNumber`，缺省 3389）时，只调和该行的 `host` / `status` / `port`，并广播连接列表刷新；别名和最近访问时间不会被心跳整行覆盖。同一节点同一协议最多一条共享记录。心跳不带 RDP 状态（Linux 节点、探活不可判定）时不改已有状态和端口。

---

## 当前 Proxy 的两种前端接入方式

在这套设计里，当前 Proxy 实现只有两种前端接入方式：

| 模式       | 含义                                                                                       | 后端职责                                |
|----------|------------------------------------------------------------------------------------------|-------------------------------------|
| `direct` | GUI 根据 connection 字段计算直连目标并连接当前目标服务；VNC 场景可按需申请 TurboVNC OTP，不提供 VNC passwd 文件下载能力       | 返回 connection 信息；为运行中 VNC 签发协议层 OTP |
| `proxy`  | GUI 先向 Engine 申请代理会话，再通过当前 Portal API origin 的 WebSocket 接入 proxy 实现（支持 VNC、RDP、SSH 等协议） | 创建 proxy 会话，返回 `connectionId + otp` |

这两种模式是前端接入策略，而不是两套后端产品形态。

---

## Direct 模式

### Direct 模式语义

GUI 直接消费 connection 事实字段并自行连接目标服务。`targetNodeId` 仍是集群内部节点身份，只有在缺少 `nodeClientHost` 和
`host` 时作为最后 fallback；如果节点配置了 `clientHost`，API 会在 connection 上返回 `nodeClientHost`，前端直连时应优先使用它。

### 后端职责

- 返回 connection 详情
- 为运行中的 VNC connection 按需生成 TurboVNC 协议层 OTP，并记录 access time
- 维护 connection 状态

### 前端职责

- 决定使用什么客户端或控件
- 决定是否唤起本地 Viewer
- 自行按 `nodeClientHost || host || targetNodeId` 得到 host，并连接到该 host 和 `connection.port`
- VNC Viewer 启动前调用 `POST /api/connections/:id/vnc-otp` 获取本次 TurboVNC one-time password

### Direct 模式适用场景

- 用户设备能直达目标节点
- 不需要 Engine 中转流量
- GUI 希望保留本地客户端体验

### Direct VNC OTP

`POST /api/connections/:id/vnc-otp` 面向 direct VNC Viewer。Server 校验 connection 归属与运行态后，通过目标节点 launcher 以
connection owner 身份执行：

```bash
/opt/TurboVNC/bin/vncpasswd -o -display :1
```

实际 `vncpasswd` 路径来自 `[vnc]` 配置。TurboVNC 可能把 `Full control one-time password: <otp>` 输出到 stderr，因此
launcher 会合并 stdout/stderr 解析。接口只返回 full-control one-time password，不返回 VNC passwd 文件，也不持久化该 OTP；该
OTP 由 TurboVNC session 管理，只适用于一次成功 Viewer 连接，失败或重连应重新申请。

---

## Proxy 模式

### Proxy 模式语义

GUI 不直接连接目标服务，而是先向 Engine 申请代理会话，然后通过 `portal` 下的 WebSocket 入口接入。
后端内部以运行时 session 对象实现 OTP 校验与状态机，但调用者只需要理解 proxy 模式即可。

### 相关接口

- `POST /api/connections/:id/proxy-session`
- `GET /api/connections/:id/proxy-session`
- `DELETE /api/connections/:id/proxy-session`
- `GET /api/connection-proxy/ws`

### 当前错误语义

- `POST /api/connections/:id/proxy-session`
    - `400 Bad Request`：连接缺少可代理的协议、目标地址或目标端口，或协议不受支持
    - `403 Forbidden`：连接协议对应的开关未启用（如 `vncEnabled=false`）
    - `429 Too Many Requests`：达到全局隧道上限（`tunnel.maxTunnels`），或达到单用户隧道上限（`tunnel.maxTunnelsPerUser`）
    - `503 Service Unavailable`：当前未启用 tunnel 服务（`tunnel.enabled=false`）
- `GET /api/connections/:id/proxy-session`
    - `404 Not Found`：当前连接没有活跃 proxy 会话
    - `503 Service Unavailable`：当前未启用 tunnel 服务（`tunnel.enabled=false`）
- `DELETE /api/connections/:id/proxy-session`
    - `404 Not Found`：当前连接没有活跃 proxy 会话
    - `503 Service Unavailable`：当前未启用 tunnel 服务（`tunnel.enabled=false`）

### 典型流程

1. GUI 调用 `POST /api/connections/:id/proxy-session`
2. 后端创建 proxy 会话，返回：
    - `connectionId`
    - `otp`
3. GUI 使用当前 Portal API origin 建立 WebSocket：`/api/connection-proxy/ws`
4. GUI 通过 `Authorization` 或 `Sec-WebSocket-Protocol` 携带 OTP
5. Engine 验证 OTP 后开始代理字节流

### Proxy 模式适用场景

- 用户设备不能直达目标节点
- 希望统一通过 Engine 暴露入口
- 浏览器 / WebView / 受限网络环境

当前协议代理实现的详细规则见 [协议代理](./proxy.md)。

---

## 推荐的理解方式

前端不需要把“远程访问”理解成多套后端模式，只需要这样理解：

- 先拿到 `connection`
- 如果当前策略是 `direct`，按 `nodeClientHost || host || targetNodeId` 计算 host 后连接
- 如果当前策略是 `proxy`，再额外申请一个 proxy 会话
- 需要启停 VNC 会话时调用生命周期接口：`POST /api/connections/:id/start|stop|restart`
  （`202 Accepted`；未知动作 `404`）。删除运行中连接时后端会远程停止 VNC 进程并关闭其 proxy 隧道
- 如果批量删除 connection，接口采用"首个错误即整体中止"语义：
    - 任一目标无权限删除：整体 `403 Forbidden`
    - 不存在的 ID 会被静默忽略（不报 `404`）
    - 无部分失败聚合语义

因此，`connection-model.md` 应该是 connection 与 proxy 能力的职责说明，而不是 GUI 协议唤起文档。

---

## 代码对应关系

- `GET /api/connections/:id` 只返回 connection 事实，不返回 VNC 明文密码
- `POST /api/connections/:id/vnc-otp` 为当前运行中的 VNC connection 生成 TurboVNC 协议层 OTP
- `POST /api/connections/:id/start|stop|restart` 经目标节点 Launcher 控制用户级 VNC 会话（普通身份直接执行 vncserver；
  提权场景为 root 执行 systemd-run cgroup + runuser 登录环境变体），并乐观更新状态
- 管理员侧具备完整对等能力：admin 连接 CRUD（含 `fixed` 来源）、admin vnc-otp、admin start/stop/restart、admin refresh、
  cleanup（见 `internal/server/portal/api/connection_systemadmin.go`）
- `POST /api/connections/refresh` 刷新当前用户的 Fixed VNC connection，不触发节点扫描；同用户短时间内的重复刷新会命中冷却去重窗口被静默忽略
- `POST /api/admin/system/connections/refresh` 异步触发一次全节点 VNC 采集，加速状态收敛
- `POST /api/connections/:id/proxy-session` 为当前 connection 创建运行时 proxy 会话
- 前端连接当前 Portal API origin 的 `/api/connection-proxy/ws`，并通过 OTP 完成本次 proxy 认证

VNC 采集会从节点本地 `Xvnc` / `Xtigervnc` 进程读取 PID、用户、desktop、`-rfbport` 等信息并回写 connection。
`connection.port` 表示真实 VNC TCP 端口，不是 display 号。
无 `connectionId` 的 VNC 进程按节点、用户、端口作为运行时唯一键补录；已有受管 connection 在节点采集中找不到对应运行时进程时会标记为
stopped，而不是直接物理删除。

关键实现：

- `internal/server/portal/api/connection.go`
- `internal/server/service/proxy/proxy.go`
- `docs-site/docs/development/remote-access/proxy.md`

> 概念层的两种接入方式（Direct / Proxy）说明见 [远程访问设计](./connection-model.md)。

---
