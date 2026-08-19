# VSCode 调试配置指南

本项目包含自动化的调试配置，支持本地调试和远程调试。

## 📋 快速开始

### 本地调试 Engine

- 按 `Ctrl+Shift+D` 打开调试侧栏
- 选择 `Local Engine` 配置
- 按 `F5` 启动调试

## 🌐 远程调试（多种方式）

### 前置条件

1. ✅ **SSH 免密码登录已配置**
    - 远程主机: `cent7.dev.vm`
    - 用户: `hpcadmin`
    - 运行路径: `/share/apps/design-cloud`

2. ✅ **远程 dlv 由脚本自动准备**
   无需远程安装 dlv——`remote-debug.sh` 会自动复用本地静态 dlv 或交叉编译
   一个静态版本上传到远程（版本由 `.vscode/env.sh` 的 `DLV_VERSION` 控制，默认 `v1.27.1`）。

   > **💡 注意**: 本地 不需要安装 dlv，VSCode Go 扩展会自动管理本地调试器

### 📱 5 种调试方式对比

#### 本地调试

| Launch 配置        | 编译上传 | 连接方式 | 耗时  | 用途     | 安全性 |
|------------------|------|------|-----|--------|-----|
| **Local Engine** | -    | 本地启动 | ~1s | 本地开发调试 | -   |

#### 部署到远程并调试（需要编译上传）

| Launch 配置                      | 编译上传 | 连接方式   | 耗时      | 用途             | 安全性 |
|--------------------------------|------|--------|---------|----------------|-----|
| **Remote Engine (Debug & Attach)** | ✅ | 直接连接 | ~20-30s | 一键编译上传 + dlv + 直连附加 | ⭐ |

#### 连接到已运行的远程调试器（无需编译上传）

| Launch 配置                            | 编译上传 | 连接方式 | 耗时  | 用途                 | 安全性 |
|--------------------------------------|------|------|-----|--------------------|-----|
| **Connect Direct Engine**            | ✗    | 直接连接 | ~1s | 直接连接已运行的 dlv（内网） | ⭐   |
| **Connect Tunnel Engine**            | ✗    | SSH 隧道  | ~3s | 自动建隧道后连接（127.0.0.1） | ⭐⭐⭐ |

### 使用场景

#### 🆕 部署到远程并调试（编译 → 上传 → 启动 → 连接）

选择基于你的网络环境：

**方式 A：内网直连（推荐）**

**操作**：

1. 按 `Ctrl+Shift+D` 打开调试侧栏
2. 选择 `Remote Engine (Debug & Attach)` 配置
3. 按 `F5` 启动

**自动执行**：

- ✅ 编译 engine（禁用优化便于调试）
- ✅ 上传到远程主机 `/share/apps/design-cloud/design-cloud-engine`
- ✅ 停止旧进程，启动新 dlv（headless 模式，端口 2345）
- ✅ VSCode 直连远程 dlv（`${config:remoteDebug.host}:2345`，即 `cent7.dev.vm:2345`）

**耗时**：~20-30 秒
**安全性**：⭐（内网直连，无加密）

**方式 B：内网直连**

内网环境想直连完整调试时：先运行 `remote-debug` 任务（编译上传并启动 dlv），
再选择 `Connect Direct Engine` 配置按 `F5` 附加到 `cent7.dev.vm:2345`。

---

#### 🚀 连接到已运行的远程调试器（无需编译上传）

**前提**：remote engine 已经在运行（之前启动过），且二进制没有改动

**场景**：

- 调试中断了，想重新连接
- 修改了 VSCode 的调试配置（断点、观看变量等），需要重连
- engine 代码未变，只想改变连接方式

**操作**：

1. 按 `Ctrl+Shift+D` 打开调试侧栏
2. 选择 `Connect Direct Engine` 配置
3. 按 `F5` 启动

**自动执行**：

- ✅ 直接连接 `cent7.dev.vm:2345`（无任何前置任务，不重新编译/上传）

**耗时**：~1 秒（最快）
**安全性**：⭐（无加密，仅适合可信内网）

> **💡 隧道场景**：若远程 dlv 只在内网监听或需加密传输，直接选择
> `Connect Tunnel Engine` 配置按 `F5`——它会自动建立/复用 SSH 隧道并连接 `127.0.0.1:2345`。

**⚠️ 重要**：如果修改了 engine 代码，必须用 `Remote Engine (Debug & Attach)` 重新编译上传！

---

#### ⚡ 直接连接（最快，内网环境）

**前提**：remote engine 已经在运行，网络环保直通 dlv 端口

**操作**：

1. 按 `Ctrl+Shift+D` 打开调试侧栏
2. 选择 `Connect Direct Engine` 配置
3. 按 `F5` 启动

**直接连接**：

- ✅ 无需 SSH 隧道，直接连接 `cent7.dev.vm:2345`
- ✅ 无任何预启动任务

**耗时**：~1 秒（最快）

**优点**：最快速、最简单
**缺点**：不安全（无加密认证），仅适合内网可信环境

---

### 调试过程中

- **设置断点**: 直接在代码左边点击
- **查看变量**: 鼠标悬停或在 Watch 面板添加
- **单步执行**: F10（不进入函数）或 F11（进入函数）
- **停止调试**: Shift+F5

### 调试完成后

按 `Shift+F5` 会自动：

- ✅ 停止 VSCode 调试连接
- ✅ 关闭 SSH 隧道（如果使用了隧道方式）
- ⚠️ **注意**：remote engine 进程继续运行（如果用的是非完整部署方式）

## 🛠 自动化脚本详解

### 脚本架构

**职责分离设计**：

| 脚本                      | 功能                             | 步数  | 用于                  |
|-------------------------|--------------------------------|-----|---------------------|
| **remote-debug.sh**     | 本地编译 → 推送 → 启动 dlv        | 5 步 | 启动远程调试器（不建立隧道）  |
| **remote-run.sh**       | 本地编译 → 推送 → 直接运行 engine | 5 步 | 生产模式运行（不带 dlv）  |
| **remote-build-debug.sh**| sync → 远程编译 → 安装 → 启动 dlv | 6 步 | 在远程主机上编译并调试     |
| **create-tunnel.sh**    | 建立 SSH 隧道                    | 1 步 | 建立加密隧道连接         |
| **cleanup-remote.sh**   | 清理本地隧道 + 远程 dlv/engine   | 3 步 | 完整调试环境清理        |
| **cleanup-tunnel.sh**   | 仅清理本地 SSH 隧道              | 1 步 | 快速断开隧道            |
| **check-status.sh**     | 检查隧道/dlv/端口监听状态        | 3 步 | 排障诊断                |

### 复合任务

VSCode 支持 `dependsOn` 来组合多个任务的执行顺序：

| 任务                     | 依赖关系 | 功能                                |
|------------------------|--------|-----------------------------------|
| **remote-debug**       | 无     | 本地编译 → 推送 → 启动 dlv           |
| **remote-run**         | 无     | 本地编译 → 推送 → 直接运行 engine    |
| **remote-build-debug** | 无     | sync → 远程编译 → 安装 → 启动 dlv（完整流程） |
| **create-tunnel**      | 无     | 建立 SSH 隧道                       |

### 执行流程

#### 1️⃣ Remote Engine (Debug & Attach) - 一键完整远程调试

```
preLaunchTask: "remote-build-debug"
↓
执行任务: remote-build-debug
  [1/5] 编译 engine
  [2/5] 清理旧进程
  [3/5] 上传二进制
  [4/5] 上传配置
  [5/5] 启动 dlv
↓
VSCode 直连 cent7.dev.vm:2345（不再依赖隧道）
```

#### 2️⃣ Connect Direct Engine - 最快重连

```
无 preLaunchTask
↓
VSCode 直接连接到 cent7.dev.vm:2345
```

> 内网直连完整调试：先运行 `remote-debug` 任务（编译→上传→启动 dlv，无隧道），
> 再选择本配置按 `F5` 附加。

#### 2️⃣' Connect Tunnel Engine - 隧道重连（自动建隧道）

```
preLaunchTask: "create-tunnel"
↓
自动执行 create-tunnel 脚本（若隧道已存在则直接复用，不重建）
↓
VSCode 通过隧道连接 127.0.0.1:2345
```

> 需要加密隧道或远程 dlv 仅限内网监听时使用；脚本会自动检测已有隧道并复用。

### remote-debug.sh 工作流程

调用方式：

```bash
bash .vscode/remote-debug.sh
```

> 说明：`remote-debug.sh` 只负责「编译 → 上传 → 启动 dlv」，不含 SSH 隧道。
> 需要隧道时请单独运行 `create-tunnel.sh`。

**工作流程**（5 步）

| 步骤 | 操作        | 说明                                                                |
|----|-----------|-------------------------------------------------------------------|
| 1  | 编译 engine | `make engine-dev ENGINE_OUT=$LOCAL_BINARY ENGINE_GC_FLAGS="all=-N -l"`（脚本自动调用，版本注入由 Makefile 统一处理） |
| 2  | 停止旧进程     | 检查并 kill 旧进程，防止二进制文件被占用                                           |
| 3  | 上传二进制     | scp 上传编译后的 engine 到远程                                             |
| 4  | 上传配置文件    | 上传 config.dev.toml（可选）                                            |
| 5  | 启动 dlv    | dlv 监听 0.0.0.0:2345（供 VSCode 连接）；engine 接收启动参数                    |

**总耗时**：~15-20 秒

---

### create-tunnel.sh 工作流程

| 步骤 | 操作     | 说明                                                   |
|----|--------|------------------------------------------------------|
| 1  | 检查 dlv | 确认远程 dlv 进程正在运行                                      |
| 2  | 清理旧隧道  | 停止已有的 SSH 隧道进程                                       |
| 3  | 建立新隧道  | `ssh -n -f -L 2345:127.0.0.1:2345 -N "$REMOTE_HOST"` |
| 4  | 验证隧道   | 通过 `ss -tuln` 确认本地端口监听                               |

**总耗时**：~2-3 秒

---

### 关键理解：dlv 与 engine 的参数

脚本在 Step 5 执行的完整命令是：

```bash
dlv exec /share/apps/design-cloud/design-cloud-engine \
  --listen=0.0.0.0:2345 \           # ← dlv 的参数（监听端口）
  --headless=true \                  # ← dlv 的参数
  --api-version=2 \                  # ← dlv 的参数
  --accept-multiclient \             # ← dlv 的参数
  -- \                               # ← 分隔符（重要！之后是 engine 的参数）
  --config config.dev.toml           # ← engine 的参数
```

**所以**：

- `2345` 是 **dlv 的监听端口**（VSCode 通过这个端口连接调试器）
- `--config config.dev.toml` 是 **engine 程序的启动参数**
- 修改 `REMOTE_ENGINE_ARGS` 来改变 engine 的参数。engine 顶层只接受 `--config`：
  ```bash
  REMOTE_ENGINE_ARGS="--config config.dev.toml"
  ```
- 日志级别等运行参数**不再有命令行开关**，改由配置文件控制（`[log] app_level`）

### 手动运行脚本

如果需要手动调用：

```bash
bash .vscode/remote-debug.sh
```

### 日志查看

远程 dlv 运行日志保存在远程主机的 `/tmp/dlv-debug.log`：

```bash
ssh hpcadmin@cent7.dev.vm tail -f /tmp/dlv-debug.log
```

## 📝 tasks.json 中的其他任务

| 任务名                      | 说明                 |
|--------------------------|--------------------|
| `go: build - engine-dev` | 本地编译 engine（开发模式）  |
| `go: test all`           | 运行所有测试             |
| `go: run engine`         | 本地不调试直接运行 engine   |
| `proto: generate`        | 生成 protobuf 文件     |
| `remote-debug`           | 编译→上传→启动 dlv（自动运行） |

**运行任务**: 按 `Ctrl+Shift+P` → `Tasks: Run Task`

## ⚙️ 配置说明

### launch.json

**1. Local Engine** - 本地调试

```json
{
    "name": "Local Engine",
    "type": "go",
    "request": "launch",
    "mode": "debug",
    "program": "${workspaceFolder}/cmd/engine"
}
```

#### 部署到远程并调试

**2. Remote Engine (Debug & Attach)** - 编译→上传→启动 dlv→隧道→附加

```json
{
    "name": "Remote Engine (Debug & Attach)",
    "type": "go",
    "request": "attach",
    "mode": "remote",
    "port": 2345,
    "host": "${config:remoteDebug.host}",
    "showLog": true,
    "trace": "verbose",
    "logOutput": "rpc",
    "preLaunchTask": "remote-build-debug"
}
```

**说明**：

- 完整自动化部署，一键完成编译 → 上传 → 启动 dlv → 直连附加
- `preLaunchTask: "remote-build-debug"` 自动执行：
    sync 代码 → 远程编译 → 安装 → 启动 dlv
- `host` 来自 `.vscode/settings.json` 的 `remoteDebug.host`（默认 `cent7.dev.vm`），直连远程 dlv
- 耗时 ~20-30 秒
- 如需隧道：先运行 `create-tunnel` 任务，并把 `host` 改为 `127.0.0.1`

---

#### 连接到已运行的调试器

**3. Connect Direct Engine** - 直接连接已运行的调试器

```json
{
    "name": "Connect Direct Engine",
    "type": "go",
    "request": "attach",
    "mode": "remote",
    "port": 2345,
    "host": "${config:remoteDebug.host}"
}
```

**说明**：

- 最快，适合内网可信环境
- 无 `preLaunchTask`，不重新编译/上传，直接附加到已运行的 dlv
- `host` 来自 `.vscode/settings.json` 的 `remoteDebug.host`（默认 `cent7.dev.vm`）
- ⚠️ engine 代码有改动时必须用 `Remote Engine (Debug & Attach)` 重新编译上传
- 无 `preLaunchTask`，直接连接远程 dlv（无隧道）
- 用于 engine 已运行，需要快速重连
- 耗时 ~1 秒
- ⚠️ 无加密认证，仅用于可信网络

---

**3' Connect Tunnel Engine** - 隧道连接已运行的调试器（自动建隧道）

```json
{
    "name": "Connect Tunnel Engine",
    "type": "go",
    "request": "attach",
    "mode": "remote",
    "port": 2345,
    "host": "127.0.0.1",
    "preLaunchTask": "create-tunnel"
}
```

**说明**：

- `preLaunchTask: "create-tunnel"` 自动执行隧道脚本：先检查本地隧道是否已存在，
  已存在则直接复用，否则建立 `localhost:2345 → 远程 127.0.0.1:2345`
- 连接地址 `127.0.0.1`（走加密 SSH 隧道）
- 用于远程 dlv 仅限内网监听或需要加密传输的场景
- 耗时 ~2-3 秒（隧道已存在时 ~1 秒）
- 清理隧道：`cleanup: local tunnels` 任务或按 Shift+F5

---

**Test** - 单元测试

```json
{
    "name": "Test",
    "type": "go",
    "request": "launch",
    "mode": "test",
    "program": "${fileDirname}",
    "args": ["-test.v", "-test.run", ".*"]
}
```

## 🗺️ 源码路径映射（-trimpath）

### 为什么需要路径映射？

本项目编译配置使用 `-trimpath` 标志来移除二进制中的完整路径前缀：

**Build 命令**（脚本复用 Makefile 的 `engine-dev` 目标）：

```bash
make engine-dev ENGINE_OUT=./engine ENGINE_GC_FLAGS="all=-N -l"
```

等价于 `go build -trimpath -tags=dev -gcflags="all=-N -l" -o ./engine ./cmd/engine`（版本信息由 Makefile 注入）。

**效果**：

- ✅ 二进制更小（移除完整编译路径）
- ✅ 更安全（不暴露本地文件系统结构）
- ✅ 跨平台一致性好（开源项目标准做法）

### 路径映射配置

由于 `-trimpath` 将源码路径标准化为 `design-cloud-engine/...` 格式（使用 go.mod 模块名），VSCode 需要映射来找到实际的源文件。

**已配置的映射** (`launch.json` 中所有 Remote 配置）：

```json
"substitutePath": [
  {
    "from": "design-cloud-engine",
    "to": "${workspaceFolder}"
  }
]
```

**工作原理**：

- 二进制中的源码路径：`design-cloud-engine/cmd/engine/main.go`
- 本地文件路径：`${workspaceFolder}/cmd/engine/main.go`
- VSCode 自动转换路径，从而找到源文件设置断点

### 如果需要修改模块名

1. 找到 `go.mod` 第一行的 `module` 声明
2. 更新 `launch.json` 中所有 Remote 配置的 `substitutePath[0].from` 值

**示例**：若改成 `module my-new-engine`

```json
"substitutePath": [
  {
    "from": "my-new-engine",
    "to": "${workspaceFolder}"
  }
]
```

### 修改远程主机配置

所有配置变量统一存放在 `.vscode/env.sh` 中，两个脚本都会自动 source 这个文件。

编辑 `.vscode/env.sh` 中的配置：

```bash
# SSH & 远程主机配置
export REMOTE_USER="hpcadmin"                    # SSH 远程用户名
export REMOTE_HOST="cent7.dev.vm"                # SSH 远程主机地址
export SSH_PORT=22                               # SSH 端口（默认 22）
export REMOTE_PATH="/share/apps/design-cloud"   # 远程运行工作目录

# 二进制文件路径
export LOCAL_BINARY="./build/engine-debug"      # 本地编译输出
export REMOTE_BINARY="$REMOTE_PATH/design-cloud-engine"  # 远程上传位置
export LOCAL_CONFIG="${PWD}/config.dev.toml"    # 本地配置文件

# 远程配置文件（完整路径）
export REMOTE_CONFIG_FILE="$REMOTE_PATH/config.dev.toml"  # 远程上传目标路径

# 调试端口配置
export LOCAL_PORT=2345         # 本地 VSCode 连接的端口
export REMOTE_DEBUG_PORT=2345  # 远程 dlv 监听的端口

# 远程工具
export REMOTE_DLV_CMD="$REMOTE_PATH/dlv"  # 上传到远程工作目录的静态 dlv（脚本自动交叉编译并上传，无需远程装 Go）
export REMOTE_ENGINE_ARGS="--config $REMOTE_CONFIG_FILE"  # engine 启动参数

# Build 配置
# 编译统一走 Makefile 的 engine-dev 目标（.vscode/common.sh 调用）：
#   make -C .. ENGINE_OUT=$LOCAL_BINARY ENGINE_GC_FLAGS="all=-N -l" engine-dev  # 调试编译
#   make -C .. ENGINE_OUT=$LOCAL_BINARY engine-dev                              # 优化编译
# 编译标志定义在 Makefile，变量：ENGINE_OUT / ENGINE_GC_FLAGS / ENGINE_EXTRA_FLAGS
```

**参数说明**：

- `REMOTE_USER`: SSH 远程用户名
- `REMOTE_HOST`: SSH 远程主机地址（不包含用户名）
- `SSH_PORT`: SSH 连接端口（默认 22）
- `REMOTE_PATH`: 远程工作目录（engine、config、dlv 都在这里）
- `REMOTE_CONFIG_FILE`: 配置文件的完整路径（在远程主机上）
- `REMOTE_ENGINE_ARGS`: engine 程序的启动参数。engine 顶层**仅**支持 `--config`：
    - `--config $REMOTE_CONFIG_FILE`
    - 日志级别、监听端口等运行参数由配置文件控制（如 `[log] app_level`、`[portal] port`），
      该调试启动行只传 `--config`
- `ENGINE_OUT`: 编译输出路径（默认 `build/design-cloud-engine-debug`，脚本传 `$LOCAL_BINARY`）
- `ENGINE_GC_FLAGS`: `-gcflags` 值，默认 `all=-N -l`（禁优化）；置空即优化编译
- `ENGINE_EXTRA_FLAGS`: 追加任意 go build 标志（如 `-race`）

**重要**：若修改了 `LOCAL_PORT`，还需要更新 `launch.json` 中对应的 `port` 字段：

```json
{
    "name": "Remote Engine",
    "port": 2345  // 改成对应的 LOCAL_PORT 值
}
```

## 🐛 常见问题

### Q: 本地需要安装 dlv 吗？

**A**:
❌ **不需要**。VSCode Go 扩展会自动下载和管理本地 dlv。
✅ **仅需远程主机安装** dlv（用于在远程启动调试器）

### Q: 提示 "ssh: command not found" 或连接失败

**A**:

1. 检查 SSH 免密码登录是否工作: `ssh -p $SSH_PORT $REMOTE_USER@$REMOTE_HOST "echo OK"`
    - 或直接用具体值: `ssh -p 22 hpcadmin@cent7.dev.vm "echo OK"`
2. 确认公钥已添加到远程主机
3. 检查 `env.sh` 中的 `REMOTE_USER`、`REMOTE_HOST` 和 `SSH_PORT` 配置是否正确

### Q: 远程编译失败

**A**:

1. 检查远程主机是否有 Go 环境
2. 查看完整错误信息，脚本会显示失败原因

### Q: 无法连接到调试器

**A**:

1. 检查隧道是否建立: `netstat -tuln | grep 2345`
2. 查看远程 dlv 日志: `ssh -p $SSH_PORT $REMOTE_USER@$REMOTE_HOST tail -100 /tmp/dlv-debug.log`
    - 或直接用具体值: `ssh -p 22 hpcadmin@cent7.dev.vm tail -100 /tmp/dlv-debug.log`
3. 检查防火墙是否阻止 2345 端口

### Q: 断点不生效

**A**:

1. 确认代码是用当前编译的二进制运行的
2. 脚本每次都会重新编译，确保调试符号存在

### Q: Go 编译报错 "invalid value \"all=-N" for flag -gcflags"

**A**:
这是 shell 引号和单词分割问题。现在编译已统一走 Makefile，`-gcflags=all=-N -l` 的引号由 Makefile 处理，不会再出现该错误。若仍需手写 go build，注意必须整体引用：

```bash
go build -trimpath -tags=dev -gcflags="all=-N -l" -o engine ./cmd/engine
```

### Q: 想要修改编译选项

**A**: 编辑 `Makefile` 中 `engine-dev` 目标旁的变量，例如：

```make
ENGINE_OUT         ?= build/design-cloud-engine-dev  # 输出路径
ENGINE_GC_FLAGS    ?=                                 # 传 all=-N -l 开启调试（禁优化）
ENGINE_EXTRA_FLAGS ?=                                 # 追加标志，如 "-race"
```

**参考**：

- `-tags=dev` - 启用开发相关的 debug 功能
- `-gcflags="all=-N -l"` - 禁用编译优化和内联，便于调试
- `-trimpath` - 移除二进制中的路径前缀（与 substitutePath 配合）

### Q: 远程主机路径不同

**A**: 编辑 `.vscode/env.sh` 中的配置变量即可

## 📚 相关文档

- [Delve 调试器文档](https://github.com/go-delve/delve/tree/master/Documentation)
- [Go 调试指南](https://golang.org/doc/debugging_go_programs)
- [VSCode Go 扩展](https://github.com/golang/vscode-go)

---

**更新于**: 2026-08-04
