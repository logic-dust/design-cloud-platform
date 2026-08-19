# 旧代码清理约束 (Cleanup Compliance)

> 对应 `.agents/README.md` 第三法则的 **可执行细则**。
> 目的：明确对外契约与内部实现的兼容边界，根除内部历史包袱。

## 0. 兼容性分界原则 (API vs. Internal)

- **对外契约（Portal REST API / WebSocket）**：纯新增端点或可选字段属于安全演进；破坏性修改/删除既有契约前必须与用户协商，达成共识后更新所有消费者、文档与 `.http` 测试。细节见 [`api-development.md`](api-development.md)。
- **内部一切实现（Service / DB Model / SQL / Launcher / Agent / Proto）**：**坚决执行零历史兼容**！
  - 严禁引入兼容别名（如 `type Old = New`、`var ErrOld = ErrNew`）；
  - 严禁编写 fallback 兜底分支或多版本嗅探；
  - 严禁保留已下线字段的废弃列映射；
  - 历史行为完全由 Git 追溯，重构即全量对齐最新设计。

## 1. 迁移必须删除旧实现

功能从 A 迁移到 B 时， **旧实现必须在同一 commit 或紧随的 follow-up 中删除**。不允许出现"新旧共存"状态超过 2 个 commit。

```go
// ❌ 错误：只迁不删
func newFoo() { ... }  // 新
func oldFoo() { ... }  // ← 未删除，变成死代码

// ✅ 正确：迁移即删除
func newFoo() { ... }
// oldFoo 已删除
```

**例外**：用户明确要求保留旧实现且设定了删除日期（在 commit message 中标注 `compat-until: YYYY-MM-DD`）。

## 2. 路由变更必须同步测试文件

任何路由路径、方法、参数的变更，必须 **立即**更新 `test/api/*.http` 中的对应请求。

```go
// api.go — 修改路由
g.PUT("/users/:user/role", ...)

// test/api/05-access-securityadmin.http — 同步修改  
PUT {{addr}}/api/admin/security/users/{{username}}/role
```

验证方式：在该 commit 之前，`git diff --stat` 中应同时出现 api.go 和一个 .http 文件；若缺少 .http 变更，说明测试文件未同步，是违反约束。

## 3. 禁止保留"兼容"别名

重构后，旧函数名、旧变量名、旧错误变量、旧类型名 **不能以别名形式保留**。

```go
// ❌ 错误
var ErrOldName = errors.New("...")  // 旧定义，还给新名当别名
var NewName = ErrOldName

// ❌ 错误  
type OldType = NewType  // 留着只为了让旧 import 能编译

// ✅ 正确：直接删除旧定义，更新所有引用到新的
```

旧实现可通过 `git log` 追溯，不需要在代码里留别名。

## 4. 中间件迁移不保留旧分支

中间件从 "运行时 if/else" 重构为 "启动时生成" 后，旧的分支代码必须删除，不能以注释掉的代码、fallback 或 `// TODO: remove`
等形式残留。

```go
// ❌ 错误：注释掉的旧逻辑 + TODO 标记
// TODO: remove this after migration
// func oldMiddleware() { ... }

// ✅ 正确：直接删除，不留痕迹
```

## 5. 重构后扫描死引用

包级重构（函数签名变化、类型替换、错误变量移动）后，在提交前运行：

```bash
# 检查被删除符号是否还有残留引用
grep -rn "<旧符号名>" --include='*.go' internal/ | grep -v _test.go

# 或者用 Go 的 compile-time deadcode detection
go build -C ./cmd/engine -o /dev/null ./...
```

如果有残留引用但编译通过，说明存在别名或兼容 stub，违反约束第 3 条。

## 6. 强制阈值

| 指标             | 允许     | 禁止                            |
|------------------|----------|---------------------------------|
| 新旧路由共存     | 0 个端点 | 任何新旧端点同时注册            |
| 死错误变量       | 0 个     | 任何未被引用的导出错误变量      |
| 兼容别名         | 0 个     | 任何类型/函数/变量别名          |
| 未同步的测试文件 | 0 个     | 路由路径变更后 .http 测试未更新 |
| 未 Tidy 的残留依赖 | 0 个     | 任何无代码引用的残留依赖或缺失依赖 (go mod tidy) |
