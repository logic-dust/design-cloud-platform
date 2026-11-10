# API 开发与文档/测试同步规范 (API Development & Sync Rules)

针对 Portal API 的新增、修改与维护，必须遵循本规范完成编码、路由注册、文档更新与测试同步的完整闭环。

既有对外 API 契约的破坏性修改或删除，实施前必须与用户讨论并达成共识；新增端点或可选字段按对应消费者和测试闭环验证。

## 1. API 开发规范

### 1.1 权限与 Policy 模型 (Echo v5)
- **Public 接口**：`PolicyGuest`（无需鉴权）
- **认证接口**：`PolicyUser`（普通用户，通过 `middleware.CurrentUsername(c)` 获取用户）
- **管理接口**：Security Admin / System Admin / Audit Admin 对应作用域与 Scope 检查。

### 1.2 架构职责划分
- **Service 层**：业务逻辑、数据持久化与策略控制 (`internal/server/service/`)
- **Agent 分发**：通过 `DispatchService` 进行 Agent 视角操作
- **dc-exec 提权**：特权指令下发至 `cmd/exec`

### 1.3 结构体与参数校验
- Payload/Response 结构体必须显式指定 `json:"..."` 与 `validate:"..."` 标签。
- 引擎定义的 Portal JSON 字段统一 **camelCase**；不为同一语义提供 snake_case 别名（发版前零历史兼容，见 [`cleanup-compliance.md`](cleanup-compliance.md) §0）。
- **调度器作业**（`Jobs` / `Fields`）透传调度器原生键名，禁止在 handler 或 driver 中对作业 map 做 key 归一化。
- 分页参数：Portal 列表 limit/offset 在 `httputil` 统一三种解析：`ParsePage`（Clamp，多数 Handler）、`ParseTicketPage`（Strict422，工单）、`ParseOptionalListPage`（Optional，审计 executions）；数字上下界单源 `db.DefaultListLimit` / `MaxListLimit`，`httputil.DefaultPageLimit` 为别名。

### 1.4 路由设计约束（资源式优先、动作式兜底）

适用于 `internal/server/portal/api/`，避免在文件、调度、连接等"动作密集型"子域硬凹纯 REST。

- 查询只用 `GET`；创建资源用 `POST`；删除单资源用 `DELETE`；修改字段用 `PATCH`/`PUT`。
- 接口本质是"执行动作"（`launch`/`kill`/`refresh`/`sync`/`renew`、批量执行、复杂参数）时，用动作式 `POST`，**动词直接收尾**（不强制 `actions/` 命名空间，参考 GitHub `/pulls/:n/merge` 风格）：
  - 单资源动作：`POST /resources/:id/kill`
  - 集合/批量动作：`POST /resources/batch-delete`、`POST /resources/batch-control`（body 携带 ID 列表）
- 参数放置：path 只放稳定资源标识；query 只用于过滤/分页/排序；body 用于创建、更新、动作与批量参数。批量 ID 列表、复杂控制参数必须放 body。
- **文件路径例外**：文件位置由稳定 `:name` 标识；目录/回收站的读取路径与流式上传目标路径使用 `contents/*` wildcard 表示相对路径，作为文件资源定位的一部分。不得放绝对路径。重命名、删除、恢复、跨路径操作等携带一个或多个路径的动作参数放 body；不要把路径塞进 query。
- 批量删除禁用 `DELETE /resources` + body 列表（HTTP 对 DELETE body 无语义定义，代理/客户端可能丢弃）；一律用 `POST /resources/batch-delete`。
- 文件子域特殊规则：目录/回收站列表与上传以 wildcard 路径定位文件资源；下载的源路径及写操作的复杂路径参数放 body。重命名、永久批量删除、移入回收站、回收站还原等用动作式（如 `POST .../contents/rename`、`POST .../trash/items/restore`）。跨目录 copy/move 当前未实现，勿在 path/query 承载多路径（禁止 `PATCH /files/:encodedPath/name?to=...`）。
- 评审检查项：这是字段更新还是命令动作？path 是否放了不稳定/超长值？query 是否只承担过滤语义？批量操作是否统一动作路由？与既有子域命名模式是否一致？

## 2. API 变更后的同步要求

修改 Portal API 路由或 Handler 后，运行端点提取器并据此人工同步 API 文档。提取器只输出摘要，不会修改或校验 Markdown 文档；文档结构与事实源见 .agents/skills/update-project-docs/SKILL.md。

### 2.1 核对 API 文档

    python3 .agents/scripts/scan_portal_api.py

根据输出和 Handler 契约，更新 docs-site/docs/api/user.md、admin.md 或 _index.md。

### 2.2 更新并严格校验 HTTP 测试

    python3 .agents/scripts/verify_http_tests.py --strict

该校验器检查 .http 路由覆盖及 Scope/领域放置；增量门禁会在 API Go 文件变更时自动运行严格模式。

### 2.3 dc-exec Operation 文档

如涉及 dc-exec Action变动，运行提取器并据此人工同步文档：

    python3 .agents/scripts/extract_actions.py

提取器只输出 Action 与权限摘要，不会修改或校验 docs-site/docs/architecture/dcexec/actions.md。文档步骤见 .agents/skills/update-project-docs/SKILL.md。

## 3. 验证与门禁
修改完成后，必须运行增量门禁校验：
```bash
./.agents/verifiers/verify-changed.sh
```
