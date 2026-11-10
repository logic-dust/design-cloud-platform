---
title: 连接生命周期模型
linkTitle: 连接模型
weight: 10
description: connection 事实模型、EDA 桌面治理模型与 direct/proxy 接入
aliases:
  - /docs/development/remote-access/connection-model
  - /docs/architecture/remote-access/connection-model
  - /docs/architecture/remote-access/vnc-display-allocation
---

后端不负责规定 GUI “如何发起远程桌面连接”，它只负责维护连接记录并在需要时返回连接事实与受控访问能力。

对前端 GUI 来说，远程访问的核心对象只有一个：

- `connection`：后端保存的连接记录；`port` / `nodeId` / `status` 等为库内事实，`host` 为读时从节点 `dialHost` 解析的 API 投影（见下文实例层）

### 本文结构

1. [EDA 图形架构与桌面治理模型](#eda-图形架构与桌面治理模型)
2. [设计边界](#设计边界)
3. [连接三层架构（规格 · 授权 · 实例）](#连接三层架构规格--授权--实例)（含 `host` 投影约定）
4. [Direct / Proxy 接入](#当前-proxy-的两种前端接入方式)
5. [实现索引](#实现索引便于维护者对照)

---

## EDA 图形架构与桌面治理模型

在半导体与 IC 设计场景中，研发人员重度依赖图形化 EDA 工具（如 Cadence Virtuoso、Synopsys Verdi 等）。平台将图形交互划分为两个职责正交的体系：**交互常驻 VNC 工作台（连接规格 + 实例）** 与 **仿真作业 GUI（Job-attached GUI）**。

### 1. 核心维度对比

| 维度 | ① 交互常驻 VNC 工作台 | ② 仿真作业 GUI (Job-attached GUI) |
|---|---|---|
| **核心定位** | 工程师的**日常交互工作台**（画原理图、版图编辑、代码编写、任务分发） | 批处理仿真任务附带的**临时图形查看窗口**（波形查看、交互调试） |
| **生命周期** | **长周期常驻**（数天至数周），随下班休眠、上班重连 | **短周期临时**（随仿真 Job 生灭），Job 结束即销毁 |
| **资源特征** | **CPU/内存波动态**（操作时突发，阅读/思考时低负载） | **算力密集型**（多核满载、大内存占用） |
| **治理机制** | **连接规格落点 + cgroup 弹性上限**（不占调度器 Slot） | **LSF / SLURM 调度器统一排队分发**（受 Queue / Core 配额管理） |
| **中断影响** | 必须保证持续可用，防止未保存数据丢失 | 作业结束自然退出，窗口关闭不影响后续流程 |

---

### 2. 架构设计考量：为什么常驻 VNC 工作台与 HPC 解耦并采用 cgroup？

将常驻办公桌面与 HPC 调度器解耦，并在拉起路径上使用 **VNC + cgroup 动态上限**，主要基于以下工程考量：

1. **算力资源最大化利用（避免 Slot 空转）**：
   - HPC 调度器按 Slot/Core 刚性锁定算力。若将常驻桌面作为 Job 提交，桌面在空闲时段会持续占死 Slot 额度。
   - 工作台落在规格选定的节点上，配合 cgroup 模式，桌面在空闲时可将 CPU 释放给同节点的其他进程或后台计算，兼顾资源共享与突发性能。
2. **保障研发交互连续性**：
   - 研发人员需要开机即用、随连随走的工作台。独立常驻桌面免去了向调度队列排队等待资源的过程，且不受队列 `RUNLIMIT` 强制超时中断的影响。
3. **隔离单点异常**：
   - 通过 `systemd-run` 挂载 `user-<uid>.slice` 为每个桌面设置内存上限，防止单个 EDA 工具内存泄漏耗尽物理机资源；配合 `runuser --login` 初始化完整用户登录环境（加载家目录、`~/.bashrc`、`/etc/profile.d` 与 D-Bus 会话），既能实现资源配额管控，又规避了普通提权拉起桌面时因缺失 `$HOME` 导致的黑屏和权限异常。

---

### 3. 仿真作业 GUI 的集成方式

当作业运行过程中产生图形界面时，通过以下两种方式接入，不与 `connections` 桌面表产生数据耦合：

- **方式 1：X11 Remote DISPLAY 回投（作业提交侧配置）**：
  - 在常驻 VNC 工作台内提交作业时，由作业脚本或调度环境自行设置 `DISPLAY`（指向工作台节点上的显示号），算力节点上的 GUI 进程将窗口渲染回工作台。
  - Engine **不会**在调度提交流程中自动注入 `DISPLAY`；是否回投取决于站点包装脚本与提交方式。
- **方式 2：作业节点本地临时 VNC（包装脚本自闭环）**：
  - 算力节点通过 RPM 自带的作业包装脚本（安装路径 `/usr/share/design-cloud-engine/scripts/vnc/job-gui-wrapper.sh`，源码 `deployment/share/vnc/job-gui-wrapper.sh`）在作业工作目录拉起临时 TurboVNC：
    1. 启动 `vncserver -desktop dce-app-${JOB_ID}`（**默认桌面名前缀必须是 `dce-app`**），将启动日志写入 `.dce_app_${JOB_ID}.log` 并校验退出码；
    2. 从日志解析分配的 display，计算 `port = 5900 + display`；
    3. 在本进程内 `export DISPLAY=:<display>` 后前台执行目标命令；
    4. 注册 `trap`，作业结束或被终止时 `vncserver -kill` 并删除日志；
  - 节点 Agent 探针识别桌面名以 **`dce-app`** 开头的 VNC 席位时跳过，不录入 `connections` 表（作业 GUI 与其它 App 席位共用这一前缀）；
  - **当前实现**：Engine 不读取作业目录元数据，也不为作业 GUI 签发 proxy 会话；用户需按节点可达性自行直连 `hostname:port`（或站点另建隧道）。若未来要在 Portal 内一键接入作业 GUI，需单独设计元数据上报与代理入口。

#### 为何用 `dce-app` 命名，而不是探测「是否完整桌面」？

| 协议 | 区分手段 | 说明 |
|------|----------|------|
| **RDP** | OS 会话形态 | Windows 可区分完整桌面（`explorer`）与 RemoteApp（`rdpshell`）——探针/Stop 已用 |
| **VNC** | **显式命名约定** `dce-app*` | 在 OS 上看都是「一个 X 席位」：单应用与完整 DE 没有稳定、跨发行版的进程特征。用桌面壳白名单（gnome-shell 等）误判多、维护成本高，故采用**启动方声明**：App/作业 GUI 必须把 VNC `-desktop` 设为 `dce-app-…`；门户托管工作台**不要**使用此前缀 |

作业包装脚本已默认使用 `dce-app-${JOB_ID}`；站点自写启动脚本须遵守同一前缀，否则可能被探针当成野生桌面入库。

---

## 设计边界

### 后端负责什么

- 创建、保存、查询、删除 `connection`（针对常驻工作台与静态接入点）
- 维护连接生命周期状态（DB 仅持久化 `pending` / `running` / `stopped` / `terminating`）。API 列表/详情会对 **online 以外** 的节点做读时派生：节点 `offline` → 展示 `unknown`，节点 `maintenance` → 展示 `maintenance`（不写回 connection 行）。`terminating` 为删除墓碑，对用户列表隐藏。VNC Launch/Start 占坑为 `pending`；Agent 成功后为 `running`。RDP 为拨号登记（`running` / `stopped`）。**SSH** 不写 connection 行，走 resource proxy。**Linux XRDP** 同样为拨号登记，并可通过 dc-exec 对 X 席位 `loginctl`/信号停会话；`connection-probe` 回写 `display`/`pid`，活席位消失或 Logout 收尸后将行置 `stopped` 墓碑（保留 `resource_id`）
- 提供连接生命周期控制接口：`POST /api/connections/:id/start|stop|restart`（仅 **VNC / RDP / XRDP** 实例行。Stop/Delete 经 `teardownRemoteSession`：VNC/XRDP 杀 X 席位；RDP 经 `SESSION_CONTROL` 踢登录会话。**不**停止 xrdp / TermService 守护进程）。**SSH 不走本接口**：无实例行，关 WebSocket 即结束隧道
- 在需要时返回连接详情与受控下载能力
- 删除或停止 **实例行** 时的副作用清理：按协议 teardown 远程会话；关闭该 connection 锚点上活跃的 proxy 隧道
- 对远程访问提供可选的 proxy 能力（一次 WebSocket Upgrade + Portal Session 建隧）：VNC/RDP/XRDP 锚 connection；SSH 锚 resource（见下）

### 后端不负责什么

- 不规定 GUI 是否使用本地 Viewer、内嵌控件还是其他客户端
- 不规定 GUI 如何拼接自定义协议 URL
- 不接管 GUI 到目标端的直连行为
- 不把“远程访问产品体验”拆成多套后端模式
- 不把 HPC 调度器的作业当做 Connection 存储或跨域管理

换句话说：后端只维护事实和能力，前端自己决定接入方式。

---

---

## 连接三层架构（规格 · 授权 · 实例）

平台采用正交解耦的三层连接资源模型：

1. **规格层 (`connection_resources`)**：
   - 管理员定义的算力与网络连接入口规范（协议 VNC/RDP/SSH/XRDP、候选节点池 `nodes` 与节点组 `nodeGroups`、VNC Display 约束、端口、每人实例上限 `maxPerUser`、`vncConfig`、公开标记 `public`）。
   - **Display 校验规则**：仅当 `len(nodes) == 1 && len(nodeGroups) == 0` 时允许指定 Display（`nil` = 动态探测分配；非 `nil` = 固定桌面号，规格 API 允许 `1..99`，与动态选号范围一致）。库存与 HTTP JSON 中的 `display` 是整数或 `null`。进程命令行上的 `:N`（含探针看到的 `:0`）只在探针解析和拉起命令时出现，**不**作为规格可写值。`maxPerUser` / `maxTotal` 保存时对全协议仍做 schema 校验（落在 `1…maxPerUserCap` 等）；对 **VNC/RDP/XRDP** 约束实例行数与 usedCount。
   - **SSH 与 `maxPerUser` / `maxTotal`**：**不做运行时 enforce，也不在管理端当作可调并发**。原因：SSH 无 `connections` 行，规格配额本意是「每人几条实例」；若只在受理 Upgrade 的**单台** Engine 上计数，多 Portal 节点 / LB 下「每人上限」会漂，语义假正确。因此：**不**按规格挡第二条 SSH 隧道。本机并发只靠 `connection.proxy.maxProxyConnections*`；同 `(resourceId, requester)` 再 Upgrade **顶替**本机旧隧道。桌面约定每资源至多一条本机 WSS（资源卡在已有隧道时禁用「启动」）。API 仍可能回存 `maxPerUser=1` 占位以满足 schema。
   - **`port` 语义**：仅对 RDP/SSH/XRDP 生效；显式值必须落在 `1..65535`，留空时按协议默认（RDP/XRDP `3389`、SSH `22`）。VNC 端口始终由 display 推导（`5900 + display`），规格上的 `port` 不适用，保存时会被拒绝。用户视角的 `resolvedPort` 对 VNC 返回 `0`（省略）。
   - **`vncConfig` 对象**（可选，JSON）：`resolution`（如 `1920x1080`）、`startOptions`（附加 vncserver 参数字符串）、`useCgroup`（Linux 是否默认走 transient slice）；与 Settings `agent.vnc` 合并后由 dc-exec 拉起。
   - **VNC 密码文件（`agent.vnc.passwdMode`，默认 `user`）**：
     - `user`：首次在计算节点为某用户拉起 VNC 时，dc-exec 若发现 `~/.vnc/passwd` 不存在，会以该用户身份执行 TurboVNC `vncpasswd -f` 写入随机静态密码（不向 API 返回）；文件不会被删除，已存在文件也不会被覆盖。用户连桌面仍通过 **OTP**（direct）或 **Proxy**。
     - `managed`：dc-exec 为每次连接生成随机密码，写入私有路径 `~/.cache/dc/connection/<connectionId>/passwd`（该目录不可用时回退 `/tmp/.dc-<uid>/connection/<connectionId>/passwd`），通过 `-rfbauth` 传给 vncserver，并在端口探测成功（会话已启动）后立即删除；失败与 stop 路径同样会清理。该模式要求同时启用 VNC 密码与 OTP 认证（未显式指定 `-securitytypes` 时 TurboVNC 默认即 `vnc+otp`），若参数关闭 OTP 则 dc-exec 拒绝启动，避免残留可长期复用的静态密码；`serverArgs`/`startOptions` 中的 `-rfbauth`、`-passwordfile`、`-passwd` 会被清洗。
2. **授权层 (`user_connection_resources`)**：
   - 门户用户与非公开连接规格的多对多授权映射。公开规格（`public = true`）无需授权映射，全员可见可连。
3. **实例层 (`connections`)**：
   - 真实落地的具体运行实例（**仅 VNC / RDP / XRDP**；JSON：`username`、`resourceId`、`nodeId`、`host`、`port`、`display`、`pid`、`status`、`lastAccessedAt`、`controlledAt`；库列名仍为 snake_case）。**SSH 不入本表**。
   - **`host`（API 投影，非库列）**：不落库。用户列表 / 详情 / Launch·Start 响应在读时从 `nodes.dial_host` 解析（空则兜底 `nodeId`）；管理员连接列表**不**投影 `host`。Proxy 拨号同样按节点解析，不读历史行内 host。
   - **`protocol`（规格与实例）**：库内为 smallint 枚举（含 `SSH` 枚举值供**规格**使用）；HTTP JSON 仍为字符串。新建实例行不会再写入 SSH。
   - **`lastAccessedAt`**：最近一次**成功接入**时间，不是「当前在线」。Proxy 在隧道 TCP 拨号成功时写入（connection 锚点）；直连由客户端在连上后调用 `POST /api/connections/:id/touch`。Disconnect / 隧道关闭**不**更新该字段。SSH 资源隧道不写本字段。
   - **`controlledAt`（仅 VNC）**：最近一次 Start/Stop 或进程 finalize 时间；探针对账宽限期用此字段，RDP/XRDP 行通常为空。
   - **唯一约束 `idx_conn_node_user_port`**：同一用户在同一节点、同一 `port` 仅一行。RDP/XRDP 常见端口固定（如 3389），故同一规格 `maxPerUser > 1` 需摊到不同节点；勿期望「同机多规格、同端口、同用户」各占一行。
   - **无归属实例（未托管）**：探针扫描发现的未受管且无法唯一关联到已有规格的 VNC 进程，以 `resourceId` 为空录入实例表；连接名称不再硬编码 `"未托管 · "` 文案（为空时回退为 `<host>:<port>`），由前端根据 `resourceId` 决定 UI 标签展示。当用户可访问的资源中存在唯一最优匹配规格时，在探针对账或用户点击 Start 时会自动写入 `resource_id` 并将名称规整为 `<resource>-<node>`；停止中的无归属行同样会在节点对账时补齐归属；若命中多个并列规格则保持无归属，且已有归属的实例不再自动改绑。
   - **匹配优先级**（四键依次比较，全部相同即视为并列并保持无归属）：`固定 display` > `节点直指（nodes 包含该节点）` > `单节点规格（仅一个 nodeId 且无 nodeGroups）` > `公开规格（public=true）`。公开规格优先，避免无归属实例先消耗私有规格的配额。

### 运行时两段式交互与生命周期

- **上段：我的连接**
  - 展示属于当前用户的 **VNC / RDP / XRDP** 连接实例行（按 `last_accessed_at DESC NULLS LAST, id DESC` 倒序）。SSH 不进本列表。
  - 支持的操作（实例协议）：
    - `running`：展示 [ Open ] [ Stop ]；`stopped`：展示 [ Start ] [ 销毁 ]。
    - VNC `unknown` 状态仅提供 [ 销毁 ]（不提供直接拉起，需等待探针对账或人工排查）。
    - 旧客户端的 Disconnect 入口仍可用，但服务端**等价于 Stop**（会结束远程会话，不只关代理）。
- **下段：可用资源**
  - 展示当前用户有权限申请的连接规格卡片。
  - **VNC / RDP / XRDP**：`usedCount` 统计该用户在当前规格下的全部实例行数（包括 `pending`、`stopped` 与 `unknown`）。`usedCount < maxPerUser` 可【+ 启动新连接】；已满则【已达配额】（Stop 不释放配额；Delete / 90 天 prune 才释放）。
  - **SSH**：无引擎实例行；桌面用本机隧道数作为 usedCount（约定每资源至多 1）。资源卡「启动」= Upgrade `GET /api/connections/ssh?resourceId=`；**已有本机隧道时禁用**资源卡启动（本地会话卡上的「启动」仍可重建）。关 WSS 即结束；服务端不按规格 `maxPerUser` 拦截。

### 协议生命周期分支

- **VNC 桌面**：
  - **固定 Display**：规格 `len(nodes)==1` 且指定 display。事务内 `ClaimVNC` 占 `(node_id, display)` 后下发 `VNCControl` 指定号；节点 X 已占用则 `display_conflict` → **409**。
  - **动态 Display**：placer 排列候选节点。事务内 `ClaimVNCPending`（`display IS NULL`、占位 `port`、`status=pending`）占配额后，Server 下发规格层 `avoidDisplays`：
    - `avoid` = 其它 VNC 规格在该节点预留的固定 display（**不**读 `connections` 表；选号权威在 Agent 本机 `scanXDisplays`）。
    - Agent 在 dc-exec **互斥锁**内用 `scanXDisplays`（`/tmp/.X11-unix/X{N}` 等）与 `avoidDisplays` 取 `[1,99]` 最小空闲号并启动；成功回报 `{display, port, pid}`，Server `FinalizeVNCPending`；`display_exhausted` 则删 pending 试下一节点。
    - 并发同节点 Launch 由锁串行；`dce-app*` 桌面不入 `connections`（占用仍可能体现在本机 X 扫描/avoidDisplays）。
  - **Stop**：远程 kill；`status=stopped`；清空 `display` / `pid`；`port` 改为占位值；保留 `resource_id`（配额 + UI 卡片）。`node_id` 可保留为**上次落点**；API 再读时 `host` 仍按当前节点 `dialHost` 投影。**Start 不再原 node 符活**。
  - **Start**（stopped）：同一 connection id 墓碑；按 **`resourceId` 重新走资源选机**（动态多节点重选；固定 display 规格仍用其唯一节点）后再拉起。等价于「资源启动线路写回本行」，不是钉死旧 node。
  - **Delete**（VNC）：先尝试远程 kill；成功则物理删行；失败则 `terminating` 并清空 `resource_id`（用户列表不可见，HTTP 仍 **204**）。探针按 **所有者+display** 对账：同所有者则再 kill 后删行；display 已属他人或进程已消失则直接删墓碑（不原地改成新用户行）。
  - **Launch / Start 错误**：配额满、display 冲突、号用尽且无候选节点、Finalize 唯一约束冲突 → **409**；Agent 离线、超时、dc-exec 不可用 → **502**。
  - **探针**：`connection-probe` 用 `/proc` 对账，不参与选号；Agent 注册后会立即跑一次探针。上报带 `connectionId` 时优先更新对应 pending/running 行。
- **Linux XRDP（X 席位）**：
  - **拨号登记**：门户行记录落点与代理生命周期；TCP 走共享 **3389**（规格 `port` 可配置，默认 3389）。
  - **EDA 断线保活**：节点 `sesman.ini` 须保持 `KillDisconnected=false`；客户端断线**不**杀桌面（探针仍能看到活着的 X 席位 → 保持 `running`）。部署侧可在 `startwm` 正常 Logout 路径追加 `loginctl terminate-session`（节点侧配置见 `docs-site/docs/deployment/config.md`，运维待办见 `todo/README.md`）。
  - **Stop/Delete**：经 dc-exec 按 username（及 display）终止 xrdp 管理的 Xorg 会话。远程杀失败 → `terminating` 占号；成功或席位消失 → **`stopped` 墓碑**（清 display/pid，**保留 `resource_id`**，不删行）。
  - **探针 / Logout 收尸**：回写 `display`/`pid`；曾附着过活席位（有 `pid`）后席位消失，或 `session_ending_confirmed` 杀座成功 → **`stopped`**。规格预留 display、尚未出现 pid 的拨号行不因此误停。
  - **Start**（stopped）：按 `resourceId` **重新选机**写回本行后 `running`（用户再拨 3389；X 由登录产生）。
- **RDP 跳板与登录节点**：
  - **幂等创建与复用**：配额已满或 MaxPerUser≤1 时复用**同一 connection id**。若该行为 **stopped** 墓碑，复用时按资源 **重新选机** 再置 `running`；若已是 **running**，仅刷新 `lastAccessedAt`（不挪节点，避免踢掉当前拨号）。
  - **选机与差集调度**：配额未满新开时，剔除其它**非 stopped** 实例已占用节点后选机。候选耗尽则兜底复用已有行（同上：stopped 重选机，running 只刷新）。
  - **Stop**（与 Delete 共用 teardown）：先关代理，再结束该用户在落点节点上的**登录会话**（RDP：完整桌面 `logoff`）。**不**停止 TermService。成功 → `stopped` 墓碑（保留 `resource_id`）；杀失败 → `terminating`（Stop 保留 resource）**且 API 返回错误**。探针 `rdp_sessions` 可对账 running↔stopped；Launch 复用 terminating 时会先重试 teardown。
  - **Disconnect**：**废弃**，服务端等价于 Stop（避免「只断代理、节点会话仍在」导致多节点 MaxPerUser=1 配额/幽灵会话不一致）。
  - **Start**：与 Launch 复用 stopped 相同——**资源选机写回本行**，不是原 node 翻状态。
  - **Delete**：同一套 teardown；成功或无可杀 → 物理删行；杀失败 → `terminating`（清 resource；HTTP 仍成功，与 VNC 一致）。
- **SSH（资源代理，无 connection 行）**：
  - **产品路径**：桌面在 SSH **规格**上点启动 → `GET /api/connections/ssh?resourceId=`（Upgrade + Portal Session）→ Engine 鉴权 → placer 选机 → 拨 `host:port` → 内存 proxy 会话。**不** `POST /api/connections`，**不**写 `connections` 行，**无** Start/Stop/Restart 实例语义。断 WSS = 结束（无墓碑、无探针）。
  - **`POST /api/connections`（Launch）对 SSH 规格返回 422**：不是「启动失败」，而是明确拒绝建实例，提示改走上面的 resource proxy。代码里的 `Launch`/`StartConnection` 拒 SSH，是为挡住误用旧客户端，不是 SSH 的正常启动 API。
  - **Stop 对 SSH 无产品语义**：关隧道 = 关 WebSocket。若库里仍有历史 SSH `connections` 行（旧版残留），管理端 Delete/cleanup 仍可 teardown；新流量不应再产生这类行。
  - 同用户同规格再次 Upgrade 会**顶替**本机既有隧道（proxy 会话键为 resourceId+requester）。

### 状态探测与对账（按协议）

门户上的 `connections.status` 与节点真实进程/会话的对应关系**因协议而异**；不要假设所有协议都走同一条「刷新 / 探活」链路。

| 维度 | VNC | RDP | SSH | XRDP (Linux) |
|---|---|---|---|---|
| **实例行事实字段** | `display`、`pid`、`port` 由拉起与探针对账维护；`host` 为读时投影 | `pid`、`display` 恒为空；`port` / `nodeId` 为拨号登记；`host` 读时投影 | **无** `connections` 行；落点仅在 proxy 打开时计算 | 拨号登记 + 探针回写 `display`/`pid`（共享 3389）；`host` 读时投影 |
| **`status` 语义** | 与桌面进程存活强相关（Stop 杀进程；探针可对账为 `stopped`） | Stop/探针：无完整桌面 RDP 会话 → `stopped` 墓碑；Start 按资源重选机 | 无门户 status；隧道存活 = 本机内存 proxy 会话 | 拨号 + 席位；活席位消失 / Logout 收尸 → `stopped`；断线保活席位仍在 → `running` |
| **周期上报** | `connection-probe` → `vnc_seats` | `rdp_sessions`（仅完整桌面 RDP） | **不探测** | `xrdp_seats` |
| **用户 `POST /api/connections/refresh`** | 仅 VNC：`CONNECTION_CHECK` | **不参与** | **不参与** | **不参与** |
| **管理端 `POST .../connections/refresh`** | 全节点 `connection-probe` | 同左（探针含 XRDP 席位） | 无 | 同 VNC 列（同一 probe） |
| **管理端 `POST .../connections/cleanup`** | online running VNC：`CONNECTION_CHECK`；全部协议 `terminating` 重试 teardown；prune 过期 stopped | 同左（terminating 收尸） | 无关（无行） | 同左（另有 probe 收尸） |
| **数据面** | `GET .../connections/:id/proxy-connection` | 同左 | `GET .../connections/ssh?resourceId=` | 同 VNC 列 |

### RDP 模型说明

RDP 定位为「拨号登记实例」：门户 `connections` 行记录落点与代理生命周期；**不**旁路采集或持久化节点 OS 会话。节点可用性以 `nodes` 在线状态为准。

---

## 当前 Proxy 的两种前端接入方式

在这套设计里，当前 Proxy 实现只有两种前端接入方式：

| 模式       | 含义                                                                                       | 后端职责                                |
|----------|------------------------------------------------------------------------------------------|-------------------------------------|
| `direct` | GUI 根据 connection 字段计算直连目标并连接当前目标服务；VNC 场景可按需申请 TurboVNC OTP，不提供 VNC passwd 文件下载能力       | 返回 connection 信息；为运行中 VNC 签发协议层 OTP |
| `proxy`  | GUI：`GET /api/connections/:id/proxy-connection`（VNC/RDP/XRDP）或 `GET /api/connections/ssh?resourceId=`（SSH）一次 WebSocket Upgrade（Portal Session），经 Engine 字节隧道接入目标服务 | 鉴权、本机配额、拨目标 TCP 并桥接 WebSocket |

这两种模式是前端接入策略，而不是两套后端产品形态。

---

## Direct 模式

### Direct 模式语义

GUI 直接消费 connection 字段并自行连接目标服务。用户面 API 返回的 `connection.host` 是读时从节点 `dialHost`（缺失时兜底 `nodeId`）解析的投影，不是库内快照；前端直连使用该 `host` 与 `connection.port`。

### 后端职责

- 返回 connection 详情
- 为运行中的 VNC connection 按需生成 TurboVNC 协议层 OTP，并记录 access time
- 维护 connection 状态

### 前端职责

- 决定使用什么客户端或控件
- 决定是否唤起本地 Viewer
- 自行连接到 `connection.host` 和 `connection.port`
- VNC Viewer 启动前调用 `POST /api/connections/:id/vnc-otp` 获取本次 TurboVNC one-time password

### Direct 模式适用场景

- 用户设备能直达目标节点
- 不需要 Engine 中转流量
- GUI 希望保留本地客户端体验

### Direct VNC OTP

`POST /api/connections/:id/vnc-otp` 面向 direct VNC Viewer。Server 校验 connection 归属与运行态后，通过目标节点 dc-exec 以
connection owner 身份执行：

```bash
/opt/TurboVNC/bin/vncpasswd -o -display :1
```

实际 `vncpasswd` 路径来自 `[vnc]` 配置。TurboVNC 可能把 `Full control one-time password: <otp>` 输出到 stderr，因此
dc-exec 会合并 stdout/stderr 解析。接口只返回 full-control one-time password，不返回 VNC passwd 文件，也不持久化该 OTP；该
OTP 由 TurboVNC session 管理，只适用于一次成功 Viewer 连接，失败或重连应重新申请。

---

## Proxy 模式

### Proxy 模式语义

GUI 不直接连接目标服务，而是对当前 Portal API origin 发起 **一次** WebSocket Upgrade，由接到请求的 Engine 校验 Portal Session、配额与协议开关后 **TCP 直拨** connection 快照中的目标端口，与 WS 做单会话字节桥接（不经 Agent 转发载荷）。

### 相关接口

- `GET /api/connections/:id/proxy-connection`（WebSocket Upgrade 或查询本机 active 会话）
- 关闭隧道：桌面关闭 WebSocket（无 `DELETE` proxy 路由）

### 当前错误语义

- `GET /api/connections/:id/proxy-connection`（Upgrade）
    - `400 Bad Request`：连接缺少可代理的协议、目标地址或目标端口，或协议不受支持
    - `403 Forbidden`：连接协议对应的开关未启用（如 `vncEnabled=false`）
    - `429 Too Many Requests`：达到本节点全局 ProxyConnection 上限（`proxy.maxProxyConnections`），或达到单用户上限（`proxy.maxProxyConnectionsPerUser`）
    - `502 Bad Gateway`：目标 TCP 拨号失败
    - `503 Service Unavailable`：当前未启用协议代理（`proxy.enabled=false`）
- `GET /api/connections/:id/proxy-connection`（无 Upgrade）
    - `404 Not Found`：**本节点**上当前用户在该 connection 上没有 active 隧道（多节点下不得用于存活探测）
    - `503 Service Unavailable`：当前未启用协议代理（`proxy.enabled=false`）

### 典型流程

1. GUI 对 `GET /api/connections/:id/proxy-connection` 发起 WebSocket Upgrade，携带 `Authorization: Bearer <portal-session>`
2. Engine 鉴权、占配额、拨目标 TCP，回 `101` 并桥接字节流
3. 隧道是否结束以 WebSocket 物理断开（EOF / CloseFrame）为准；桌面关 WS 即回收本机会话。**禁止**轮询无 Upgrade 的 GET 探测存活；不提供 `DELETE` proxy 路由

### Proxy 模式适用场景

- 用户设备不能直达目标节点，但能访问 Portal
- 希望统一通过 Engine 暴露入口
- **集群内部网络可信**：Engine 到节点为明文 TCP，依赖内网隔离与应用层协议加密（详见 [协议代理](./proxy.md)「数据面路径与信任边界」）

当前协议代理实现的详细规则见 [协议代理](./proxy.md)。

---

## 推荐的理解方式

前端不需要把“远程访问”理解成多套后端模式，只需要这样理解：

- 先拿到 `connection`
- 如果当前策略是 `direct`，使用 `connection.host` 与 `connection.port`（`host` 为节点 `dialHost` 的读时投影，缺失时兜底 `nodeId`）
- 如果当前策略是 `proxy`，再对 `GET /api/connections/:id/proxy-connection` 发起 WebSocket Upgrade（Portal Session）
- 需要启停 VNC 会话时调用生命周期接口：`POST /api/connections/:id/start|stop|restart`
  （`202 Accepted`；未知动作 `404`）。删除运行中连接时后端会远程停止 VNC 进程并关闭其 ProxyConnection
- 如果批量删除 connection，接口采用"首个错误即整体中止"语义：
    - 任一目标无权限删除：整体 `403 Forbidden`
    - 不存在的 ID 会被静默忽略（不报 `404`）
    - 无部分失败聚合语义

因此，本页描述 connection 与 proxy 能力的职责边界，而不是 GUI 协议唤起文档。

---

## 实现索引（便于维护者对照）

- `GET /api/connections/:id` 只返回 connection 事实，不返回 VNC 明文密码
- `POST /api/connections/:id/vnc-otp` 为当前运行中的 VNC connection 生成 TurboVNC 协议层 OTP
- `POST /api/connections/:id/start|stop|restart`：仅 VNC/RDP/XRDP 实例；Stop/Delete 经 `teardownRemoteSession`（VNC/XRDP 席位 kill；RDP `SESSION_CONTROL`）。SSH 无实例 API
- 管理员侧：连接规格 CRUD、实例 CRUD、vnc-otp、start/stop/restart、refresh（连接席位采集）、cleanup
- `POST /api/connections/refresh`：仅当前用户的 **VNC** 实例，经 `CONNECTION_CHECK` 收敛状态；不触发全节点 connection-probe；短时间去重静默忽略
- `POST /api/admin/system/connections/refresh`：异步触发全节点 `connection-probe`，上报 `ConnectionReport`（VNC + XRDP 席位），加速状态收敛
- `GET /api/connections/:id/proxy-connection`（Upgrade + Portal Session）：VNC/RDP/XRDP 数据面
- `GET /api/connections/ssh?resourceId=`（Upgrade + Portal Session）：SSH 数据面（无 connection 行）

连接席位采集会从节点本地扫描 VNC（`Xvnc` / `Xtigervnc`）与 XRDP Xorg 席位，经 `ConnectionReport.vnc_seats` / `xrdp_seats` 回写 connection。
`connection.port` 表示真实 VNC TCP 端口，不是 display 号。
无 `connectionId` 的 VNC 进程按节点、用户、端口作为运行时唯一键补录；已有受管 connection 在节点采集中找不到对应运行时进程时会标记为
stopped（不是立即物理删除）；`status=stopped` 且超过 90 天的行由周期保留任务（`connection_stopped_prune`）或管理员 cleanup 物理删除。

关键实现：

- `internal/server/portal/api/connection.go`
- `internal/server/service/proxy/proxy.go`
- `docs-site/docs/development/remote-access/proxy.md`

> 概念层的两种接入方式（Direct / Proxy）见上文「当前 Proxy 的两种前端接入方式」。

### Portal 连接错误公开 message（桌面 exact match）

连接放置/冲突类失败不引入 JSON `code`；HTTP status + 下列 **稳定英文 `message`** 即为对外契约（须与 Go sentinel `Error()` 全等，不得拼节点名等资源细节）。实现：`connection.PublicHTTP`、`handleConnectionError`。

| 公开 message | HTTP | Go sentinel |
|---|---|---|
| `candidate node set is empty` | 422 | `ErrCandidateSetEmpty` |
| `no healthy online nodes` | 503 | `ErrNoHealthyNode` |
| `all candidate nodes are already occupied` | 409 | `ErrAllNodesOccupied` |
| `node connection limit reached` | 409 | `ErrNodeConnectionLimit` |
| `target node is not available` | 503 | `ErrTargetNodeUnavailable` |
| `no available node for connection` | 503 | `ErrPlacerNoNodeAvailable` |
| `connection resource in use` | 409 | `ErrResourceInUse` |
| `connection resource name already exists` | 409 | `ErrResourceNameConflict` |
| `connection already exists` | 409 | `ErrConnectionAlreadyExists` |
| `display is already occupied` | 409 | `ErrDisplayOccupied` |
| `display_exhausted` | 409 | `ErrDisplayExhausted` |
| `display_conflict` | 409 | `ErrDisplayConflict` |
| `connection is still starting` | 409 | `ErrConnectionStillStarting` |
| `connection state unknown` | 409 | `ErrConnectionStateUnknown` |

其它 Portal 错误以 HTTP status 为主；桌面客户端在调用点用 status 区分 UX，不靠子串猜测 `message`。

---
