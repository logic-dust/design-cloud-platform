# Launcher Implementation Package

此目录包含 `dc-launcher` 二进制文件的核心实现逻辑。

> 当前 `dc-launcher` 仅支持 Linux。运行环境校验、`/proc` 调用链检查与 seccomp 都依赖 Linux 内核能力。

## 目录结构

```
internal/launcher/
├── launcher.go          # Handler registry
├── action/
│   ├── file.go          # 文件操作
│   ├── job.go           # 作业相关动作
│   ├── user.go          # 用户相关动作
│   └── helper.go        # 共享辅助函数
└── verify/
    ├── environment.go   # L1: 运行环境验证
    ├── token.go         # L2: Token 验证
    └── action.go        # L3: 白名单检查
```

## 核心类型

```go
type Handler func (ctx context.Context, req *execution.ExecuteRequest) (string, error)

var Handlers = map[execution.ActionName]Handler{}
```

## 验证层级

| 层级     | 文件                      | 函数                     | 职责                               |
|--------|-------------------------|------------------------|----------------------------------|
| **L1** | `verify/environment.go` | `RuntimeEnvironment()` | 运行环境验证 (权限/UID)                  |
| **L2** | `verify/token.go`       | `Token()`              | 令牌验证                             |
| **L3** | `verify/action.go`      | `Action()`             | Action白名单验证 (检查 Registry 及 Mode) |

## 执行流程

```
L1 RuntimeEnvironment() → 读请求 → L2 Token() → L3 Action() → dispatch → Handler
```

## 注意事项

- 入口点：`cmd/launcher/main.go`
- Build tags 目前已收敛为 `linux`
- 参数验证是各 Handler 的责任，框架仅做白名单检查

## 设计约束

### 执行超时限制

Launcher 设计用于**快速系统操作**（用户管理、文件操作等），不适合长时间运行的任务。

- **默认超时**：30 秒
- **最大超时**：60 秒（硬编码限制）
- **超时行为**：如果请求的超时 > 60 秒，会**自动截断到 60 秒**（不会报错）
- **设计意图**：
    - 防止资源泄漏和僵尸进程
    - 强制快速响应，提升用户体验
    - 长时间任务应使用 **Scheduler 系统**进行异步执行

**示例**：

- ✅ 适合：修改密码、列出用户、移动文件
- ❌ 不适合：编译代码、数据库备份、大文件处理

### 用户权限模型

- **纯粹的提权工具**：Launcher 仅用于执行 **Agent 自身权限无法完成** 的操作。
    - 需要 Root 权限（如 `chpasswd`）。
    - 需要切换身份（TargetUser）的文件操作。
- **只读操作移交**：所有只读的系统信息获取（如 `USER_LIST`）应由 Agent 直接执行，不经过 Launcher。
- **显式声明**：所有 Action 必须在 Registry 中明确 Mode 属性。
- **UID 限制**：禁止切换到系统用户（UID < 1000，除 root 外）。
- **Root 保护**：Root 执行必须在 Registry 中显式指定 Mode 为 privileged。
