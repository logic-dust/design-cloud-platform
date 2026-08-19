# 远程桌面接入：直连 + 纯 TCP 隧道中转架构

> 本文档描述 Design Cloud Engine 的**远程桌面接入层**方案，独立于平台 Agent。
> 适用对象：Windows（TermService RDP）与 Linux（xrdp RDP / Xvnc VNC）节点。
> 定位：**接入层（Engine/Client 侧）能力**，与节点 Agent 完全解耦。

---

## 1. 背景与目标

- **桌面客户端优先**（非浏览器），用户使用本地 mstsc / FreeRDP / VNC viewer 连接远程桌面；
- 目标节点可能处于**隔离网络 / 跨网段**，客户端无法直连，需要经 Engine 中转；
- 技术栈**全 Go**，不引入 guacd（C）、Guacamole（Java）、FreeRDP 库等外部组件。

## 2. 双轨接入模型

```
                    ┌─ 直连: mstsc / FreeRDP / VNC viewer ──> 目标节点 (3389/5900)
桌面客户端 ─────────┤
                    └─ 隧道: 本地客户端 ─> Client(TCP↔WS) ─> Engine(WS↔TCP) ─> 目标节点
```

| 路线 | 适用场景 | 客户端 | 中转 |
| :--- | :--- | :--- | :--- |
| **直连** | 客户端可路由到目标（同网段/VPN） | mstsc / FreeRDP / VNC viewer | 无 |
| **隧道** | 客户端无法直连目标（隔离/跨网段） | 本地客户端连 `127.0.0.1:<port>` | Client + Engine 双向字节搬运 |

## 3. 核心原则：纯 TCP 隧道（Zero-Protocol-Awareness）

**关键设计**：隧道两端**完全不理解 RDP/VNC 协议**，只做「字节搬运」。

```
RDP/VNC Server ←─ Engine(WS↔TCP) ←─WebSocket─→ Client(TCP↔WS) ←─ 本地客户端
```

- **Client 端**：起一个本地 TCP listener，把收到的字节原样经 WebSocket 转发给 Engine；
- **Engine 端**：连目标节点 `NodeIP:3389`（或 `5900`），把 WebSocket 字节原样转发给目标；
- **协议握手端到端直达**：本地 mstsc/FreeRDP/VNC viewer 自己完成协议握手，握手字节穿透隧道直达目标；
- **不模拟服务端**：Client 端不做 RDP/VNC 服务端握手，只做 `net.Conn` ↔ WebSocket 双向拷贝。

### 与「模拟服务端」方案的本质区别

| 方案 | Client 端职责 | 复杂度 |
| :--- | :--- | :--- |
| ❌ 模拟服务端 | 实现 RDP/VNC 服务端握手，把 WS 数据还原成协议字节流 | 极高（RDP 服务端协议复杂） |
| ✅ 纯 TCP 隧道 | 只做字节搬运，不解析协议 | 极低（Go 几十行） |

## 4. 技术实现要点（全 Go）

> **重要**：本方案**不是从零实现**，而是对现有 `internal/server/service/proxy` 的复用与扩展。
> 现有 proxy 已经实现了「纯 TCP 隧道」的核心——`bridgeProxyConnections` 就是
> `wsConn ↔ tcpConn` 的双向 `io.Copy`，`ProtocolHandler.Connect` 就是 `net.Dial("tcp", target)`，
> 完全协议无关。本节据此展开，标注「已有」与「待扩展」。

### 4.1 双向拷贝核心（已有）

现有 `internal/server/portal/api/connection_proxy.go` 的 `bridgeProxyConnections`：

```go
// 已有实现：WS <-> TCP 双向字节搬运，协议无关
func (h *connProxyHandler) bridgeProxyConnections(ctx context.Context, wsConn *websocket.Conn, tcpConn net.Conn) {
    wsNet := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
    defer wsNet.Close()
    defer tcpConn.Close()

    var wg sync.WaitGroup
    var closeOnce sync.Once
    closeConnections := func() { closeOnce.Do(func() { wsNet.Close(); tcpConn.Close() }) }

    wg.Go(func() { defer closeConnections(); io.CopyBuffer(tcpConn, wsNet, buf) })  // WS -> TCP
    wg.Go(func() { defer closeConnections(); io.CopyBuffer(wsNet, tcpConn, buf) })  // TCP -> WS
    wg.Wait()
}
```

- **Engine 端**：`wsConn` ↔ `targetConn`（`ProtocolHandler.Connect` → `net.Dial("tcp", "NodeIP:3389")`）；
- **Client 端（待扩展）**：`localConn`（`net.Listener.Accept`）↔ `wsConn`，复用同一套 `io.Copy` 逻辑；
- 任一侧关闭即触发对侧关闭（`closeOnce` 保证幂等）。

### 4.2 连接生命周期（已有，OTP 复用）

现有 proxy 的 OTP 隧道机制**可直接复用**，无需新造：

1. 用户在 Portal 点击「连接」→ `POST /api/connections/:id/proxy-session` → `proxy.Create` 签发
   **短时效 OTP**（`crypto.GenSecret(24)`，256-bit 随机，默认 `SessionTimeout=1800s`）；
2. Client 端用 OTP 建立 WebSocket 到 Engine（`GET /api/connection-proxy/ws`，OTP 经
   `Sec-WebSocket-Protocol: dc-proxy-token.<otp>` 或 Bearer 传递）；
3. Engine 校验 OTP（`GetByOTP` → `Activate`，一次性、防重放）→ 解析目标 `Host:Port` → `net.Dial`；
4. `bridgeProxyConnections` 双向搬运，直到任一侧断开 → `CloseByConnectionID` 清理。

**关键复用点**：
- `Tunnel` 结构已快照 `Protocol/Host/Port/Username`，OTP 阶段无需 DB 往返；
- `tunnelStore` 已实现 OTP 一次性、过期清理（`PruneExpiredPending`）、按连接替换；
- `ProtocolRegistry` 已注册 VNC/RDP/SSH 三种协议，`BaseProtocolHandler.Connect` 就是纯 TCP dial。

### 4.3 半关闭（Half-Close）语义（待扩展）

- RDP/VNC 协议存在「客户端先关写、服务端继续写」的半关闭场景；
- 现有 `bridgeProxyConnections` 是**全双工断开**（任一侧 `io.Copy` 返回即 `closeOnce` 关闭两侧），
  无法表达半关闭；
- **扩展方案**：在 WebSocket 消息层加**控制帧**区分「关写」与「全关」：
  - 用 `websocket.MessageBinary` 承载数据帧，用 `websocket.MessageText`（或自定义 opcode）承载控制帧；
  - 控制帧语义：`close-write`（客户端不再发送，但继续接收）、`close`（全关）；
  - Engine 端收到 `close-write` 后调 `tcpConn.(*net.TCPConn).CloseWrite()`，保留读方向；
- **一期可先做全双工断开**（现有行为），半关闭作为 P3 优化（RDP 场景更依赖半关闭）。

### 4.4 安全边界（已有 + 待扩展）

- ✅ 隧道令牌（OTP）短时效、一次性、绑定目标节点（已有 `tunnelStore` 保证）；
- ✅ 字节流**不落日志**（现有 proxy 只记 `connection/protocol/target`，不记 payload）；
- ✅ WebSocket 走 HTTPS + OTP 鉴权（已有 `allowWailsOrigin` 来源校验）；
- ⚠️ 密码/凭据注入位置：直连在客户端本地，隧道在 Engine 侧（见 §6），需演进 Kerberos 票据；
- ⚠️ Client 端本地 TCP listener 需绑定 `127.0.0.1`（仅本机可达），避免暴露到局域网。

## 5. 与节点 Agent 的关系（解耦）

- **Agent 不参与隧道**：Agent 只负责「资源采集 + 探活 + 注册」，不转发任何远程桌面数据；
- **Engine 侧隧道**是接入层组件，与 Agent 的 gRPC 心跳/遥测链路**物理分离**；
- 因此 Windows Agent**无需考虑隧道实现**，仅需在注册时写入 `protocol: "RDP"` 的静态连接记录。

## 5.1 Client 端待扩展点（桌面客户端侧）

现有 proxy 只覆盖了「Engine 侧 WS↔TCP」，**Client 端（桌面客户端）的本地 TCP listener 是新增部分**：

```
本地 mstsc/FreeRDP/VNC viewer ──> 127.0.0.1:<port> ──> Client(TCP↔WS) ──> Engine(WS↔TCP) ──> 目标
```

Client 端职责（全 Go，无协议逻辑）：

1. **本地 TCP listener**：`net.Listen("tcp", "127.0.0.1:0")`（随机端口，仅本机可达）；
2. **建立 WS**：用 OTP 连 Engine 的 `/api/connection-proxy/ws`；
3. **双向搬运**：`localConn ↔ wsConn`，复用与 Engine 侧相同的 `io.Copy` + `closeOnce` 模式；
4. **生命周期**：本地客户端断开 → 关闭 WS → Engine 侧 `CloseByConnectionID` 清理隧道。

**与 Engine 侧 `bridgeProxyConnections` 的对称性**：两端是同一套「`net.Conn` ↔ WebSocket 双向拷贝」
逻辑的镜像，可抽成共享包（如 `internal/common/tunnel`）供 Client 与 Engine 复用。

## 5.2 凭据注入位置（直连 vs 隧道）

| 路线 | 凭据注入位置 | 密码流向 | 安全要求 |
| :--- | :--- | :--- | :--- |
| **直连（mstsc）** | 客户端本地 | 密码不出客户端机器 | `cmdkey` 预存 + 连接后清理 |
| **直连（FreeRDP）** | 客户端本地 | 密码不出客户端机器 | 命令行 `/p:` 或 stdin 管道 |
| **隧道** | Engine 侧 | 密码需到 Engine | 建议演进为 Kerberos 票据注入，避免明文密码 |

## 7. 会话模型与续存探测（背景知识）

远程桌面会话存在「断开 ≠ 结束」的**持久会话**特性，这是「续存探测」存在的根本原因：

| 协议 | 会话模型 | 续存探测方式 |
| :--- | :--- | :--- |
| **VNC（Xvnc/Xtigervnc）** | 进程即会话，需预先拉起 | 扫 `/proc` 找 Xvnc 进程 |
| **RDP（Windows TermService）** | 持久会话，断开后仍存活 | `WTSEnumerateSessions` / `quser` |
| **RDP（Linux xrdp）** | 按需拉起，断开后 xorgxrdp 存活 | 扫 `xorgxrdp` 进程 / `xrdp-sesman` |

- Linux xrdp **不需要**像 VNC 那样「预先拉起会话」，用户连接时自动 fork `xorgxrdp`；
- 因此 Linux xrdp 的**服务级探活**只需查 `xrdp` 服务（3389/systemd），
  而「扫 `xorgxrdp` 进程」属于**会话级续存探测**（现有 `internal/agent/probe/vnc.go` 已实现）。

## 8. 实施路线

| 阶段 | 内容 | 交付物 | 状态 |
| :--- | :--- | :--- | :--- |
| **P0** | Engine 侧 WS↔TCP 隧道 + OTP | `internal/server/service/proxy` | ✅ 已有 |
| **P1** | Client 端本地 TCP listener + WS 隧道骨架（Go） | 本地客户端可经隧道连目标 VNC | ⬜ 待做 |
| **P2** | 抽取共享 `internal/common/tunnel` 双向拷贝包 | Client/Engine 复用同一套搬运逻辑 | ⬜ 待做 |
| **P3** | RDP 协议支持（3389）+ 半关闭语义 | 本地 mstsc/FreeRDP 经隧道连 Windows/xrdp | ⬜ 待做 |
| **P4** | 凭据注入演进（Kerberos 票据） | 隧道侧免明文密码 | ⬜ 待做 |

## 9. 决策记录

- ✅ 桌面客户端优先 → 不引入 Guacamole/guacd（浏览器方案）；
- ✅ 纯 TCP 隧道（字节搬运）→ 不模拟 RDP/VNC 服务端；
- ✅ 全 Go 实现 → 无 C/Java 外部组件；
- ✅ 与 Agent 解耦 → Windows Agent 方案不包含隧道实现；
- ✅ **复用现有 `internal/server/service/proxy`** → Engine 侧隧道与 OTP 已就绪，仅需补 Client 端。
