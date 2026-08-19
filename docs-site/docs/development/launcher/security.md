---
title: 安全
linkTitle: 安全
weight: 130
description: Launcher 的特权边界、token 与校验机制
---

## 概述

dc-launcher 是 Design Cloud Engine 中唯一具备特权能力的组件，负责执行需要提升权限的系统操作（如切换用户身份、访问受保护文件等）。它由
Agent 通过授权路径调用，或由 Engine 文件流式服务直接 fork（见 §1.2 “第二条受信调用路径”）；
Portal、Control 或最终用户不直接调用它。当前推荐模型是：**Engine 以普通用户运行，Launcher
通过 `cap_setuid` / `cap_setgid` 获得最小提权能力，并在需要时显式切换到 root 或目标普通用户执行动作**
。本文档描述其安全架构设计，以及当前实现能提供的隔离边界。

> [!IMPORTANT]
> `dc-launcher` 当前已明确限定为 **Linux-only**。它依赖 Linux 的：
> - `/proc/<pid>` 调用链校验
> - `syscall.Stat_t` / 文件所有者校验
> - `cap_setuid` / `cap_setgid`
> - seccomp-BPF 系统调用隔离

## 设计目标

1. **最小权限原则**: Launcher 仅具备必要的 `cap_setuid` 和 `cap_setgid` 能力，不让整个 Engine 常驻 Root
2. **深度防御**: 多层独立验证机制，单点失效不影响整体安全
3. **跨环境一致性**: 不依赖硬编码配置，适配开发、测试、生产环境
4. **调用链收敛**: 优先只接受来自受信部署目录中的 engine 进程调用
5. **可审计性**: 所有特权操作均有完整审计日志

---

## 架构设计

### 五层纵深防御机制 (Defense in Depth)

Launcher 采用 **L0 进程环境清理 + L1~L4 五层纵深防御** 体系。前置验证（L1）优先执行以实现早期快速拒绝，全程记录审计日志：

| 阶段 | 层级 | 验证内容 | 时机 | 绕过难度 | 防御目标 |
|:---|:---|:---|:---|:---|:---|
| 前置 | **L0: 环境清理** | 无条件移除 `LD_PRELOAD` 等危险 loader 变量 | 启动后瞬间 | 不适用（直接清理） | 环境变量预加载注入防御 |
| 前置 | **L1: 环境验证** | 文件属主/权限（严格 `700`）+ 父进程身份/路径白名单 | 启动时 | 需要 root 权限 | 阻断非法调用进程 |
| 请求 | **L2: 令牌验证** | 运行时动态 Token 匹配（常数时间比较） | 收到请求后 | 需要同机受保护文件读权限 | 阻断伪造请求 |
| 授权 | **L3: 权限验证** | Operation 白名单 + 严格参数模式校验 | 执行前 | 无法绕过（硬编码白名单） | 拦截越权与参数篡改 |
| 隔离 | **L4: 系统隔离** | seccomp-BPF 系统调用白名单 | 执行时 | 需要内核级别漏洞 | 限制进程受陷后的系统调用 |

支持的 Action 详见[启动器动作](./actions.md)文档。执行时序图参看[执行流程图](./_index.md#执行流程)。

### Action 设计原则

本平台定位为 **"身份代理 + 资源门户"**，而非 **"基础设施管理平台"**：
- **边界受限与降险**：不接管系统级服务（如 License Server、调度器由 systemd 管理）；不修改全局系统配置；所有动作严格受限于登录用户上下文。
- **避免复杂度外溢**：只读监控优先于破坏性控制；Agent 信任上游 Service 构造的参数，不做无谓重复校验；进程管理由 OS 原生机制处理。

---

## 安全机制详解

### 1. 运行环境验证机制（L1）

Launcher 启动时在读取请求前执行的安全验证，保证只有授权的进程才能继续。

#### 1.1 文件权限验证

**原理**: 验证 Launcher 二进制文件的权限和所有权，防止恶意修改。

**验证内容**:
1. 文件权限必须为 `700`（仅 owner 能执行）：group 和 other 不得有任何权限位
2. 文件所有者必须为运行 Engine/Agent 进程的系统用户
3. group/other 权限位不得包含任何位

**检测方式**: 使用 `os.Stat()` 获取文件元数据并检查权限位与 UID。
**目的**: 防止任何用户修改 Launcher 二进制或绕过权限检查。

#### 1.2 调用者身份验证

**原理**: 验证调用进程的用户身份是否与 Launcher 文件所有者的身份一致，并确认调用链来自 Agent。

**验证内容**:
1. **UID 匹配**: 调用进程的 Real UID 必须与 Launcher 所有者 UID 一致（通过 `/proc/[ppid]/status` 校验）
2. **父进程检查**: 父进程不能是 Shell（通过 `/proc/[ppid]/comm` 检查 `bash`、`sh`、`zsh` 等）
3. **父进程可执行文件校验**: 读取 `/proc/[ppid]/exe`，父进程属主必须与 Launcher 完全一致或属于标准系统 bin 目录，且文件名必须在受信白名单（`design-cloud-engine` / `dc-engine` / `runner`）中，父进程文件不得对 group/other 可写。

**目的**: 将允许的调用来源严格收敛到受信部署路径，防止系统管理员误操作、测试时绕过 Agent 直接调用，以及普通同 UID 自定义进程随意伪造调用。

**第二条受信调用路径（Engine 文件流式传输）**:

除 Agent gRPC/Agent 调用链外，还存在一条正式受信路径：Portal 服务层的文件流式传输
（`server/service/file/stream.go: StreamExecutor`）会**直接 fork dc-launcher** 以绕过
gRPC/Agent 链路实现大文件直写。该路径仍复用同一运行时目录下的 token 文件（`LauncherTokenPath()`）
构造 `ExecuteInput`，launcher 侧照常执行 L1（父进程为 engine 可执行文件，命中受信名单）与 L2 token 校验，
并不因调用方不同而放宽任何检查。

**危险环境变量清理（L0）**:

以下危险变量在进程启动时由 L0 阶段统一清理：

- `LD_PRELOAD`：库预加载注入
- `LD_LIBRARY_PATH`：库路径劫持
- `LD_AUDIT`：动态链接器审计钩子
- `LD_DEBUG`：动态链接器调试输出/信息泄露

**为什么需要显式清理？**

虽然现代 Linux 系统的动态链接器（`ld.so`）为了安全，通常会在 AT_SECURE 模式下（如运行 setuid 程序时）自动忽略这些环境变量，但对于
**静态链接**的 Go 程序（dc-launcher 即为此类），这些变量可能会保留在进程环境中。

Launcher 采取**自动清理（Sanitize）**策略：

1. **启动时清理**：进程启动后第一件事即调用 `execution.CleanCurrentProcessEnv()`。
2. **自动移除**：无条件从当前进程环境中移除上述变量（`unsetenv`），确保不会传递给子进程。

这种策略既保证了安全性（子进程运行在纯净环境），也避免了因全局环境配置（如 `/etc/profile` 设置了 `LD_LIBRARY_PATH`）导致
Launcher 启动失败。

### 2. 通信安全机制

#### 2.1 请求传输方式

**通道**: 所有 Action 都通过标准输入（stdin）发送一行换行结尾的 JSON header（`execution.ExecuteInput`）。普通 Action 在该行之后没有
body；`FILE_UPLOAD` 在换行后继续发送原始文件字节流；`FILE_DOWNLOAD` 成功时把原始文件字节写到 stdout，失败时才在 stderr 输出
JSON 错误。

**请求格式**:

```json
{
	"token": "Q8qGvP0g0w1z2A3b4C5d6E7f",
	"request": {
		"traceId": "trace-123",
		"executionId": "req-123",
		"actionName": "FILE_LIST",
		"targetUser": "alice",
		"payload": {
			"dir": "/home/alice"
		}
	}
}
```

**安全优势**:

- 敏感数据（token）不出现在进程列表（`ps` 命令）中
- 不通过命令行参数传递，避免 shell 历史记录泄露
- 所有 Action 复用统一的结构化 header，便于做 L2/L3 校验
- `FILE_UPLOAD` / `FILE_DOWNLOAD` 只让文件内容走 raw 流，不必进入 JSON envelope

#### 2.2 动态 Token 机制

**Token 生成**（`internal/agent/executor/launcher/token.go: InitToken`）:

- `launcher.InitToken` 使用 `crypto.GenSecret(24)` 生成 **24 字符 URL-safe secret**（不是 UUID）
- 运行时目录解析顺序（`fs.RuntimeDir()`）：
    1. `/run/user/<uid>/design-cloud-engine/`（当 `/run/user/<uid>` 存在时）
    2. `/tmp/design-cloud-engine-<uid>/`（回退）
- Token 文件路径：`<RuntimeDir>/dc-launcher.token`
- 创建目录使用 `fs.MkdirPrivate()`（严格 `0700`）；写入文件使用 `fs.WritePrivateFile()`（严格 `0600`，临时文件 + 原子 rename）

**Token 验证**（`internal/launcher/verifier/token.go: Token`）：

- 检查目录权限严格为 `0700`、所有者为当前用户（`fs.VerifyPrivateDir`）
- 检查文件权限严格为 `0600`、所有者为当前用户
- 读取文件内容，使用 `subtle.ConstantTimeCompare` 与请求中的 token 做常数时间比较
- 验证失败立即拒绝，不进入 L3 授权流程
- Token 路径通过包级变量 `launcherTokenPath`（函数类型）间接寻址，测试可替换，生产逻辑固定

**Token 轮换**:

- 每次 Agent **进程重启**时重新生成并覆盖写入，前一个 token 自动失效
- 无额外定时轮换机制，依靠进程生命周期保证新鲜度
- Token 仅存在于节点本地运行时目录，不在数据库中存储，不随 Engine 重启传播
- Token 值不出现在日志、审计记录或命令行参数中

#### 2.3 启动超时机制

**原理**: Launcher 启动后必须在 **7 秒**内收到并解析首行 JSON header，否则自动退出。

**实现**:

- `cmd/launcher/main.go` 使用 `context.WithTimeout(context.Background(), 7*time.Second)` 包裹 `readRequest`
- `readRequest` 从 `stdin` 读取第一行，去掉结尾换行后解析为 `execution.ExecuteInput`
- 超时针对的是**请求 header**；`FILE_UPLOAD` 的 raw body 在 header 解析完成后继续处理

**目的**: 防止 Launcher 进程被恶意保持打开状态。

### 3. 操作执行机制

#### 3.1 Action 白名单

**原理**: 当前 allowlist 由 `execution.Actions` 与 `internal/launcher/launcher.go` 的 handler 注册表共同构成。

每个 `ActionSpec` 包含以下信息：

- `Launcher *LauncherSpec`：为 `nil` 时表示 Direct（Agent 内部直接执行）；非 `nil` 时委托给 Launcher
  - `Privilege`：`PrivilegeTargetUser`（降权到目标用户）或 `PrivilegeRoot`（提权到 root）
  - `IOModel`：`IOModelJSON` / `IOModelStreaming`
  - `EnableSeccomp`：是否启用 seccomp-BPF 系统调用过滤。当前显式关闭 seccomp 的动作：
    `VNC_GENERATE_OTP`、`VNC_CONTROL`、`VNC_CGROUP_CONTROL`（外部工具/vncserver 系统调用面宽，
    无法被窄 allowlist 覆盖；其中前两者仍会应用 NoNewPrivs，见 §4.2）
- `Description`：动作说明
- `NewPayload`：期望的 payload struct 类型

**安全特性**:

- `verifier.Action` 会拒绝未知 `actionName`
- Direct 模式的动作（`Spec.IsDirect()` 为真，例如 `JOB_QUERY`）不会通过 Launcher 执行
- `IOModelStreaming` 动作不会进入普通 handler registry，而是由 `main` 直接路由到流式执行逻辑

#### 3.2 Payload 与参数验证

**验证内容**:

1. `execution.GetPayload` 先校验 `actionName` 与 `NewPayload` 是否匹配，再把原始 JSON 反序列化为强类型 struct
2. 文件类 handler 会继续执行 `validatePath` / `validateEntryName` 等校验，阻止敏感系统路径和路径穿越
3. 用户类 handler 会执行 `validateTargetUser` / `validatePassword`，禁止 root、系统用户（UID < 1000）和非法密码输入
4. Job/Env 相关逻辑会过滤非法环境变量名及危险 key
5. 流式 `FILE_UPLOAD` / `FILE_DOWNLOAD` 不走普通 handler registry，当前只检查 payload 结构和关键字段（如 `dstPath` /
   `srcPath` / `size`）

#### 3.3 系统命令与脚本调用

Launcher 当前没有通用的“命令模板 + 占位符”模型；主要执行路径如下：

1. **密码类动作**：调用系统命令 `chpasswd`
2. **Job 类动作**：执行上游预组装的 `CommandSpec`
    - `EnvScript == ""`：直接按 `Program + Args` 调用，`WorkDir` 设置为 `cmd.Dir`
    - `EnvScript != ""`：通过 `bash -c '. "$0" && exec "$@"' <envScript> <program> <args...>` 安全传参执行（参数作为 bash 独立位置变量，无需字符串转义与引号拼接）
3. **VNC 会话控制动作**：
    - **`VNC_CONTROL`**：以目标普通用户身份直接按 `Program + Args` 参数切片调用 `vncserver` / `vncserver -kill`，不经由 shell。
    - **`VNC_CGROUP_CONTROL`**：以 root 身份调用 `systemd-run --scope --slice=user-<uid>.slice runuser --login <targetUser> --session-command "<shellCmd>"`。为了杜绝 shell 注入漏洞，所有命令与参数切片均通过 `quoteShellArg`（POSIX 单引号转义，将内部 `'` 转为 `'\''`）进行严格安全封装；同时将进程工作目录切换为目标用户的家目录，完整建立用户环境与 D-Bus 会话。

#### 3.4 命令执行安全

**执行方式**:

- 大多数命令路径使用 `exec.CommandContext` / slice 传参，不经过通用 `sh -c`
- 仅在配置了 `EnvScript` 时，通过 `bash -c '. "$0" && exec "$@"'` 进行受控环境 source 与 exec 替换
- `VNC_CGROUP_CONTROL` 通过 `runuser --session-command` 执行登录复合命令时，对所有命令行切片元素进行严格的 POSIX 单引号转义（`quoteShellArg`），防止任意参数注入
- `runCommandWithDir` 会把基础环境固定为 `PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` 与
  `LC_ALL=C`
- 当前 launcher 代码设置了 `SysProcAttr{Setpgid: true}`

**超时控制**:

- 普通 launcher Action 默认超时 30 秒，请求值可缩短，但放大值会被截断到最大 60 秒
- 流式 Action（`FILE_UPLOAD` / `FILE_DOWNLOAD`）不应用 30/60 秒超时包装（`withTimeout=false`），由 Engine 上游 context 控制传输生命周期，且下载路径支持取消中断透传

#### 3.5 身份切换模型

当前推荐模型不是让 Engine 常驻 Root，也不是把 Launcher 作为通用 Root 管理器，而是：

1. **`PrivilegeTargetUser`**：Launcher 解析 `targetUser` 身份（`id` + `getent passwd`），拒绝 `root` /
   `nobody` / UID < 1000，然后执行 `Setgroups` / `Setgid` / `Setuid`
2. **`PrivilegeRoot`**：以 root 身份执行的白名单动作。除密码相关特权动作（修改/重置/校验 shadow）外，
   还包括 **`VNC_CGROUP_CONTROL`** —— 以 root 执行 `systemd-run --scope`（挂载 `--slice=user-<uid>.slice`）
   并内嵌调用 `runuser --login <targetUser>` 启动用户级 vncserver；该动作同时**禁用 seccomp**（`EnableSeccomp: false`）。
   进入该分支时若当前 UID 已为 root 则显式切换到 UID/GID 0。虽然以此特权身份执行，但 handler 会对 `targetUser` 实施白名单校验（非空、非 root/nobody、禁止以 `-` 开头或包含空字节、系统解析 UID ≥ 1000），且通过 `runuser --login` 自动将权限和会话收敛到该目标用户。

这使得 Launcher 的高权限窗口尽量小，并把 Root 能力收缩在少量白名单动作里。

### 4. 环境隔离机制

#### 4.1 环境变量清理

**目的**: 防止通过动态链接器/加载器相关环境变量注入攻击。

**实现**:

1. **L0 启动即清理**：`execution.CleanCurrentProcessEnv()` 会一次性移除 `LD_PRELOAD`、`LD_AUDIT`、`LD_DEBUG`、
   `LD_LIBRARY_PATH`
2. **L1 校验范围**：L1 只覆盖运行目录权限与调用方身份（UID、父进程链），不重复做危险环境变量检查 ——
   环境清洗仅在派生侧（L0）完成一次
3. **环境变量过滤**：`BuildCleanEnv` / `BuildLauncherEnv` 以及 Job/Exec 相关 env 合并逻辑都会丢弃非法 key 与上述危险 key
4. **执行环境不是“最小空环境”**：`prepareExecution` 会在当前进程环境基础上补充 `USER`、`LOGNAME`、`HOME`、`SHELL` 以及请求中的安全
   env

**当前危险变量集合**:

- `LD_PRELOAD`
- `LD_AUDIT`
- `LD_DEBUG`
- `LD_LIBRARY_PATH`

#### 4.2 NoNewPrivs 标志

`PR_SET_NO_NEW_PRIVS` **已实现**（`seccomp.go:applyNoNewPrivs`）：对所有 `PrivilegeTargetUser`
动作生效 —— 包括关闭 seccomp allowlist 的动作（如 `VNC_GENERATE_OTP` / `VNC_CONTROL`），
在 seccomp 关闭时仍作为独立防护执行；身份切换（setuid/setgid）之后、handler 执行前通过 `prctl` 设置。

作用范围仅限 launcher 进程自身：handler 子进程并未通过 `SysProcAttr` 显式继承该策略，
不依赖 NoNewPrivs 约束子进程行为。

此外，当前安全边界还依赖于：

- L0 的危险环境变量清理
- `PrivilegeTargetUser` / `PrivilegeRoot` 的 UID/GID 切换
- L2/L3 的 token 与 action allowlist 校验
- L4 的 seccomp-BPF 系统调用过滤

### 5. 系统调用隔离机制（L4）

Launcher 在完成身份切换后、执行 handler 前，应用 seccomp-BPF 过滤器限制进程能执行的系统调用。

#### 5.1 seccomp-BPF 原理

**seccomp (Secure Computing Mode)** 是 Linux 内核提供的系统调用过滤机制，通过 BPF (Berkeley Packet Filter) 程序定义白名单。

**工作原理**:

1. 进程调用 `prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, ...)`
2. 内核在每次系统调用前执行 BPF 程序
3. BPF 程序检查系统调用号是否在白名单中
4. 不在白名单的系统调用触发 `SIGSYS` 信号，进程被杀死

**特点**:

- **不可逆**: 一旦应用，无法移除（除非进程退出）
- **继承性**: 子进程自动继承 seccomp 过滤器
- **内核强制**: 无法通过用户态代码绕过

#### 5.2 白名单设计

根据 LauncherSpec 动态构建系统调用白名单：

**基础系统调用**（所有模式）:

- 文件 I/O: `read`, `write`, `open`, `close`, `stat`, `mmap`
- 进程管理: `exit`, `getpid`, `getuid`, `clone`, `wait4`
- 信号处理: `rt_sigaction`, `rt_sigprocmask`, `rt_sigreturn`
- 时间: `clock_gettime`, `gettimeofday`, `nanosleep`
- Go runtime: `futex`, `sched_yield`, `epoll_*`

**PrivilegeTargetUser** (文件操作 + 作业提交):

- 文件操作: `unlink`, `rename`, `mkdir`, `chmod`, `chown`
- 进程创建: `execve`, `fork`, `dup`, `pipe`, `setpgid`
- 网络: `socket`, `connect` (用于调度器工具)

**PrivilegeRoot** (密码管理等 root 动作；注意 `VNC_CGROUP_CONTROL` 关闭了 seccomp，不加载本过滤器):

- 包含 User 模式的所有系统调用
- 额外: `lseek`, `fsync` (用于 shadow 文件操作)

**明确禁止的系统调用**:

- `ptrace`: 调试其他进程
- `mount`, `umount`: 挂载文件系统
- `reboot`, `kexec_load`: 系统重启
- `init_module`, `delete_module`: 加载内核模块
- `iopl`, `ioperm`: 直接硬件访问
- `acct`, `swapon`, `swapoff`: 系统管理

#### 5.3 应用时机

seccomp 过滤器在以下时机应用：

```go
// 1. 完成身份切换（setuid/setgid）
setuid(targetUID)
setgid(targetGID)

// 2. 应用 seccomp 过滤器
applySeccompFilter(spec.Mode)  // ← L4 在此应用

// 3. 执行 handler
handler(ctx, req)
```

**为什么在 setuid 之后？**

- `PrivilegeTargetUser`: 降权后需要设置 `PR_SET_NO_NEW_PRIVS`（非 root 进程的内核要求）
- `PrivilegeRoot`: root 进程可以直接应用 seccomp，无需 `PR_SET_NO_NEW_PRIVS`

#### 5.4 失败处理

seccomp 加载失败时采用 **非致命（Non-Fatal）** 策略：

```go
if err := loadFilter(filter); err != nil {
    fmt.Fprintf(os.Stderr, "seccomp: failed to load filter: %v\n", err)
    // Non-fatal: other security layers (L1-L3, setuid) remain active.
}
```

**原因**:

1. 旧内核（< 3.5）不支持 seccomp-BPF
2. **开发环境**: 某些容器环境可能禁用 seccomp
3. **纵深防御**: L1-L3 仍然提供强大的防护

**监控建议**:

- 生产环境应监控 seccomp 加载失败事件
- 如果频繁失败，应调查内核配置或容器限制

#### 5.5 L4 的独特价值

L4 提供了 L1-L3 无法提供的防御：

| 威胁场景              | L1-L3 能否防御？ | L4 如何防御？                   |
|-------------------|-------------|----------------------------|
| **代码注入漏洞**        | ❌ 不能        | 限制注入代码能执行的系统调用             |
| **依赖库漏洞**         | ❌ 不能        | 限制库代码能执行的系统调用              |
| **Handler 逻辑漏洞**  | ⚠️ 部分       | 即使 handler 有漏洞，也无法执行危险系统调用 |
| **特权滥用**          | ❌ 不能        | 即使是 root 进程，也只能执行白名单系统调用   |
| **Go Runtime 漏洞** | ❌ 不能        | 限制 runtime 能执行的系统调用        |

**具体示例**:

```go
// 假设某个 handler 存在命令注入漏洞
func vulnerableHandler(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
    // 危险：直接拼接用户输入
    cmd := exec.Command("sh", "-c", "echo "+req.Payload["message"])
    return cmd.CombinedOutput()
}
```

**攻击尝试**:

```json
{
  "action": "VULNERABLE_ACTION",
  "payload": {
    "message": "hello; reboot"  // 尝试重启系统
  }
}
```

**防御效果**:

- **L1-L3**: ✅ 通过（调用者合法、Token 正确、Operation 在白名单）
- **L4**: ❌ 阻止！`reboot` 系统调用不在白名单中，进程收到 `SIGSYS` 信号被杀死

#### 5.6 与其他层的协同

L4 不能替代 L1-L3，原因如下：

**L4 无法替代 L1**:

- seccomp 无法检查文件权限（如果 launcher 被篡改为 777，任何用户都能执行）
- seccomp 无法验证调用者身份（无法区分"合法 Agent"和"恶意同 UID 进程"）
- seccomp 无法检查父进程（无法阻止从 shell 直接调用）

**L4 无法替代 L2**:

- seccomp 无法验证 Token（无法区分"真实请求"和"伪造请求"）
- seccomp 无法限制请求频率（无法防止 token 被窃取后的滥用）

**L4 无法替代 L3**:

- seccomp 无法检查业务逻辑（无法限制执行哪些 Operation）
- seccomp 无法验证参数语义（无法防止路径遍历攻击，如 `../../../../etc/shadow`）
- seccomp 只能限制系统调用，无法限制系统调用的参数

**协同示例**:

```
攻击者尝试读取 /etc/shadow
    ↓
L1: ✅ 通过（假设攻击者获得了 Agent UID）
    ↓
L2: ✅ 通过（假设攻击者窃取了 Token）
    ↓
L3: ❌ 阻止！参数验证检测到路径遍历 "../"
    ↓
（如果 L3 有漏洞，允许了该路径）
    ↓
L4: ⚠️ 无法阻止（open、read 是白名单内的系统调用）
    ↓
文件系统权限: ❌ 阻止！进程以普通用户身份运行，无权读取 /etc/shadow
```

---

## 部署配置

### 文件权限设置

```bash
# 1. 创建服务用户（与包装默认一致；无 home）
sudo groupadd -r design-cloud || true
sudo useradd -r -M -g design-cloud -s /sbin/nologin design-cloud || true

# 2. 安装二进制文件（同一目录，本地盘）
INSTALL_DIR="/opt/design-cloud"
sudo mkdir -p $INSTALL_DIR
# 先 install 二进制，再收口属主/权限（勿对空路径 chown）
sudo install -m 755 build/design-cloud-engine $INSTALL_DIR/design-cloud-engine
sudo install -m 700 build/dc-launcher $INSTALL_DIR/dc-launcher
sudo chown design-cloud:design-cloud $INSTALL_DIR/design-cloud-engine $INSTALL_DIR/dc-launcher

# 3. 赋予 Launcher Capabilities (文件级特权)
# [重要提示] dc-launcher 二进制必须部署在节点本地磁盘文件系统中（如 ext4, xfs）。
# 如果部署在 NFS 共享存储并通过客户端执行，NFS 的安全限制（如 `nosuid` 参数）
# 会导致内核忽略这里的 setcap 属性，进而引发 Launcher 切换用户时报 "operation not permitted" 错误。
sudo setcap cap_setuid,cap_setgid+ep $INSTALL_DIR/dc-launcher
```

### Systemd 服务配置

生产以包内 [`deployment/design-cloud-engine.service`](../../../../deployment/design-cloud-engine.service) 为准。精简示意：

```ini
[Unit]
Description=Design Cloud Engine
After=network-online.target

[Service]
Type=simple
User=design-cloud
Group=design-cloud
WorkingDirectory=/opt/design-cloud
RuntimeDirectory=design-cloud
Environment=RUNTIME_DIRECTORY=/run/design-cloud
ExecStart=/opt/design-cloud/design-cloud-engine --config /etc/design-cloud/config.toml --log-dir /var/log/design-cloud
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/etc/design-cloud /var/lib/design-cloud /var/log/design-cloud /run/design-cloud

[Install]
WantedBy=multi-user.target
```

configless Agent 用 `systemctl edit` drop-in 覆盖 `ExecStart`（见包内 `configless.conf.example`），勿改成另一套 User/路径。

---

## 安全验证

### 部署后验证脚本

```bash
#!/bin/bash
# 验证关键安全配置

LAUNCHER="/opt/design-cloud/dc-launcher"

# 1. 检查文件所有者
OWNER=$(stat -c '%U' $LAUNCHER)
echo "Launcher owner: $OWNER"

# 2. 检查权限
PERM=$(stat -c '%a' $LAUNCHER)
echo "Launcher permissions: $PERM (expected: 700)"

# 3. 检查 Capabilities
CAPS=$(getcap $LAUNCHER)
echo "Launcher capabilities: $CAPS"

# 4. 检查运行时目录（生产：systemd RuntimeDirectory → /run/design-cloud）
RUNTIME_DIR="${RUNTIME_DIRECTORY:-/run/design-cloud}"
if [ -d "$RUNTIME_DIR" ]; then
    TOKEN_OWNER=$(stat -c '%U' "$RUNTIME_DIR")
    TOKEN_PERM=$(stat -c '%a' "$RUNTIME_DIR")
    echo "Runtime dir owner: $TOKEN_OWNER"
    echo "Runtime dir permissions: $TOKEN_PERM (expected: 700)"
fi

# 5. 验证 Agent 和 Launcher 所有者一致
AGENT_OWNER=$(stat -c '%U' /opt/design-cloud/design-cloud-engine)
if [ "$AGENT_OWNER" == "$OWNER" ]; then
    echo "Owner consistency check passed"
else
    echo "Owner mismatch: Agent=$AGENT_OWNER, Launcher=$OWNER"
    exit 1
fi
```

---

## 威胁模型

### 攻击场景分析

| 攻击者能力       | 攻击目标            | L1  | L2  | L3  | L4  | 结果                |
|-------------|-----------------|-----|-----|-----|-----|-------------------|
| 普通用户        | 直接执行 Launcher   | ❌   | -   | -   | -   | 拒绝执行（文件权限）        |
| 普通用户        | 编写程序调用 Launcher | ❌   | -   | -   | -   | 拒绝执行（UID 不匹配）     |
| 获取 Agent 权限 | 读取 Token 文件     | ✅   | ⚠️  | -   | -   | 可以读取（但有时间窗口限制）    |
| 获取 Agent 权限 | 自定义同 UID 进程直接调用 | ⚠️  | ❌   | -   | -   | 大部分场景拒绝（父进程校验）    |
| 获取 Agent 权限 | 窃取 Token 发送恶意命令 | ✅   | ✅   | ❌   | -   | 只能执行白名单 Operation |
| 获取 Agent 权限 | 参数注入攻击          | ✅   | ✅   | ❌   | -   | 拒绝执行（参数验证）        |
| 获取 Agent 权限 | 路径遍历攻击          | ✅   | ✅   | ❌   | -   | 拒绝执行（路径验证）        |
| 获取 Agent 权限 | 利用 handler 代码漏洞 | ✅   | ✅   | ✅   | ❌   | 只能执行白名单系统调用       |
| 获取 Agent 权限 | 尝试执行 reboot     | ✅   | ✅   | ✅   | ❌   | 进程被杀死（SIGSYS）     |
| 获取 Agent 权限 | 尝试加载内核模块        | ✅   | ✅   | ✅   | ❌   | 进程被杀死（SIGSYS）     |
| Root 用户     | 绕过所有检查          | N/A | N/A | N/A | N/A | Agent 拒绝以 root 运行 |

**图例**:

- ✅ 通过该层检查
- ❌ 被该层阻止
- ⚠️ 部分通过（有限制条件）
- `-` 未到达该层

### 安全保证

**在以下假设成立的前提下，系统是安全的**:

1. Linux 内核的进程隔离机制未被突破
2. 文件系统权限机制正常工作
3. `/proc` 文件系统未被篡改
4. Launcher 二进制文件未被替换
5. seccomp-BPF 内核功能正常工作

**在以下情况下，系统仍能维持主要安全边界**:

1. Agent 进程存在远程代码执行漏洞（L4 限制系统调用）
2. 其他普通用户尝试直接执行 launcher（L1 阻止）
3. 攻击者能够发送任意请求到 Launcher（L2-L3 阻止）
4. Launcher handler 存在代码漏洞（L4 限制系统调用）
5. 依赖库存在安全漏洞（L4 限制系统调用）

**需要明确的限制**:

1. 如果攻击者已经获得与 engine 相同的 UID 权限，且能够在受信目录中替换/伪装受信 engine 二进制，则无法仅依靠 launcher
   自身彻底阻止调用
2. 因此，父进程目录/文件名校验的目标是**缩小攻击面**，而不是宣称对"同 UID 完全沦陷"场景提供绝对防护
3. L4 (seccomp) 在旧内核或某些容器环境中可能无法加载，此时仍依赖 L1-L3 提供防护

**攻击者无法实现**:

1. 执行白名单之外的 Operation（L3 阻止）
2. 执行白名单之外的系统调用（L4 阻止）
3. 篡改已部署的脚本内容（文件系统权限 + L4 阻止）
4. 绕过参数验证逻辑（L3 阻止）
5. 读取其他用户的文件（文件系统权限 + L3 参数验证）
6. 调试其他进程（L4 阻止 ptrace）
7. 加载内核模块（L4 阻止 init_module）
8. 重启系统（L4 阻止 reboot）

---

## 安全开发规范

### 代码审查清单

在编写或审查涉及 Launcher 的代码时，必须确认：

#### 1. 身份与权限校验

- [x] **UID 动态绑定**: 验证 `caller_uid == launcher_file_owner_uid`（实现于 `verify/environment.go:checkCaller`）
- [ ] **组权限限制**: launcher 二进制文件权限 `700`，具备 `cap_setuid,cap_setgid+ep`（文件权限 700 由
  `verifyFilePermissions` 运行时检查；setcap 由部署脚本保证）
- [x] **父进程路径校验**: 验证父进程所在目录（通过属主匹配机制放行，或为标准系统 bin 目录）（实现于
  `verify/environment.go:verifyTrustedParentExecutable`）
- [x] **父进程文件名校验**: 验证父进程为受信任的 engine 可执行文件名（实现于
  `verify/environment.go:verifyTrustedParentExecutable`）
- [x] **禁止 Shell 启动**: 检测父进程是否为 Shell，是则拒绝（实现于 `verify/environment.go:checkCaller`，校验
  `knownShellParents`）

#### 2. 通信安全

- [x] **stdin header**: 请求通过 stdin 首行换行分隔的 JSON 头部行传入（newline-delimited JSON header line），
  不使用命令行参数（实现于 `main.go:readRequest`）
- [x] **动态 Token 校验**: 验证请求中的 Token 与文件中的一致（实现于 `verify/token.go:Token`，L2 校验，使用常量时间比较）
- [x] **启动超时机制**: 7 秒内未收到并解析 header 则自动退出（实现于 `main.go:startupTimeout` + context deadline）

#### 3. 指令执行逻辑

- [x] **ActionSpec allowlist**: 只接受 `execution.Actions` 中允许经 launcher 执行的 `actionName`（实现于
  `verify/action.go:Action`，L3 校验）
- [x] **Payload 类型绑定**: 通过 `NewPayload` / `execution.GetPayload` 校验 action 与 payload 一致性（各 handler 入口调用
  `GetPayload`）
- [x] **禁用通用 Shell 解析**: 常规路径使用 `exec.CommandContext` + slice 传参；仅在 `InitScript` 非空时使用
  `BuildShellCommand` / `ShellQuote`
- [x] **参数合法性过滤**: handler 侧继续执行路径/用户名/密码等针对性校验

#### 4. 环境清理

- [x] **危险变量清理**: 处理 `LD_PRELOAD`、`LD_AUDIT`、`LD_DEBUG`、`LD_LIBRARY_PATH`（L0:
  `main.go:CleanCurrentProcessEnv` 启动时一次性移除）
- [x] **环境变量过滤**: 合并 env 时丢弃非法 key 与危险 key（`BuildCleanEnv` / `BuildLauncherEnv`）
- [x] **NoNewPrivs**: 通过 `PR_SET_NO_NEW_PRIVS` 设置于 `seccomp.go:applyNoNewPrivs`（对所有 `PrivilegeTargetUser` 生效，且在 seccomp 关闭时仍作为独立 fallback 执行）
- [x] **Setpgid**: 子进程创建时设置独立进程组（`runCommandWithDir` 设置
  `SysProcAttr{Setpgid: true}`，未设置 Noctty）。已知例外：VNC 相关 handler 直接使用
  `exec.CommandContext`，未走统一受限启动器路径（无 PATH/LC_ALL 锁定与 Setpgid 设置）

#### 5. 系统调用隔离

- [x] **seccomp 应用**: 在 setuid/setgid 后、handler 执行前应用 seccomp 过滤器（实现于 `seccomp.go`，L4）
- [x] **白名单最小化**: 只允许 LauncherSpec 所需的系统调用（`seccomp.go` 中按模式分别允许/禁止 syscall）
- [x] **架构验证**: 验证系统调用来自 x86-64 架构（`seccomp.go` 中 `BuildFilter` 的 arch 偏移检查）
- [x] **失败处理**: seccomp 加载失败时记录日志但不中断执行

#### 6. 禁止的模式（设计原则，代码层面已遵循）

- [x] **绝不** 使用 `os.System()` 或 `exec.Command("/bin/sh", "-c", ...)`
- [x] **绝不** 拼接字符串构造命令: `cmd + " " + modes`
- [x] **绝不** 提供通用的 `DELETE_FILE`、`RUN_SCRIPT` 接口
- [x] **绝不** 接受用户传入的脚本内容
- [x] **绝不** 信任未经验证的路径参数

---

## 审计与监控

### 审计事件

虽然 Launcher 本身是无状态且不直接写日志文件的，但它会返回详细的执行结果，由 Engine 统一记录以下审计事件：

| 事件类型                         | 级别       | 触发条件           | 记录内容                                        |
|------------------------------|----------|----------------|---------------------------------------------|
| `LAUNCHER_EXECUTION_SUCCESS` | Info     | Operation 执行成功 | action, duration, exit_code, output_summary |
| `LAUNCHER_EXECUTION_FAILED`  | Error    | Operation 执行失败 | action, error, exit_code                    |
| `LAUNCHER_TOKEN_ERROR`       | Critical | Token 验证失败     | error_message                               |

> [!NOTE]
> 当 Launcher stderr 输出超过 `execution.DefaultMaxCommandOutputBytes` 限制时，执行器会在截断点后追加标识内容
`[stderr truncated: exceeded N bytes]`，以便审计记录和日志中能明确看到截断边界。

### 日志格式

```
[时间戳] [级别] [事件类型] 详细信息

示例:
2026-01-09T10:30:45.123Z [CRITICAL] IDENTITY_CHECK_FAILED caller_uid=1002 launcher_owner=1001 ppid=12345 binary=/opt/design-cloud/design-cloud-engine
2026-01-09T10:31:15.456Z [INFO] ACTION_EXECUTED action=CHECK_DISK_USAGE duration=0.05s exit_code=0
```

### 监控指标

**关键指标**:

- `launcher_security_check_failures_total{check_type, reason}` - 安全检查失败次数
- `launcher_action_executions_total{action, status}` - 操作执行统计
- `launcher_action_duration_seconds{action}` - 操作执行耗时

**告警规则**:

- 5 分钟内安全检查失败次数 > 10：立即告警
- Launcher 二进制文件 hash 变化：立即告警
- 异常 Operation 执行频率：警告

---

## 合规性

本架构符合以下安全标准：

- **NIST SP 800-53 AC-6**: 最小权限原则
- **NIST SP 800-53 AU-2**: 审计和问责
- **NIST SP 800-53 AC-3**: 访问控制
- **NIST SP 800-53 IA-2**: 用户身份验证
- **NIST SP 800-53 SI-16**: 内存保护 (防止代码注入利用)
- **CIS Controls 4.1**: 安全配置管理
- **CIS Controls 18.5**: 应用软件安全 (系统调用级纵深防御)
- **CIS Controls 5.1**: 特权账户管理

---

## 参考资料

- Linux Capabilities: `man 7 capabilities`
- /proc 文件系统: `man 5 proc`
- Go exec 包: https://pkg.go.dev/os/exec
- OWASP Command Injection: https://owasp.org/www-community/attacks/Command_Injection
