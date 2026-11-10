---
title: 状态概览看板与实时事件流
linkTitle: 概览与事件流
weight: 30
description: Overview 大屏监控数据架构、前后端精度分工、多调度器状态归一化与基于 HTTP SSE 的实时事件推送契约
aliases:
  - /docs/development/dashboard-overview
  - /docs/development/events-and-streaming
---

Design Cloud Engine（DCE）为 Web 控制台与桌面客户端提供了一套统一的高性能实时状态展示与事件通知体系：通过服务端收敛的多源指标聚合，保障复杂集群与调度器状态的毫秒级大屏呈现；通过标准的 HTTP Server-Sent Events (SSE) 单向推送通道，以轻量「失效信号驱动」取代笨重的客户端高频轮询。

---

## 1. 概览监控数据治理与前后端精度规范

为了规避 JavaScript 原生双精度浮点数计算失真，系统建立了严格的前后端职责边界：

### 1.1 后端治理原则（后端负责 100% 数据精度）

- **容量与负载**：全网物理内存、GPU 显存、共享存储容量及 1m/5m/15m 系统负载均由后端在 Go 语言服务层完成格式化（`format.RoundTo(val, 2)`），统一保留两位小数输出。
- **利用率比例**：CPU 使用率、GPU 使用率、存储利用率在 API 中统一输出为 `0 ~ 1` 范围的比例小数，保留 4 位小数（例如 `0.1234` 对应 `12.34%`）。

### 1.2 前端渲染规范（对齐统一展示口径）

- **容量/数量展示（`fmtQuantity`）**：$\ge 100$ 时四舍五入取整并加千分位（如 `128 GB`、`1,024`）；$< 100$ 时保留 1 位小数（如 `48.0 GB`、`16.5`）。
- **整数计数（`fmtCount`）**：节点数、配置核心数等离散整数统一按千分位格式化输出。
- **利用率百分比**：统一格式化为 1 位小数（如 `12.5%`）。
- **动画帧平滑**：前端数字增量动画（`animateNum`）在计算过渡帧时，自动按目标精度的格式化器渲染，消除闪烁长浮点数。

---

## 2. 多调度器作业状态映射架构

平台无缝对接 **IBM Spectrum LSF** 与 **Slurm Workload Manager** 等主流外部集群调度器。概览看板与 API 对外统一归一化为 5 种作业状态生命周期：

| 规范状态 | JSON 键名 | 业务含义 | LSF 原始状态码 | Slurm 原始状态码 |
|:---|:---|:---|:---|:---|
| **Running** | `running` | 运行中 | `RUN` | `RUNNING`, `R`, `COMPLETING`, `CG` |
| **Pending** | `pending` | 排队等待资源 | `PEND`, `WAIT` | `PENDING`, `PD`, `REQUEUED`, `RQ` |
| **Suspended** | `suspended` | 暂停或挂起 | `PSUSP`, `SSUSP`, `USUSP` | `SUSPENDED`, `S` |
| **Completed** | `completed` | 成功结束 | `DONE` | `COMPLETED`, `CD` |
| **Failed** | `failed` | 异常失败/取消 | `EXIT`, `UNKWN`, `ZOMBI` | `FAILED`, `F`, `CANCELLED`, `CA`, `TIMEOUT`, `NODE_FAIL`, `OUT_OF_MEMORY` |

---

## 3. 全局实时事件推送流 (`GET /api/events`)

系统采用标准 HTTP `text/event-stream` (SSE) 单向推送流，复用标准 HTTP/HTTPS 端口与 Session 鉴权，穿透企业防火墙和代理网关。单节点本地 `push.Hub` 对并发 SSE 客户端有硬上限（当前 **4096**）；超额新连接返回 **503**，避免无界占用内存。

### 3.1 核心设计理念：失效信号驱动 (Signal-Driven Refresh)

- **轻量失效信号**：推送流只下发轻量 `refresh` 信号或用户提醒 `notify`，绝不直接广播大体积业务实体快照。
- **避免状态错乱**：客户端收到 `refresh` 信号后，通过标准 RESTful API 重拉最新视图。保证数据库始终为唯一事实源，消除客户端本地拼装增量数据导致的不一致。

### 3.2 载荷契约与事件标签

```json
{
  "action": "refresh",
  "targetId": "42",
  "notify": {
    "title": "作业完成",
    "message": "您的仿真作业 #42 已顺利执行完毕",
    "level": "success"
  },
  "extra": {}
}
```

| SSE 事件标签 | 触发业务场景 | 目标受众 | 客户端响应策略 |
|:---|:---|:---|:---|
| **`job`** | 作业提交、排队、取消或终结 | 作业所有者 | 若处于作业列表页，局部或全量更新作业状态 |
| **`ticket`** | 工单新建、流转、状态变更、评论 | 工单报告人与经办人 | 刷新工单看板或更新评论列表 |
| **`node`** | 节点上线、离线或心跳异常 | 系统管理员 | 刷新节点监控表格 |
| **`connection`** | 桌面会话建立、注销或断开 | 桌面所有者 | 更新桌面卡片状态与连通性指示 |
| **`session`** | 会话过期或管理员强制踢下线 | 被操作用户 | 捕获 `action="logout"`，立即清除 Token 并跳回登录页 |
| **`system`** | 系统维护公告、全局告警 | 全员或管理员 | 界面弹出全局 Toast 提示 |

### 3.3 审计日志实时增量流 (`GET /api/admin/audit/logs/{name}/stream`)

面向安全管理员与审计人员，提供类似 `tail -f` 的实时增量输出。仅在进入审计日志页面时按需建立连接，离开页面时即时关闭，服务端将最新落盘的日志行直接推向前端终端。
