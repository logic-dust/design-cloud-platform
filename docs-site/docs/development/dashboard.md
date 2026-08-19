---
title: Dashboard 数据架构
linkTitle: Dashboard 规范
weight: 55
description: Dashboard 数据架构、前后端精度分工与多调度器状态规范
---

本文档阐述 Design Cloud Engine 的 Dashboard 监控面板的数据治理、前后端精度分工以及多集群调度器（LSF/Slurm）作业状态的统一归一化架构。

---

## 1. 数据精度与前后端分工原则

为了确保系统具有单一事实来源（Single Source of Truth）并规避 Web 前端 JavaScript 的二进制浮点数计算失真，Dashboard 建立了严格的前后端职责边界：

### 1.1 后端治理（后端负责 100% 数据精度）
- **容量与负载**：全网物理内存、GPU 显存、共享存储容量及 1m/5m/15m 系统负载均由后端在 Go 语言服务层完成格式化（`format.RoundTo(val, 2)`），统一保留两位小数输出。
- **利用率**：CPU 使用率、GPU 使用率、存储利用率在 API 中统一输出为 `0 ~ 1` 范围的比例小数，保留 4 位小数（例如 `0.1234`），乘以 100 即精准对应 `12.34%`。

### 1.2 前端渲染（纯粹展示与派生计算格式化）
- **后端直传字段 $\rightarrow$ 原样展示**：对于 API 直接返回的容量、负载、核心数、节点数和作业数，前端直接渲染 DOM，不进行任何 `.toFixed()` 或四舍五入。
- **前端派生计算 $\rightarrow$ 保留两位小数**：仅当在前端 JS 中进行乘除法计算派生显示（如 `utilization * 100` 或 `used / total`）时，运算后使用 `.toFixed(2)` 规范输出，消除 JS 浮点数计算误差。
- **动画帧渲染**：前端数字增量动画（`animateNum`）在计算过渡帧时，自动按目标数值的精度格式化，避免过渡帧展示未格式化的长浮点数。

---

## 2. 多调度器作业状态映射架构

Design Cloud Engine 支持无缝接入 **IBM Spectrum LSF** 和 **Slurm Workload Manager** 等多种 HPC 调度器。Dashboard 对外提供统一的 5 种作业生命周期状态：

| 规范状态 | JSON 键名 | 状态含义 | LSF 原始状态码 | Slurm 原始状态码 |
|---|---|---|---|---|
| **Running** | `running` | 运行中 | `RUN` | `RUNNING`, `R`, `COMPLETING`, `CG` |
| **Pending** | `pending` | 排队/等待中 | `PEND`, `WAIT` | `PENDING`, `PD`, `REQUEUED`, `RQ` |
| **Suspended** | `suspended` | 已暂停/挂起 | `PSUSP`, `SSUSP`, `USUSP` | `SUSPENDED`, `S` |
| **Completed** | `completed` | 已完成 | `DONE` | `COMPLETED`, `CD` |
| **Failed** | `failed` | 已失败/取消 | `EXIT`, `UNKWN`, `ZOMBI` | `FAILED`, `F`, `CANCELLED`, `CA`, `TIMEOUT`, `NODE_FAIL`, `OUT_OF_MEMORY` |

---

## 3. 统一调度领域模型与主机采集规范

Dashboard API 采用统一的 `scheduler` 领域对象聚合 HPC 调度的计算节点、作业态势与队列信息：

```json
{
  "scheduler": {
    "hosts": { "online": 16, "total": 16 },
    "jobs": { "running": 5, "pending": 2, "suspended": 0, "completed": 100, "failed": 3 },
    "queues": [
      { "name": "normal", "allocated": 100, "running": 80, "pending": 15, "suspended": 3, "completed": 400, "failed": 2 }
    ]
  }
}
```

### 3.1 主机状态直连采集 (`ExecuteListHosts`)
- **LSF 集群**：调用 `bhosts -json` 采集集群计算主机状态，解析 `STATUS`（`ok`, `closed_Full` 标记为在线，`unreach`, `unavail` 标记为离线）。
- **Slurm 集群**：调用 `sinfo --json` 采集节点状态，解析节点状态掩码（`idle`, `allocated`, `mixed` 标记为在线，`down`, `drain`, `fail` 标记为离线）。
- **执行方式**：计算主机状态查询为系统只读命令，使用 `ActionModeDirect` 以 Agent 进程身份直接执行，无需特权提权或降权。

### 3.2 LSF 全局作业采集约束
在进行全网作业状态汇总统计时，LSF 的 `bjobs` 指令默认仅返回当前执行用户自身的作业。为了保证全网全用户作业统计的准确性，后端驱动自动为全局查询附加 `-u all` 参数（即 `bjobs -json -o ... -a -u all`），实现跨用户全量作业的完整采集。

### 3.3 缓存与刷新节律
- **缓存单元**：聚合层采用 `schedulerCache cache.Cell[SchedulerStats]` 进行原子缓存。
- **刷新间隔**：受 `settings.dashboard.schedulerRefreshInterval`（默认 `600s` / 10 分钟）控制，保证调度数据聚合平稳低开销。

---

## 4. 实时推送流协议 (`GET /api/dashboard?stream=true`)

Dashboard 支持通过 HTTP Server-Sent Events (SSE) 建立实时长连接推送，避免客户端轮询开销。

### 4.1 连接管理与生命周期
- **端点**：`GET /api/dashboard?stream=true`
- **鉴权**：携带 `Authorization: Bearer <token>`；若平台启用了公开 Dashboard，持有 `PublicDashboardToken` 的请求亦可免登录以访客身份接入。
- **生命周期**：客户端仅在处于 Dashboard 页面时保持连接，离开页面时主动关闭连接。
- **超时豁免**：服务端内置超时 Skipper，`stream=true` 时自动豁免常规 API 的 60s 请求超时限制。

### 4.2 帧格式契约
- **事件名**：采用命名事件 **`event: dashboard`**。客户端须通过专用监听器订阅（如 JS 中的 `addEventListener("dashboard", ...)`）。
- **心跳机制**：数据无变化时，服务端每 30 秒发送一次保活注释行（`: ping`）。
- **Payload 载荷结构**：
  ```json
  {
    "data": {
      "sampledAt": "2026-09-25T10:00:00Z",
      "nodes": { "online": 16, "total": 16 },
      "resources": { "cpuUsedPercent": 45.2, ... },
      "scheduler": { ... },
      "storage": { ... }
    },
    "version": "1.0.0"
  }
  ```
- **变更驱动与指纹检测**：
  1. 连接建立后，服务端**立即推送首帧**完整快照；
  2. 此后按数据变更驱动推送——仅当底层数据块的 `sampledAt` 采样时间戳发生变化时才下发新帧；
  3. 当载荷中的 `version` 与客户端当前版本不一致时，表示服务端引擎已完成热升级，客户端应主动触发界面整页重新加载。


