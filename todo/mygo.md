# Desktop 迁移到 MyGo：目标架构与落地方案

> 状态：目标设计（2026-11-12）。本文只描述目标形态，不在此执行代码落地。Monorepo 仓名迁移属规划范围，不在此执行。依据 Engine `origin/main`（`d23e8d2`）的现有契约编写。

## 1. 目标与范围

用 [MyGo](https://github.com/egoist/mygo) 在同语言仓内实现桌面客户端，与 Engine 共享中立契约，作为桌面客户端的目标实现。

原则：

- 远程会话画质与帧率由外部 `TurboVNC` / `mstsc` 决定，与外壳语言无关。
- 数据通道是本地 TCP ↔ WSS ↔ 目标主机的流式转发；控制面用标准库 `net/http` + SSE，数据面复用 [`coder/websocket`](https://github.com/coder/websocket)。
- 不长期维护两套业务 API，不考虑数据库迁移。切流期允许短时并行托管 Velopack 与 MyGo 产物目录，对外契约以公开 `GET /api/version` 与 MyGo 更新源为准。
- 体积、行数等收益数字仅作待测预期，不作为已核实事实。
- RHEL 7 GNOME 不是桌面客户端必须覆盖的目标系统，仅作基础兼容性冒烟，接受无系统托盘（关窗即退出）与默认 CPU/GL 渲染表现，不做额外兼容投入。
- 现网桌面客户端位于兄弟仓 `design-cloud-desktop`（Avalonia + Velopack），不在本仓实现。

## 2. 官方能力与验收门槛

前期开发跟随官方最新版，不锁定次要版本；实施与验收前复核当时最新文档与模块要求。

- 模块：[github.com/egoist/mygo](https://pkg.go.dev/github.com/egoist/mygo)（使用最新版）
- 源码与许可证：[egoist/mygo](https://github.com/egoist/mygo)，[MIT](https://github.com/egoist/mygo/blob/main/LICENSE)
- 文档：[MyGo Docs](https://mygo.egoist.dev/docs)
  - 自更新：[Auto-updates](https://mygo.egoist.dev/docs/updates)
  - Secrets：[Desktop APIs](https://mygo.egoist.dev/docs/native)
  - 托盘：[Menus and the tray](https://mygo.egoist.dev/docs/menus)
  - `Window.Update`：[Views](https://mygo.egoist.dev/docs/ui/views)
  - `ui.Table`：[Table](https://mygo.egoist.dev/docs/ui/table)
  - CLI / `CGO_ENABLED=0`：[CLI](https://mygo.egoist.dev/docs/cli)
  - 分发与 Linux 依赖：[Building and distribution](https://mygo.egoist.dev/docs/distribution)

构建默认 `CGO_ENABLED=0`，可交叉编译；运行时仍依赖目标机 GTK3、libsecret、AppIndicator 等桌面库，不是无动态依赖的纯静态包。桌面工具链下限与当时最新 MyGo 对齐；`go.mod` / `AGENTS.md` 的 Go 下限在引入 MyGo 时一并抬到满足该版本的要求。当前仓内 Go 语言版本为 1.27，补丁下限以 `AGENTS.md` 为准。Go 标准库自带 `crypto/tls` 与 `crypto/ed25519`，运行时与更新验签均不依赖目标机 OpenSSL 版本。

第一笔 `cmd/desktop` / `internal/desktop` 合入即触发现有全仓 Windows 交叉编译门禁（`verify-changed.sh` → `verify-cross-build.sh`：`CGO_ENABLED=0 GOOS=windows go build ./...`）。原型不得绕开门禁；Linux-only 或桌面专属代码须用显式 build tag 声明，或保证可在该门禁下编译。

官方 Linux [托盘文档](https://github.com/egoist/mygo/blob/main/docs/menus.md)写明需要 `libayatana-appindicator3`；缺库时 `NewTray` 报错，直接降级为普通退出（关窗即退出），不为缺少托盘库的发行版做额外兼容。托盘可用时，关窗进托盘使用官方模式：`OnClose` 中 `PreventDefault` + `Hide()`，退出走 `RoleQuit`。跨协程更新 UI 必须经 `Window.Update` / `Invalidate` / `RunOnMain`。

## 3. 仓库与模块边界

目标规划是 Monorepo `design-cloud-platform`，在同一仓内放置：

```text
cmd/engine
cmd/exec                 # 入口；产物仍为 dc-exec
cmd/desktop              # 桌面客户端入口
internal/contract        # 共享枚举、协议、Portal 视图 DTO
internal/desktop         # 桌面私有实现
internal/server          # 服务端私有
internal/dcexec          # PrivilegedExec 实现包
```

当前 `go.mod` 模块路径仍为 `design-cloud-engine`；仓名 / 模块路径迁移仅属规划，本文不执行。配置名保持 `execBinPath`，二进制保持 `dc-exec`，默认路径 `/opt/dce/dc-exec`。

Desktop 安装与分发形态按平台与编译渠道区分：

- **自更新渠道（`installChannel=updater`）**：安装在用户可写目录（Windows 如 `%LOCALAPPDATA%\Programs`；Linux 亦可构建该渠道，供开发与自更新安装验证），启用内置 [`mygo.Updater`](https://github.com/egoist/mygo/blob/main/docs/updates.md)。
- **包管理器渠道（`installChannel=rpm`）**：Linux 生产由运维统一打包 RPM，安装至系统目录。构建时由 `Makefile` 通过 `-ldflags "-X ..."` 注入 `InstallChannel=rpm`，运行期据此走 RPM 升级指引，不发起自替换。

架构允许合理调整。Portal 响应对需要桌面复用时，抽到 `internal/contract`（或同等中立契约包），桌面不依赖带 GORM 的 `internal/model` 或 `internal/server/*`。`internal/contract` 现有内容以枚举、错误与设置模式为主，Portal 连接 / 用户等视图类型按第 9 节在用到时再下沉，不一次搬空。

依赖隔离复用现有 `.agents/rules/arch-rules.yaml` 与 `go list` 门禁（`.agents/verifiers/verify-arch.py`，fail-closed）。规则在第一批 `cmd/desktop` / `internal/desktop` 包落地的同一变更中登记，双向隔离：

- `cmd/desktop` / `internal/desktop` 不得 import `internal/server`、`internal/db`、`internal/execution`、`internal/model`。
- `internal/server`、`internal/contract` 及其他服务端包不得 import `internal/desktop`。

## 4. 版本治理（客户端比较与渠道感知）

单一配置源：`schema.DesktopSettings.minSupportedVersion`。管理员经现有 `PUT /api/admin/system/settings/desktop` 写入。空字符串表示不设下限；非空必须是严格的 `MAJOR.MINOR.PATCH`（无 `v` 前缀、无前导零、无预发布或 build metadata）。`minSupportedVersion` 仅在不兼容的 API 或协议变动时提高；普通新增功能只提供可选更新，不抬下限。

唯一对外投影：公开的 `GET /api/version`。标准信封在原有 `version` / `commit` / `releaseDate` / `desktopEnabled` 之外，始终输出顶层 `data.minDesktopVersion`。该值来自 desktop 设置域的 `minSupportedVersion`，不公开整个设置域。该字段不依赖发布文件，也不因桌面发布关闭、无安装包、无 session 或 session 过期而省略。设置读取或已存值校验失败返回 5xx，不得伪装成空下限。Session（登录响应与 `GET /api/sessions/current`）不增加该字段。登录体 `client.appVersion` 仍只是客户端上报，不参与服务端拦截。

`minDesktopVersion` 与 `desktopEnabled` 相互独立：发布关闭时下限字段仍返回。客户端若看到 `desktopEnabled=false`，停止可选更新检查与下载提示，不要把 latest/download 的 404 当成版本检查失败；必须更新判定仍只看 `minDesktopVersion`（若本地已低于下限，仍展示阻断说明与退出 / 切换服务器路径）。

没有 426，没有 `X-Client-Version`，业务 API 不做服务端版本闸门。这是产品策略：由客户端比较并引导升级，不是服务端安全边界；过旧客户端仍可能调用 API。

客户端规则：

- 比较 `local >= min`（闭区间）。
- 字段缺失与空字符串对客户端同义：不设下限。
- 字段非空但非法：检查失败，不能当作通过。
- 区分最低支持版本与最新版本：`GET /api/version` 的 `data.minDesktopVersion` 决定是否必须更新；最新包版本只用于可选更新（Phase 3 起走 MyGo manifest；在此之前可用测试更新源，不要把现有 Velopack `GET /api/desktop/releases/latest` 的 JSON 语义当作 MyGo Updater 的输入）。
- 检查时机：启动（含本地仍有有效 session）、登录成功、切换服务器；以及 SSE 连接或重连成功、收到 desktop 设置变更通知后。这些时机都重拉同一个公开接口 `GET /api/version`。可选更新仍可按既有节奏检查最新版本。
- 通知形态：`event: system`，`action: refresh`，`extra: {"domain":"desktop"}`。只提示重拉 `GET /api/version`，SSE 不上传客户端版本，载荷不含最低支持版本值。
- `GET /api/version` 与更新源通道保持免 Session，保证无 session 或 session 过期时仍能读取最低支持版本并完成更新闭环。
- **安装渠道感知（编译期优先 + 运行期兜底）**：
  - 编译时通过 `-ldflags` 注入分发渠道标识（`installChannel=updater` 或 `installChannel=rpm`）。
  - **优先级**：非空的编译期 `InstallChannel` 优先；仅当编译期渠道为空时，才用 [`mygo.Updater.Enabled()`](https://github.com/egoist/mygo/blob/main/docs/updates.md) 与目录可写性做兜底推断。禁止因目录碰巧可写就把 `rpm` 构建改走自替换，也禁止因 `Enabled()==false` 就把 `updater` 构建静默降级为「无更新能力」而不提示。
  - **自更新渠道（`installChannel=updater`）**：使用官方 `Updater` 的产物签名（Ed25519）并继续走 TLS 下载替换，先尝试 delta，失败自动回退全量归档，完成后经用户确认调用 `App.Relaunch`。若目录不可写，提示无法完成自更新，保留重试 / 退出 / 切换服务器，不自动改走 RPM 文案。
  - **包管理器渠道（`installChannel=rpm`）**：不发起后台 `Updater.Install()` 自替换。当检测到版本低于 `minDesktopVersion`（或存在可选更新）时，界面提示当前版本由系统包管理器维护，引导用户通过 RPM / 运维通道更新。
- 可选更新可以关闭并继续业务。必须更新不可跳过进入业务，但必须允许重试、切换服务器或退出客户端；不要做成无法退出的唯一 modal。检查未通过或未完成时，不进入或停留在可操作工作区；**不强杀已经打开的远程会话**。需要安装或重启才能完成更新时，先说明影响并得到用户确认，再结束子进程并重启。
- 检查失败（网络、5xx、非法 min）：可重试，使用退避，不空转循环。已在线会话遇到瞬时网络错误时，不得顺手强杀会话或清掉登录状态。
- 已发布安装包版本低于当前 min，是运维错误：提示无法获得满足要求的包，停止自动循环。应先发布足够新的包（`updater` 渠道的更新包与 `rpm` 渠道的 RPM）再抬下限。

更新提示的具体 UI 由桌面客户端实现，见第 8 节。现有桌面客户端与 MyGo 客户端在版本下限、SSE `domain=desktop`、免 Session 更新通道、无 426 / 无业务 API 闸门上遵循同一契约；更新包格式不共用——Velopack `releases.*.json` 不得当作 MyGo Updater 输入。

## 5. 凭据

密码和 session token 只在系统凭据库中持久化；系统凭据库不可用或读写失败时，仅保留在内存中。不得再写设备绑定的加密文件作为回退。

- 登录页在探测到可用的系统凭据库时，保留「记住 / 保持登录」复选框。
- 探测不到（如 [`mygo.Secrets`](https://mygo.egoist.dev/docs/native) 返回 `errors.ErrUnsupported`）则禁用该复选框，默认不勾选，并用小字说明原因（例如未发现钥匙环或系统凭据库）。
- Windows 使用系统凭据管理器；macOS 实际探测 Keychain（探测命令不得把秘密放进 argv）；Linux 使用 MyGo Secrets / Secret Service。运行期写入或读取失败时同样只留在内存，并让用户感知「无法保持登录」。
- 钥匙环解锁等可能阻塞的探测放在后台，经 `Window.Update` 回到主线程，不卡住首屏。
- 同一政策适用于为远程连接缓存的系统密码。

## 6. 隧道

隧道按 Engine 现有数据面协议实现，而不是两端直接 `io.Copy` 原始字节：

- **有 connection 实例的协议（VNC / RDP / XRDP）**：使用 [`coder/websocket`](https://github.com/coder/websocket) 连接 `GET /api/connections/:id/proxy-connection`。
- **SSH**：不创建 connection 实例，使用 `GET /api/connections/ssh?resourceId=` 做 WebSocket Upgrade；帧与半关闭约定相同。该路由只接受 Upgrade，无查询接口。
- 数据帧为二进制消息。
- 文本帧 `{"op":"close-write"}` 表示 TCP 半关闭（`CloseWrite`），不是拆掉整条连接。
- 读上限 4 MiB。
- 升级时使用 `Authorization: Bearer`（不要改成 Cookie 或 query，以免扩大跨站握手风险）。
- **取消隧道 = 关闭 WebSocket**（EOF / CloseFrame / 取消 dial context）。客户端持有这条套接字，自行断开即可；数据面在桥接结束时关闭后端 TCP，并用 `CloseByID` 注销本机会话。服务端不提供 `DELETE .../proxy-connection`。有 connection 实例的协议还可被服务端 `CloseAllByConnectionID` 关掉（停止 / 删除连接实例）；SSH 无 connection 行，不受该路径影响。用户登出或封禁走 `CloseAllByRequester`，可关掉该用户本机全部隧道（含 SSH）。客户端看到的仍是 WebSocket 断开。
- 无 Upgrade 的 `GET .../proxy-connection` 仅查询本机 active 会话，**禁止**轮询它判断存活；他机 404 不代表隧道不存在。
- **Origin**：目标行为是，在已通过 Bearer 鉴权的前提下，允许原生客户端不带 `Origin` 完成 Upgrade（即使配置了 `portal.websocket_host` / `portalHost`）。这与数据面关闭 coder 同源校验、以 Bearer 为边界的现有注释一致，需要改握手校验并补单测，作为 Phase 0 的 Engine 小改动同期完成。在该改动落地前联调，客户端可以发送已允许的 `http://127.0.0.1` / `http://localhost`，勿伪造任意浏览器 Origin。
- 单本地端口可多次 `Accept` / 查看器重连。
- 临时 VNC 口令文件使用最小权限（`0600`）。查看器进程启动后可延迟数秒删除，并在连接失败、隧道取消、桌面退出及下次启动时执行兜底清理，确保进程生命周期内关闭监听且无残留文件。

## 7. 更新、打包与 CA 证书加载

- **自更新与产物托管**：桌面自更新使用 [MyGo 官方 `Updater`](https://mygo.egoist.dev/docs/updates)（`Check` / `Install`：先 delta，失败回全量 / `App.Relaunch` / `SetSource` / Ed25519 公钥）。Engine 托管 `mygo build` 产物（平台 manifest、全量归档、delta）。构建侧用 `MYGO_UPDATER_PRIVATE_KEY` 签名，密钥不进仓库。切流期可短时并行保留 Velopack 目录供现网客户端；MyGo manifest 不得直接覆盖现有 `releases.*.json` 语义，版本查询继续走公开 `GET /api/version`。Phase 0 使用独立测试更新源验证闭环，不必先改造生产托管目录。
- **Linux RPM 与构建注入**：不设独立的 Linux 桌面安装器二进制。Linux 生产由运维统一打包 RPM，在 `Makefile` 编译阶段通过 `-ldflags "-X .../internal/desktop/buildinfo.InstallChannel=rpm -X .../internal/desktop/buildinfo.Version=$(VERSION)"` 注入渠道与版本。Linux 仍须能构建 `installChannel=updater` 的用户可写安装，供 Phase 0 验证官方 Updater；只靠 RPM 渠道无法在 Linux 上证明自更新闭环。
- **Windows 首次安装**：在 MyGo 安装包能等价完成首次安装（含企业 CA 与用户可写落点）之前，继续使用现有 `cmd/dc-desktop-installer`（CA 注入 + 写 config + 下载 Setup）。该体验在 Phase 3 就绪后，由 MyGo 安装包承担。
- **企业 CA 证书加载（免 root、随包或用户目录）**：
  - 安装系统级 CA 为可选项，不强制要求系统证书库注入。
  - 支持将企业 CA 随包发布（通过 Go `//go:embed` 或 [MyGo `PathResources`](https://github.com/egoist/mygo/blob/main/docs/distribution.md) 分发）或存放在用户配置目录。
  - Desktop 启动时基于 `x509.SystemCertPool()` **追加**随包/用户目录 CA，构造显式 `tls.Config` / HTTP Transport，配置给业务 HTTP、SSE、WSS 客户端。
  - **不得**把「进程内设置 `SSL_CERT_FILE`」当作与追加 CA 等价的默认方案：该变量若生效通常是替换系统根而非追加；设置过晚则对已初始化的池无效；且业务 Transport 与 `mygo.Updater` 都不会自动继承。Phase 0 必须证明 Updater 下载实际使用了同一追加后的证书池（官方支持的自定义 Transport / 源配置，或经验证的等价注入），不能只验证业务 HTTP 客户端。

## 8. 界面

主线程只做渲染与状态读取，禁止在 View 中做阻塞 I/O。HTTP、SSE、隧道和外部进程监控在后台 goroutine 中运行，经 [`Window.Update` / `Invalidate` / `RunOnMain`](https://github.com/egoist/mygo/blob/main/docs/ui/views.md) 回到主线程。高频 SSE 数据先合并再刷新。

视觉保持极简：静态 SVG、扁平界面，不做粒子或重装饰动画。列表使用官方虚拟化 [`ui.Table`](https://github.com/egoist/mygo/blob/main/docs/ui/table.md)。输入法、长列表与更新闭环纳入早期原型验收。

托盘失败则降级为普通退出，不强制依赖托盘保活。系统通知走 [MyGo Desktop Notifications](https://github.com/egoist/mygo/blob/main/docs/native.md)。

可选更新与必须更新的界面不同，由桌面客户端结合安装渠道（`installChannel`）完成：

- **可选更新**（本地已满足 `minSupportedVersion`，但有更新版本，且 `desktopEnabled=true`）：可以关闭提示并继续业务。若为 `updater` 渠道则提供一键下载更新；若为 `rpm` 渠道则在关于/设置页或轻提示中告知新版本及 RPM 更新方式。`desktopEnabled=false` 时不展示可选更新下载入口。
- **必须更新**（本地低于 `minSupportedVersion`）：不可跳过进入业务，但不能只做一个无法退出的 modal，应保留重试、切换服务器和退出客户端的路径。`updater` 渠道展示下载安装按钮；`rpm` 渠道展示版本过低说明与包管理器升级指引。
- 已打开的远程连接不突然终止。需要安装或重启时，先说明影响并得到用户确认。

## 9. 实施阶段与详细开发清单

下表周期均为待 Phase 0 结果校准的初步估算，不是承诺。

| 阶段 | 周期（初步估算） | 工作 | 验收 |
|---|---|---|---|
| Phase 0：前置原型 | 约 1 周 | 更新闭环（独立测试源、签名 + 随包 CA TLS、无 session 可下包，含 `installChannel=rpm` 与 Linux `updater` 构建验证）；`ui.Table` / 输入法 / 长列表；凭据探测与内存路径；隧道按第 6 节协议打通（VNC 数据面与 SSH 资源代理、半关闭、关 WS 取消、临时口令延迟清理）；Engine 落地空 Origin + Bearer 的握手规则并补测。 | 证明自更新/RPM 渠道检测、Updater 与业务共用追加 CA、登录凭据、表格输入与隧道可走通；配置了 `portalHost` 时空 Origin 可 Upgrade；Windows 交叉编译门禁通过。 |
| Phase 1：骨架 | 约 2 周 | 完善 `cmd/desktop` 与 `internal/desktop`；接入 `internal/contract` DTO 与 HTTP/SSE；配置 `Makefile` `-ldflags` 渠道注入；补齐 arch-rules（若 Phase 0 已登记则复核）。仓名迁移仍仅属规划。 | CI 编译通过；版本检查、`desktopEnabled` 行为、渠道识别与设置通知可联调。 |
| Phase 2：核心业务 | 约 3 周 | Overview、Connections（含外部查看器拉起与生命周期管理，含 SSH）、Tickets。 | 核心连接链路可灰度。 |
| Phase 3：完整功能 | 约 3 周 | Files、Scheduler、管理台与动态配置；Engine 增加 MyGo manifest 托管；MyGo 首次安装体验（含企业 CA）就绪，Windows 首次安装改由该安装包承担。 | 达到切流功能面；Velopack 与 MyGo 产物短时并行策略明确。 |
| Phase 4：上线 | 约 1 周 | Windows 安装包与 Linux RPM 打包流水线收敛、稳定性验证与发布切换。 | 成为桌面唯一发布路径。 |

破坏性 API 变更时抬高 `minSupportedVersion` 并同步发布满足下限的更新包与 RPM 包；由客户端按自身渠道强制引导升级。

### 9.1 仓库现状与模块映射

1. **服务端已就绪能力（直接复用）**：
   - 版本下限配置与接口投影：`internal/contract/schema/desktop_version.go` 与 `internal/server/portal/api/version.go`（`GET /api/version` 已输出 `data.minDesktopVersion`；与 `desktopEnabled` 独立）。
   - 数据面 WSS 桥接：`internal/server/service/proxy/bridge.go` 与 `internal/server/portal/api/connection_proxy.go`（已支持 4 MiB 读上限、二进制流、`{"op":"close-write"}` 半关闭、SSH `GET /api/connections/ssh?resourceId=`）。取消=关 WebSocket；会话在桥接结束后由 `CloseByID` 注销。Origin：当前空 Origin 在配置了 `portalHost` 时会被拒绝；Phase 0 按第 6 节改为 Bearer 通过后允许空 Origin。
   - 公开更新通道：`GET /api/desktop/releases/*` 已在公开路由注册；现为 Velopack 语义，MyGo manifest 托管为后续改造。Phase 0 用独立测试源。
2. **`internal/desktop` 目标子包划分（映射兄弟仓 Avalonia 客户端 `design-cloud-desktop`）**：
   - `internal/desktop/buildinfo`：编译期注入 `Version`、`Commit`、`Date`、`InstallChannel`（`updater` / `rpm`）。
   - `internal/desktop/cert`：加载随包或用户目录 CA，追加到系统池并构造显式 `tls.Config` / Transport；负责证明 Updater 使用同一证书配置。
   - `internal/desktop/client`：标准库 `net/http` API 客户端与 `/api/events` SSE 订阅合并器。
   - `internal/desktop/credential`：封装 `mygo.Secrets`，启动探测可用性，不可用时自动降级内存存储。
   - `internal/desktop/updater`：对比 `localVersion` 与 `minDesktopVersion`，尊重 `desktopEnabled`，结合 `InstallChannel` 优先级分流官方 `mygo.Updater` 自更新或 RPM 升级指引。
   - `internal/desktop/tunnel`：本地 `127.0.0.1:0` 多 `Accept` 监听器与 `coder/websocket` 双向桥接；覆盖 `proxy-connection` 与 SSH `resourceId` 路由。
   - `internal/desktop/launcher`：拉起外部 `TurboVNC` / `mstsc` / SSH 进程、延迟清理 `0600` 临时口令文件与退出兜底回收。
   - `internal/desktop/state` + `internal/desktop/ui/*`：响应式状态中心与 MyGo Native UI 各业务页面（`shell`、`login`、`overview`、`connections`、`tickets`、`files`、`scheduler`、`licenses`、`apps`、`admin`、`preferences`）。

### 9.2 分阶段可执行任务拆解

1. **Phase 0（前置原型与核心链路验证）**：
   - **P0-1**：将 Go 下限抬到满足最新 MyGo 的要求（同步 `go.mod` / `AGENTS.md`），引入最新 `github.com/egoist/mygo`，创建 `cmd/desktop/main.go` 与 `cmd/desktop/mygo.json`；同一变更登记 arch-rules 双向隔离；验证子目录编译、打包与全仓 Windows 交叉编译门禁。
   - **P0-2**：实现 `internal/desktop/buildinfo` 与 `internal/desktop/cert`，验证 `-ldflags` 注入 `InstallChannel=rpm/updater`、渠道优先级，以及随包 CA 在业务请求与 `mygo.Updater` 下的 TLS 握手（显式证书池，不依赖 `SSL_CERT_FILE` 捷径）。Linux 同时验证 `updater` 用户可写安装与 `rpm` 渠道指引。
   - **P0-3**：实现 `internal/desktop/credential`，验证 `mygo.Secrets` 探测与 `errors.ErrUnsupported` 内存回退。
   - **P0-4**：实现 `internal/desktop/tunnel` 与 `internal/desktop/launcher` 原型；打通 `proxy-connection` 与 SSH `resourceId` 隧道；Engine 落地第 6 节 Origin 规则并补测试；取消路径验证为关 WS，确认会话与后端 TCP 被回收。
   - **P0-5**：验证 `ui.Table` 虚拟列表、中文输入法与 `mygo.NewTray` 托盘创建/关窗降级。
2. **Phase 1（骨架、契约下沉与控制面联调）**：
   - **P1-1**：复核 / 补齐 `internal/desktop` 与 `cmd/desktop` 的依赖隔离红线。
   - **P1-2**：将桌面马上用到的 Portal API 请求/响应 DTO 下沉至 `internal/contract`（如 `internal/contract/dto`），避免一次搬空全部 Portal 类型。
   - **P1-3**：完成 `internal/desktop/client`（HTTP + SSE 断线重连、帧级合并刷新、`event: system` 版本重拉触发、`desktopEnabled` 处理）。
   - **P1-4**：完成 `internal/desktop/ui/shell` 与 `login`（登录页、服务器切换、必须更新阻断页、可选更新提示、左侧导航骨架）。
3. **Phase 2（核心业务迁移：Overview、Connections、Tickets）**：
   - **P2-1**：完成 `internal/desktop/ui/overview` 概览仪表盘。
   - **P2-2**：完成 `internal/desktop/ui/connections` 远程连接列表（`ui.Table`）、新建/连接/断开、本地直连配置管理、SSH 与外部查看器路径发现。
   - **P2-3**：完成 `internal/desktop/ui/tickets` 工单列表、详情、创建与审批流。
4. **Phase 3（完整业务功能与 Engine 发布目录）**：
   - **P3-1**：完成 `internal/desktop/ui/files` 受控文件浏览、流式上传下载与外发申请。
   - **P3-2**：完成 `internal/desktop/ui/scheduler`、`apps`、`licenses`（作业调度、远端应用、License 实时水位）。
   - **P3-3**：完成 `internal/desktop/ui/admin`（三员管理台与系统动态配置）与 `preferences`（偏好设置与关于页）。
   - **P3-4**：改造 `internal/server/service/desktop` 托管 `mygo build` 产物（manifest、全量包、delta）；明确与 Velopack 目录的短时并行策略；MyGo 首次安装（含企业 CA）体验就绪后，Windows 首次安装改由该安装包承担。
5. **Phase 4（打包流水线收敛与上线切换）**：
   - **P4-1**：在 `Makefile` 中收敛 Windows 签名安装包/更新包与 Linux RPM（注入 `InstallChannel=rpm`）构建目标。
   - **P4-2**：完成弱网 SSE 重连、版本下限防呆、万行 `ui.Table` 与多路并发隧道长稳回归测试，正式切换桌面端发布路径。
