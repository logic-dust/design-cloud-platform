---
title: 内置 Action 规范与注册
linkTitle: Action 规范
weight: 20
description: dc-exec 与 Agent 支持的特权动作三维正交模型、契约定义与注册规范
aliases:
  - /docs/architecture/dcexec/actions
---

## 简介

### 什么是 Action（三维正交模型）

Action 是 `execution.ExecuteRequest.ActionName` 对应的受控执行单元。系统使用 `execution.ActionSpec` 描述每个 Action，解耦为三个正交维度：

| 维度 | 解决什么问题 | 枚举 / 可选值 | 说明 |
|:---|:---|:---|:---|
| **1. 路由与执行者 (Runner)** | 由谁执行？要不要跨越安全边界？ | • **Direct** (Agent 原生直接跑)<br>• **DcExec** (委托给特权代理) | 绝大多数只读查询走 Direct；涉及用户文件/特权操作走 dc-exec |
| **2. 权限策略 (Privilege)**<br>*(仅 dc-exec 关注)* | 运行时使用什么 Linux 身份？ | • **TargetUser** (降权到目标用户)<br>• **Root** (提权到超级用户) | 读写用户文件/提交作业用 TargetUser；改 shadow 密码用 Root |
| **3. I/O 与交互模型 (IOModel)** | 进程间怎么传数据？生命周期如何？ | • **JSON** (标准内存 RPC 响应)<br>• **Streaming** (管道二进制流) | 决定通信管道是发 JSON 还是拷贝文件字节流 |

### 安全模型

- **路由解耦**：Agent 原生身份可直接完成的只读操作（`Spec.IsDirect()`）不进入 DcExec，零额外进程开销；
- **身份隔离**：dc-exec 仅在 `PrivilegeRoot` 时显式提权到 root，`PrivilegeTargetUser` 会严格降权并切换到 `targetUser`（UID ≥ 1000）；
- **仅受理 Allowlist Action**：dc-exec L3 校验仅允许 `execution.Actions` 中声明了 `PrivilegedExecSpec` 的 Action，Direct Action 会被直接拒绝；
- **流式大文件传输**：`IOModelStreaming`（`FILE_UPLOAD` / `FILE_DOWNLOAD`）复用统一的换行分隔 JSON 头部行协议（首行 `[compact JSON ExecuteInput]\n`，随后为可选的原始文件字节），直接在标准输入输出上进行流式拷贝；
- **结构化 Payload**：dc-exec 运行时通过 `execution.GetPayload` 强类型校验 `ActionName` ↔ Payload 结构体；
- **防御纵深**：文件类 Action 严格执行路径黑名单校验；外部命令使用固定 `PATH` / `LC_ALL` 并过滤危险环境变量（`LD_PRELOAD` 等）。

## 协议说明

### 请求格式

dc-exec 从 stdin 读取一行 `execution.ExecuteInput` JSON header：

```json
{
  "token": "<dcexec-token>",
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

- `actionName`：Action 常量名
- `targetUser`：`PrivilegeTargetUser` 必须提供；`PrivilegeRoot` 大多不使用（特例：`VNC_CGROUP_CONTROL` 必须提供目标用户以定位 `user-<uid>.slice` 与 `runuser` 用户环境）
- `FILE_UPLOAD`：header 后继续写 raw body
- `FILE_DOWNLOAD`：只发送 header

### 响应格式

普通 JSON Action / `FILE_UPLOAD`：

```json
{
  "success": true,
  "message": "Action executed successfully",
  "output": "操作结果字符串（可能是 JSON）"
}
```

失败时：

```json
{
  "success": false,
  "message": "Error",
  "error": "错误信息"
}
```

`FILE_DOWNLOAD` 成功时直接把文件字节写到 stdout；失败时只在 stderr 输出 JSON 错误。

## 安全验证层

dc-exec 采用四层纵深防御模型，在 handler 执行前依次校验：

| 层级     | 名称         | 职责                                                                                                                                                                     |
|:-------|:-----------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **L1** | 运行时环境      | 启动时验证 dc-exec 二进制文件权限（700）、调用方 UID 与 dc-exec 所有者匹配、拒绝 shell 父进程调用、拒绝非信任父进程可执行文件（仅信任 `dc-engine` / `runner`）                                  |
| **L2** | Token 验证   | 验证请求携带的 token 与 `<RuntimeDir>/dc-exec.token` 一致（`/run/user/<uid>/design-cloud-engine/` 或回退 `/tmp/design-cloud-engine-<uid>/`）；Token 目录权限 0700、文件权限 0600，对比使用恒定时间比较 |
| **L3** | Action 白名单 | 检查 ActionName 是否在 `execution.Actions` 中、`Spec.IsDirect()` 为假（即必须委托给 DcExec）、handler 存在。`IOModelStreaming` Action 跳过 handler 存在性检查                                                |
| **L4** | Seccomp    | 默认对所有 dc-exec action 设置 NoNewPrivs 并以 TSYNC 加载 BPF seccomp 白名单；`PrivilegeTargetUser` 白名单不含身份切换调用，`PrivilegeRoot` 额外放行身份切换与 shadow/PAM 读写调用；`DisableSeccomp` / `DisableNoNewPrivs` 显式关闭 |

## 支持的 Operation

以下表格列出所有注册的 Action 及其在三维正交模型中的归类：

| Operation ID                                  | 描述                                   | 1. 路由 (Runner) | 2. 权限 (Privilege) | 3. I/O 模型 (IOModel) | TargetUser |
|:----------------------------------------------|:-------------------------------------|:---------------|:-------------------|:--------------------|:-----------|
| `USER_LIST`                                   | 列出系统用户                               | **Direct**     | *(无)*              | JSON                | 否          |
| `USER_GET`                                    | 获取单个用户信息                             | **Direct**     | *(无)*              | JSON                | 否          |
| `GROUP_LIST`                                  | 列出系统用户组                              | **Direct**     | *(无)*              | JSON                | 否          |
| `GROUP_GET`                                   | 获取单个用户组信息                            | **Direct**     | *(无)*              | JSON                | 否          |
| `LICENSE_GET_STATUS`                          | 查询 License 状态                        | **Direct**     | *(无)*              | JSON                | 否          |
| `LICENSE_LIST_FEATURES`                       | 查询 License 特性                        | **Direct**     | *(无)*              | JSON                | 否          |
| `JOB_QUERY`                                   | 查询作业信息                               | **Direct**     | *(无)*              | JSON                | 否          |
| [USER_PASSWORD_CHANGE](#user_password_change) | 用户自主验证原密码并修改密码                       | dc-exec       | **Root**           | JSON                | 不支持        |
| [USER_PASSWORD_RESET](#user_password_reset)   | 管理员重置指定用户的密码                         | dc-exec       | **Root**           | JSON                | 不支持        |
| [USER_PASSWORD_VERIFY](#user_password_verify) | 验证本地用户密码（读取 /etc/shadow）             | dc-exec       | **Root**           | JSON                | 不支持        |
| [VNC_CGROUP_CONTROL](#vnc_cgroup_control)     | 通过 systemd-run Cgroup slice 控制 VNC  | dc-exec       | **Root**           | JSON                | 必须         |
| [FILE_LIST](#file_list)                       | 获取指定目录下的文件和目录列表                      | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_READ](#file_read)                       | 读取小文件内容（Base64 编码返回，不超过 1 MB）        | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_DELETE](#file_delete)                   | 批量删除文件或递归删除目录                        | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_MOVE](#file_move)                       | 移动文件或目录到新位置                          | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_RENAME](#file_rename)                   | 在当前目录下重命名文件或目录                       | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_MKDIR](#file_mkdir)                     | 创建新目录（权限 0755）                       | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_MKDIR_ALL](#file_mkdir_all)             | 递归创建目录（已存在时幂等）                       | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_CHECK](#file_check)                     | 检查文件是否存在并获取详细元数据                     | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_WRITE_CHECK](#file_write_check)         | 通过创建临时文件探测目录真实写权限                    | dc-exec       | TargetUser         | JSON                | 必须         |
| [FILE_UPLOAD](#file_upload)                   | 流式上传大文件（先写临时文件再原子 rename）            | dc-exec       | TargetUser         | **Streaming**       | 必须         |
| [FILE_DOWNLOAD](#file_download)               | 流式下载大文件（文件内容直接写到 stdout）             | dc-exec       | TargetUser         | **Streaming**       | 必须         |
| [TRASH_MOVE](#trash_move)                     | 将文件安全移动到符合 XDG 规范的用户回收站              | dc-exec       | TargetUser         | JSON                | 必须         |
| [TRASH_DELETE](#trash_delete)                 | 永久删除回收站中的指定项目                        | dc-exec       | TargetUser         | JSON                | 必须         |
| [TRASH_DELETE_MANY](#trash_delete_many)       | 批量永久删除回收站中的多个项目                      | dc-exec       | TargetUser         | JSON                | 必须         |
| [TRASH_RESTORE_MANY](#trash_restore_many)     | 批量将回收站中的多个项目还原回原始路径                  | dc-exec       | TargetUser         | JSON                | 必须         |
| [TRASH_EMPTY](#trash_empty)                   | 彻底清空当前用户的回收站                         | dc-exec       | TargetUser         | JSON                | 必须         |
| [TRASH_LIST](#trash_list)                     | 列出回收站中的文件及其原始删除信息                    | dc-exec       | TargetUser         | JSON                | 必须         |
| [JOB_SUBMIT](#job_submit)                     | 在目标用户身份下提交新任务                        | dc-exec       | TargetUser         | JSON                | 必须         |
| [JOB_CONTROL](#job_control)                   | 对现有任务执行生命周期控制（kill/suspend/resume 等） | dc-exec       | TargetUser         | JSON                | 必须         |
| [JOB_MODIFY](#job_modify)                     | 修改作业属性（队列、名称等）                       | dc-exec       | TargetUser         | JSON                | 必须         |
| [CONNECTION_CHECK](#connection_check)         | 检查 VNC 连接存活状态                        | dc-exec       | TargetUser         | JSON                | 必须         |
| [VNC_GENERATE_OTP](#vnc_generate_otp)         | 为运行中的 TurboVNC 会话生成一次性密码             | dc-exec       | TargetUser         | JSON                | 必须         |
| [VNC_CONTROL](#vnc_control)                   | 启动或停止 VNC 会话                        | dc-exec       | TargetUser         | JSON                | 必须         |

---

### USER_PASSWORD_CHANGE

**功能**: 用户自主验证原密码并修改密码（需要读取 /etc/shadow 验证原密码、调用 chpasswd 写新密码）

**权限**:

- **Privilege**: `PrivilegeRoot`（需要读取 /etc/shadow 并以 root 身份调用 chpasswd）
- **DisableSeccomp**: True（`chpasswd` 等外部工具系统调用面宽）
- **DisableNoNewPrivs**: True（仅持 `cap_setuid`/`cap_setgid` 提权后需在 exec 时恢复完整 root 能力以读写 `/etc/shadow`）
- **TargetUser**: 不支持

**实现**: 直接读取 /etc/shadow 解析哈希做密码验证（支持 yescrypt、SHA-512、SHA-256、MD5），验证通过后调用外部命令 `chpasswd`

#### USER_PASSWORD_CHANGE Payload 参数

| 字段名           | 类型       | 必选 | 说明         | 验证规则                                                              |
|:--------------|:---------|:---|:-----------|:------------------------------------------------------------------|
| `username`    | `string` | 是  | 用户名        | 不能为空；禁止控制字符和空白；禁止以 `-` 开头；必须是已存在的普通用户（UID ≥ 1000）；禁止操作 root 或系统用户 |
| `password`    | `string` | 是  | 当前密码用于验证身份 | 不能为空；禁止控制字符                                                       |
| `newPassword` | `string` | 是  | 要设置的新密码    | 禁止控制字符；最大长度 256 字符                                                |

---

### USER_PASSWORD_RESET

**功能**: 管理员直接重置指定用户的密码，无需验证原密码

**权限**:

- **Privilege**: `PrivilegeRoot`（需要以 root 身份调用 chpasswd）
- **DisableSeccomp**: True
- **DisableNoNewPrivs**: True（同 `USER_PASSWORD_CHANGE`）
- **TargetUser**: 不支持

**实现**: 外部命令 `chpasswd`

#### USER_PASSWORD_RESET Payload 参数

| 字段名        | 类型       | 必选 | 说明      | 验证规则                                                              |
|:-----------|:---------|:---|:--------|:------------------------------------------------------------------|
| `username` | `string` | 是  | 目标用户名   | 不能为空；禁止控制字符和空白；禁止以 `-` 开头；必须是已存在的普通用户（UID ≥ 1000）；禁止操作 root 或系统用户 |
| `password` | `string` | 是  | 要设置的新密码 | 禁止控制字符；最大长度 256 字符                                                |

---

### USER_PASSWORD_VERIFY

**功能**: 验证本地用户的密码是否正确（登录校验），无需知道现有哈希格式

**权限**:

- **Privilege**: `PrivilegeRoot`（需要读取 /etc/shadow）
- **DisableSeccomp**: True
- **DisableNoNewPrivs**: True（同 `USER_PASSWORD_CHANGE`）
- **TargetUser**: 不支持

**实现**: 直接读取 /etc/shadow 并解析哈希字段，支持 yescrypt (`$y$`)、SHA-512 (`$6$`)、SHA-256 (`$5$`)、MD5 (`$1$`)
四种哈希算法进行密码验证。对失败响应加入随机延迟（100–199 ms）以防止基于时间的侧信道攻击。同时校验账户锁定、禁用和过期状态。

#### USER_PASSWORD_VERIFY Payload 参数

| 字段名        | 类型       | 必选 | 说明    | 验证规则           |
|:-----------|:---------|:---|:------|:---------------|
| `username` | `string` | 是  | 用户名   | 不能为空；禁止控制字符和空白 |
| `password` | `string` | 是  | 待验证密码 | 不能为空；禁止控制字符    |

---

### FILE_LIST

**功能**: 获取指定目录下的文件和目录列表，返回每个文件的名称、大小、权限模式、修改时间、所有者/组 ID、目录标记、隐藏标记、符号链接目标等完整元数据

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 按批 `File.Readdirnames` 读取文件名。payload `limit` 由调用方传入（Portal 取自已校验的 `file.listMaxEntries`）；dc-exec 要求 `limit > 0`，不在本地夹 settings 上下界。实际最多再读 100 个名字并一并返回，只对将要返回的条目 `Lstat`。读满后目录仍有后续名字时 stdout 带 `"truncated":true`；未截断时省略该字段。

#### FILE_LIST Payload 参数

| 字段名   | 类型       | 必选 | 说明   | 验证规则                                                                                                                                |
|:------|:---------|:---|:-----|:------------------------------------------------------------------------------------------------------------------------------------|
| `dir` | `string` | 是  | 目录路径 | 必须为绝对路径；禁止控制字符（`\r\n\t`）；禁止命中递归黑名单（如 `/etc`、`/root`、`/proc`、`/sys`、`/bin`、`/usr` 等）或精确黑名单（如 `/`、`/home`、`/opt`、`/var`、`/tmp` 根目录本身） |
| `limit` | `int` | 是  | 设置窗口 | 必须 `> 0`（Portal 填入已校验的 `listMaxEntries`）。返回条数最多为该值加 100 |

---

### FILE_READ

**功能**: 读取小文件的内容并以 Base64 编码字符串返回

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.Open` + `io.ReadAll`，限制最大读取 1 MB

#### FILE_READ Payload 参数

| 字段名    | 类型       | 必选 | 说明   | 验证规则                                                         |
|:-------|:---------|:---|:-----|:-------------------------------------------------------------|
| `path` | `string` | 是  | 文件路径 | 必须为绝对路径且通过路径黑名单校验；禁止读取目录（检查 `info.IsDir()`）；文件大小超过 1 MB 返回错误 |

---

### FILE_DELETE

**功能**: 批量删除文件或递归删除目录。当 `files` 数组非空时只删除指定文件；当 `files` 为空时递归删除 `dir` 整个路径

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.RemoveAll`

#### FILE_DELETE Payload 参数

| 字段名     | 类型       | 必选 | 说明                                      | 验证规则                                                                                  |
|:--------|:---------|:---|:----------------------------------------|:--------------------------------------------------------------------------------------|
| `dir`   | `string` | 是  | 基础目录                                    | 必须为绝对路径且通过黑名单校验                                                                       |
| `files` | `array`  | 否  | 文件名列表。若提供则仅删除 `dir` 下的这些文件，不删除 `dir` 本身 | 每个文件名必须是单一干净路径段：禁止绝对路径、禁止 `.`/`..` 或 `../` 前缀穿越、禁止路径分隔符 `/` 或 `\`、禁止控制字符 `\x00\r\n\t` |

---

### FILE_MOVE

**功能**: 移动文件或目录到新位置（可跨目录）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.Rename`

#### FILE_MOVE Payload 参数

| 字段名   | 类型       | 必选 | 说明   | 验证规则            |
|:------|:---------|:---|:-----|:----------------|
| `src` | `string` | 是  | 源路径  | 必须为绝对路径且通过黑名单校验 |
| `dst` | `string` | 是  | 目标路径 | 必须为绝对路径且通过黑名单校验 |

---

### FILE_RENAME

**功能**: 在当前目录下重命名文件或目录

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.Rename`

#### FILE_RENAME Payload 参数

| 字段名    | 类型       | 必选 | 说明                | 验证规则                                                           |
|:-------|:---------|:---|:------------------|:---------------------------------------------------------------|
| `src`  | `string` | 是  | 源文件完整路径           | 必须为绝对路径且通过黑名单校验                                                |
| `name` | `string` | 是  | 新文件名（单一文件名，不含目录段） | 必须是单一干净路径段：禁止绝对路径、禁止 `.`/`..` 或 `../` 前缀穿越、禁止 `/` 或 `\`、禁止控制字符 |

---

### FILE_MKDIR

**功能**: 创建新目录（权限 0755）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.Mkdir`（权限 `0755`）

#### FILE_MKDIR Payload 参数

| 字段名   | 类型       | 必选 | 说明   | 验证规则            |
|:------|:---------|:---|:-----|:----------------|
| `dir` | `string` | 是  | 目录路径 | 必须为绝对路径且通过黑名单校验 |

---

### FILE_MKDIR_ALL

**功能**: 递归创建目录；目标已存在时保持幂等

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.MkdirAll`（权限 `0755`）

#### FILE_MKDIR_ALL Payload 参数

| 字段名   | 类型       | 必选 | 说明   | 验证规则            |
|:------|:---------|:---|:-----|:----------------|
| `dir` | `string` | 是  | 目录路径 | 必须为绝对路径且通过黑名单校验 |

---

### FILE_CHECK

**功能**: 检查文件是否存在并获取其详细元数据，包括解析符号链接后的真实路径

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: Go 内部函数 `os.Stat` + `filepath.EvalSymlinks` + `filepath.Abs`

#### FILE_CHECK Payload 参数

| 字段名    | 类型       | 必选 | 说明   | 验证规则            |
|:-------|:---------|:---|:-----|:----------------|
| `path` | `string` | 是  | 文件路径 | 必须为绝对路径且通过黑名单校验 |

#### 响应数据

成功时返回以下 JSON 结构：

- `size` (int64)：文件大小
- `mode` (string)：Unix 权限模式字符串，如 `-rw-r--r--`
- `realPath` (string)：解析所有符号链接后的绝对路径，供上层进行下载根目录白名单校验
- `modTime` / `modTimeN` (int64)：最后修改时间（秒 + 纳秒）
- `cTime` / `cTimeN` (int64)：状态变更时间（秒 + 纳秒）
- `device` (uint64)：所在设备号
- `inode` (uint64)：inode 编号
- `isDir` (bool)：是否为目录

---

### FILE_WRITE_CHECK

**功能**: 检查当前用户对指定目录是否具有实际写权限，通过创建临时文件探测而非仅检查模式位

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 通过 `os.CreateTemp(dir, ".write_check_*")` 创建临时文件并立即删除，以探测真实写能力（比 Unix 模式位检查更可靠，能正确处理
ACL、只读挂载等场景）

#### FILE_WRITE_CHECK Payload 参数

| 字段名   | 类型       | 必选 | 说明    | 验证规则            |
|:------|:---------|:---|:------|:----------------|
| `dir` | `string` | 是  | 待检查目录 | 必须为绝对路径且通过黑名单校验 |

---

### FILE_UPLOAD

**功能**: 流式上传大文件。dc-exec 先读 stdin 首行 JSON header（含 `dstPath` 和 `size`），随后把剩余原始字节流写入 `dstPath`
所在目录中的临时文件，`chmod 0644` 后再 `os.Rename` 到目标位置，实现原子写入

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelStreaming`（协议在 JSON header 后携带原始文件字节，不经过 json-envelope 编码）

**实现**: dc-exec 读取 stdin 首行 JSON header → 将后续字节写入 `dstPath` 同目录 `.upload_*` 临时文件 →
`os.Chmod(tmpFile, 0644)` → `os.Rename` 到 `dstPath`

#### FILE_UPLOAD Payload 参数

| 字段名       | 类型       | 必选     | 说明      | 验证规则                                                             |
|:----------|:---------|:-------|:--------|:-----------------------------------------------------------------|
| `dstPath` | `string` | 是      | 目标保存路径  | dc-exec 侧执行完整路径校验（绝对路径、控制字符、递归/精确黑名单、符号链接解析）；目标目录必须已存在且当前用户可写 |
| `size`    | `int64`  | 建议显式提供 | 要写入的字节数 | `> 0` 时按精确字节数读取；`< 0` 时持续读取直到 EOF；**省略时 JSON 默认值为 `0`，将写入一个空文件** —— 调用方必须始终显式提供正数 `size` |

---

### FILE_DOWNLOAD

**功能**: 流式下载大文件。dc-exec 读 stdin 首行 JSON header 后直接打开 `srcPath`，把文件内容直接 copy 到 stdout（无 JSON
封装）。此 Action 只负责节点上读文件字节；Portal 下载审批落库表为 `download_requests`（`model.DownloadRequest`），二者勿混淆。

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelStreaming`（响应为原始文件字节，不经过 json-envelope 编码）

**实现**: dc-exec 读取 stdin 首行 JSON header → `os.Open(srcPath)` → `io.Copy` 到 stdout。失败时仅向 stderr 输出 JSON
错误。

#### FILE_DOWNLOAD Payload 参数

| 字段名       | 类型       | 必选     | 说明      | 验证规则                                                 |
|:----------|:---------|:-------|:--------|:-----------------------------------------------------|
| `srcPath` | `string` | 是      | 源文件路径   | dc-exec 侧执行完整路径校验（绝对路径、控制字符、黑名单、符号链接解析）             |
| `size`    | `int64`  | 建议显式提供 | 要输出的字节数 | `>= 0` 时按精确字节数输出（含 `0`=空输出）；`< 0` 时持续输出直到 EOF；省略时 JSON 默认值为 `0` |

---

### TRASH_MOVE

**功能**: 将文件安全移动到符合 XDG Trash 规范的用户回收站（移动原文件 + 生成 `.trashinfo` 元数据文件）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 内置 XDG Trash 逻辑，使用 `Move()` 函数同时移动文件到 `~/.local/share/Trash/files/` 并生成对应的 `.trashinfo`
信息文件到 `~/.local/share/Trash/info/`

#### TRASH_MOVE Payload 参数

| 字段名     | 类型       | 必选 | 说明       | 验证规则                                                                |
|:--------|:---------|:---|:---------|:--------------------------------------------------------------------|
| `dir`   | `string` | 是  | 文件所在目录   | 必须为绝对路径且通过黑名单校验                                                     |
| `files` | `array`  | 是  | 待回收文件名列表 | 每个文件名必须是单一干净路径段：禁止绝对路径、禁止 `.`/`..` 或 `../` 前缀穿越、禁止 `/` 或 `\`、禁止控制字符 |

---

### TRASH_DELETE

**功能**: 永久删除回收站中的指定项目（同时删除 `files/` 和 `info/` 下的对应文件）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 内置 XDG Trash 逻辑，调用 `Delete()` 函数同时删除 `~/.local/share/Trash/files/<trashName>` 和
`~/.local/share/Trash/info/<trashName>.trashinfo`

#### TRASH_DELETE Payload 参数

| 字段名         | 类型       | 必选 | 说明        | 验证规则                                                           |
|:------------|:---------|:---|:----------|:---------------------------------------------------------------|
| `trashName` | `string` | 是  | 回收站内唯一标识名 | 必须是单一干净路径段：禁止绝对路径、禁止 `.`/`..` 或 `../` 前缀穿越、禁止 `/` 或 `\`、禁止控制字符 |

---

### TRASH_DELETE_MANY

**功能**: 批量永久删除回收站中的多个项目

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 内置 XDG Trash 逻辑，调用 `DeleteMany()` 函数批量删除

#### TRASH_DELETE_MANY Payload 参数

| 字段名          | 类型      | 必选 | 说明    | 验证规则                                                             |
|:-------------|:--------|:---|:------|:-----------------------------------------------------------------|
| `trashNames` | `array` | 是  | 标识名列表 | 每项必须是单一干净路径段：禁止绝对路径、禁止 `.`/`..` 或 `../` 前缀穿越、禁止 `/` 或 `\`、禁止控制字符 |

---

### TRASH_RESTORE_MANY

**功能**: 批量将回收站中的多个项目还原回原始路径

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 内置 XDG Trash 逻辑，调用 `RestoreMany()` 函数还原文件或目录

#### TRASH_RESTORE_MANY Payload 参数

| 字段名          | 类型      | 必选 | 说明    | 验证规则                                                             |
|:-------------|:--------|:---|:------|:-----------------------------------------------------------------|
| `trashNames` | `array` | 是  | 待还原的标识名列表 | 每项必须是单一干净路径段：禁止绝对路径、禁止 `.`/`..` 或 `../` 前缀穿越、禁止 `/` 或 `\`、禁止控制字符 |

---

### TRASH_EMPTY

**功能**: 彻底清空当前用户的回收站（删除 `~/.local/share/Trash/{files,info}/*` 所有内容）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 内置 XDG Trash 逻辑，调用 `Empty()` 函数

#### TRASH_EMPTY Payload 参数

本 Action 无 Payload 字段（`ActionTrashEmptyPayload` 为空结构体）。

---

### TRASH_LIST

**功能**: 列出回收站中的文件及其原始删除信息。可选提供 `trashPath` 子目录偏移路径

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 内置 XDG Trash 逻辑，按批读取目录名（条数与 `FILE_LIST` 相同：payload `limit` 再加 100），读满后仍有后续名字则 `truncated` 为 true，再解析 `.trashinfo` 获取原始路径和删除时间

#### TRASH_LIST Payload 参数

| 字段名         | 类型       | 必选 | 说明      | 验证规则                   |
|:------------|:---------|:---|:--------|:-----------------------|
| `trashPath` | `string` | 否  | 子目录偏移路径 | 相对于回收站根目录；不允许绝对路径或路径穿越 |
| `limit` | `int` | 是  | 设置窗口 | 与 `FILE_LIST` 相同：必须 `> 0`；dc-exec 不夹 settings 上下界。返回条数最多为该值加 100 |

---

### JOB_SUBMIT

**功能**: 在目标用户身份下通过预组装的调度器命令提交新任务（如 LSF `bsub`、Slurm `sbatch`、PBS `qsub`）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 以目标用户身份执行 `runCommandWithDir()`，若 payload 中 `command.initScript` 非空则通过 `bash -lc` 执行
`execution.BuildShellCommand(command)`，否则直接执行 `command.program` 及其 `args`。环境变量经 `buildJobEnvs()` 过滤后注入。

#### JOB_SUBMIT Payload 参数

| 字段名        | 类型                  | 必选 | 说明                    | 验证规则                                                                                               |
|:-----------|:--------------------|:---|:----------------------|:---------------------------------------------------------------------------------------------------|
| `command`   | `object`            | 是  | 结构化命令 (`CommandSpec`) | dc-exec 侧必须非空；`Program` 不能为空且不得包含 `\r\n\t`；`Args` 不得包含 `\r\n`；`WorkDir` / `InitScript` 若提供必须为绝对路径。作业名、队列、目录和用户命令都在参数里 |
| `env`       | `map[string]string` | 否  | 注入环境变量到命令进程           | 过滤非法 key（非空、可打印 ASCII、不含 `=`）和危险 key（`LD_PRELOAD`、`LD_AUDIT`、`LD_DEBUG`、`LD_LIBRARY_PATH`）后注入      |
| `envScript` | `string`            | 否  | 执行命令前 source 的脚本       | 非空时通过 `bash -c '. "$0" && exec "$@"'` 执行                                                                 |

---

### JOB_CONTROL

**功能**: 对现有任务执行生命周期控制操作（kill、suspend、resume、requeue 等），通过预组装的调度器命令实现

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 以目标用户身份执行预组装的调度器控制命令（如 LSF `bkill` / `bstop` / `bresume`、Slurm `scancel` / `scontrol`）

#### JOB_CONTROL Payload 参数

| 字段名         | 类型                  | 必选 | 说明                               | 验证规则                                                                                               |
|:------------|:--------------------|:---|:---------------------------------|:---------------------------------------------------------------------------------------------------|
| `command`   | `object`            | 是  | 结构化命令 (`CommandSpec`) | dc-exec 侧必须非空；`Program` 不能为空且不得包含 `\r\n\t`；`Args` 不得包含 `\r\n`；`WorkDir` / `InitScript` 若提供必须为绝对路径。操作、作业号和目标队列都在参数里 |
| `env`       | `map[string]string` | 否  | 环境变量                  | 过滤非法 key 和危险 key（规则同 JOB_SUBMIT）后注入命令环境                                                            |
| `envScript` | `string`            | 否  | 执行命令前 source 的脚本    | 非空时通过 `bash -c '. "$0" && exec "$@"'` 执行                                                                 |

---

### JOB_MODIFY

**功能**: 修改作业属性（队列、名称等），通过预组装的调度器命令实现

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 以目标用户身份执行预组装的调度器修改命令（如 LSF `bmod`、Slurm `scontrol update`、PBS `qalter`）

#### JOB_MODIFY Payload 参数

| 字段名       | 类型        | 必选 | 说明                           | 验证规则                                                                                               |
|:----------|:----------|:---|:-----------------------------|:---------------------------------------------------------------------------------------------------|
| `command`   | `object` | 是  | 结构化命令 (`CommandSpec`) | dc-exec 侧必须非空；`Program` 不能为空且不得包含 `\r\n\t`；`Args` 不得包含 `\r\n`；`WorkDir` / `InitScript` 若提供必须为绝对路径。作业号、队列和名称都在参数里 |
| `envScript` | `string` | 否  | 执行命令前 source 的脚本    | 非空时通过 `bash -c '. "$0" && exec "$@"'` 执行                                                                 |

---

### CONNECTION_CHECK

**功能**: 检查 **VNC** 连接是否仍在运行。通过 `/proc/<pid>` 校验进程是否存在、是否属于当前用户、以及是否为已知 VNC 二进制进程。Portal `POST /api/connections/refresh` 与管理员 cleanup 中的 VNC 对账会调用本动作；**RDP/SSH** 为门户生命周期管理，不经过 `CONNECTION_CHECK`（见 [连接模型 · 状态探测](/docs/development/remote-access/connection-model#状态探测与对账按协议)）。

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`

**实现**: 以目标用户身份通过 `/proc/<pid>/status` 校验所有者和 `/proc/<pid>/comm` 校验是否为 Xvnc / Xtigervnc / Xorg 等已知 VNC 二进制名

#### CONNECTION_CHECK Payload 参数

| 字段名           | 类型      | 必选       | 说明       | 验证规则                                                                                                                                                                                     |
|:--------------|:--------|:---------|:---------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `connections` | `array` | 是（可为空数组） | 待检查的连接列表 | 每个元素包含 `connectionId` (uint)、`username` (string)、`host` (string，拨号目标，由引擎在下发前填入解析后的 dialHost)、`port` (uint)、`display` (*int，nil=无显示号，0 表示 :0)、`protocol` (string)、`pid` (uint)、`name` (string) |

#### 返回值

输出 JSON 结构 `{"connections":[...]}`；每个连接项的 `state` 取值为 `running`、`stopped` 或 `unknown`，并附带 `message` 和
`info` 字段。

---

### VNC_GENERATE_OTP

**功能**: 为运行中的 TurboVNC 会话生成 full-control one-time password，供前端启动 VNC Viewer 使用

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`
- **DisableSeccomp**: True（TurboVNC 外部工具系统调用面宽，无法用 dc-exec 窄 seccomp allowlist 覆盖；NoNewPrivs 生效）

**实现**: 以目标用户身份直接执行 `payload.vncpasswdPath -o -display payload.display`，不经过 shell。执行环境为固定
`PATH`、`LC_ALL=C`，加上降权后的 `HOME`/`USER`/`LOGNAME`（`vncpasswd` 依赖 `HOME` 定位 `~/.Xauthority`）及可选的 `XAUTHORITY`。TurboVNC 可能将 OTP
行输出到 stderr，因此 dc-exec 合并 stdout/stderr 后通过正则提取 `Full control one-time password: <otp>`。错误信息中 OTP
行会被脱敏（替换为 `[redacted]`）。

#### VNC_GENERATE_OTP Payload 参数

| 字段名             | 类型       | 必选 | 说明                        | 验证规则                                                       |
|:----------------|:---------|:---|:--------------------------|:-----------------------------------------------------------|
| `connectionId`  | `uint`   | 是  | 请求 OTP 的 connection ID    | 必须非 `0`                                                    |
| `display`       | `integer`| 是  | TurboVNC 显示号（`0` 表示 `:0`） | 不得为 `null`；≥ 0；dc-exec 规范化为 `:N` 传给 `vncpasswd`             |
| `vncpasswdPath` | `string` | 是  | TurboVNC `vncpasswd` 绝对路径 | 必须是非空绝对路径；不得包含控制字符 `\r\n\t`；由 Server 基于 `binDir` 决议下发 |

#### 返回值

输出 JSON 结构 `{"otp":"22225523"}`；dc-exec 不会将 OTP 记录到日志，失败输出中的 OTP 行会被脱敏处理。

---

### VNC_CONTROL

**功能**: 以目标用户身份启动或停止 VNC 会话（直接调用 `vncserver`），服务于连接规格拉起的 VNC 实例生命周期（Start/Stop/Restart 与删除前清理）

**权限**:

- **Privilege**: `PrivilegeTargetUser`
- **IOModel**: `IOModelJSON`
- **DisableSeccomp**: True（vncserver 外部工具系统调用面宽，无法用 dc-exec 窄 seccomp allowlist 覆盖）
- **DisableNoNewPrivs**: True（桌面会话进程树继承 dc-exec 属性，需保留 sudo/su/passwd 等 setuid 程序；见 security.md §3.1）

**实现**: `start` 时若目标用户 `~/.vnc/passwd` 不存在，先以该用户执行 TurboVNC `vncpasswd -f` 写入随机静态密码（不向调用方返回）；再执行 `<vncserverPath> [serverArgs...] <display>`。`stop` 为 `<vncserverPath> -kill <display>`。除 cgroup 变体外不经过 shell。`vncserverPath` 留空时回退为裸名 `vncserver`（经继承 PATH 解析）。

#### VNC_CONTROL Payload 参数

`VNC_CONTROL` 与 `VNC_CGROUP_CONTROL` 复用同一 `VNCControl` payload：

| 字段名            | 类型       | 必选 | 说明                              | 验证规则                                       |
|:----------------|:---------|:---|:--------------------------------|:-------------------------------------------|
| `connectionId`  | `uint`   | 否  | 关联连接 ID（可为 0，用于占坑拉起）  | 允许为 `0`                                    |
| `display`       | `integer`| 是  | 目标显示号（`0` 表示 `:0`）         | 不得为 `null`；≥ 0                             |
| `operation`     | `string` | 是  | 动作动词                            | 仅接受 `start` / `stop`，其他值报错                 |
| `vncserverPath` | `string` | 否  | `vncserver` 可执行文件路径             | 留空时使用裸名 `vncserver`（PATH 查找）                                 |
| `vncpasswdPath` | `string` | 否  | TurboVNC `vncpasswd` 绝对路径        | 留空且 `vncserverPath` 为绝对路径时，与同目录下 `vncpasswd` 同名推导              |
| `startOptions`  | `string` | 否  | start 时附加给 vncserver 的参数命令行字符串 | `serverArgs` 为空时按空白拆分消费                                      |
| `serverArgs`    | `array`  | 否  | start 时附加给 vncserver 的参数列表       | 优先消费（stop 时忽略）                                               |

#### 返回值

输出 JSON 结构 `VNCControlResult`（`{ "port": 5901, "pid": 12345 }`）；失败时 stderr 带 vncserver 的退出错误输出。

---

### VNC_CGROUP_CONTROL

**功能**: 在 systemd Cgroup slice 隔离下以目标用户身份启动或停止 VNC 会话（特权变体）

**权限**:

- **Privilege**: `PrivilegeRoot`（唯一非密码类的 root 动作）
- **TargetUser**: 必须（要求非 root/nobody 普通用户，系统解析 UID ≥ 1000）
- **IOModel**: `IOModelJSON`
- **DisableSeccomp**: True
- **DisableNoNewPrivs**: True（`systemd-run --scope` 下的桌面会话进程树继承 dc-exec 属性）

**实现**: dc-exec 保持 root 身份，执行
`systemd-run --scope --slice=user-<uid>.slice runuser --login <targetUser> --session-command "<vncserverPath> ..."`。
外层 `systemd-run` 负责在内核中动态创建并挂载 `user-<uid>.slice` 容器，使整棵进程树受 Cgroup 资源配额管辖；
内层 `runuser --login` 负责标准用户环境初始化（切换 UID/GID、自动定位真实 `$HOME`、切换到家目录并完整加载 `~/.bashrc`、`/etc/profile.d` 与 D-Bus session bus），彻底规避纯 `systemd-run --uid` 因缺少 `$HOME` 导致的桌面黑屏与权限错误。
工作目录切换由 `runuser --login` 在建立用户登录上下文后自动完成，外部 `systemd-run` 保持默认工作目录（避免缺乏 `CAP_DAC_OVERRIDE` 时预先 chdir 进 `0700` 私有家目录失败）；所有命令与参数通过 POSIX 单引号转义保护（`quoteShellArg`）防注入，参数拼装与 start/stop 语义同 `VNC_CONTROL`。

#### VNC_CGROUP_CONTROL Payload 参数

复用 [`VNCControl`](#vnc_control) payload。另要求请求级 `targetUser` 必须提供且通过安全校验（非空、非 root/nobody、禁止 `-` 开头或含空字节、系统解析 UID ≥ 1000）。

#### 返回值

输出 JSON 结构 `{}`；失败时 stderr 带 systemd-run/vncserver 的退出错误输出。

---

### SESSION_CONTROL

**功能**: 结束拨号协议（SSH / RDP）的**用户登录会话**，服务于统一 Stop/Delete teardown。**绝不**停止 `sshd` / TermService 等系统守护进程。

**Linux（SSH，dc-exec target user）**:

- **Privilege**: `PrivilegeTargetUser`（与 XRDP/VNC Stop 相同——连接所有者本人可执行）
- **IOModel**: `IOModelJSON`
- **DisableSeccomp**: True
- payload.username 必须等于 targetUser；仅对该用户 `Service` 含 `sshd`/`ssh` 的 loginctl session 调用 `terminate-session`。**禁止** `terminate-user`。

**Windows（RDP，Agent Direct）**:

- 无 dc-exec；Agent 代该用户执行。只 `logoff` **完整桌面** RDP（`rdp-tcp#*` 且 session 内有 `explorer.exe`）；排除 `console` 与 RemoteApp（`rdpshell`/`rdpinit` 无 explorer）。

#### SESSION_CONTROL Payload 参数

| 字段名 | 类型 | 必选 | 说明 |
|:---|:---|:---|:---|
| `connectionId` | `uint` | 是 | 门户连接实例 ID |
| `protocol` | `string` | 是 | `SSH` 或 `RDP` |
| `operation` | `string` | 是 | 仅 `stop` |
| `username` | `string` | 是 | 节点上的登录名（按用户结束该节点上全部对应协议登录会话） |

#### 返回值

输出 JSON：`{"terminated":N}`（结束的 session/进程数；可为 0）。

---

> 以上所有 Operation 的定义基于代码版本 `internal/execution/actions.go` + `actions_linux.go` 中 `Actions` 映射、
`internal/dcexec/dcexec.go` 中 `Handlers` 注册、以及 `internal/dcexec/action/` 下各 handler 的实际校验逻辑自动生成。
