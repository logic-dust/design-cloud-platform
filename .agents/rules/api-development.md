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
- **Launcher 提权**：特权指令下发至 `cmd/launcher`

### 1.3 结构体与参数校验
- Payload/Response 结构体必须显式指定 `json:"..."` 与 `validate:"..."` 标签。
- 分页参数统一使用 `min(maxLimit, max(1, req.Limit))` 进行安全裁剪。

### 1.4 路由设计约束（资源式优先、动作式兜底）

适用于 `internal/server/portal/api/`，避免在文件、调度、连接等"动作密集型"子域硬凹纯 REST。

- 查询只用 `GET`；创建资源用 `POST`；删除单资源用 `DELETE`；修改字段用 `PATCH`/`PUT`。
- 接口本质是"执行动作"（`launch`/`kill`/`refresh`/`sync`/`renew`、批量执行、复杂参数）时，用动作式 `POST`，**动词直接收尾**（不强制 `actions/` 命名空间，参考 GitHub `/pulls/:n/merge` 风格）：
  - 单资源动作：`POST /resources/:id/kill`
  - 集合/批量动作：`POST /resources/batch-delete`、`POST /resources/batch-control`（body 携带 ID 列表）
- 参数放置：path 只放稳定资源标识；query 只用于过滤/分页/排序；body 用于创建、更新、动作与批量参数。文件系统路径、批量 ID 列表、复杂控制参数必须放 body。
- 批量删除禁用 `DELETE /resources` + body 列表（HTTP 对 DELETE body 无语义定义，代理/客户端可能丢弃）；一律用 `POST /resources/batch-delete`。
- 文件子域特殊规则：列表/读取/下载用资源式；重命名/移动/复制/批量删除/移入回收站用动作式（`POST /files/rename` 等），禁止 `PATCH /files/:encodedPath/name?to=...` 这类 URL 承载多路径。
- 评审检查项：这是字段更新还是命令动作？path 是否放了不稳定/超长值？query 是否只承担过滤语义？批量操作是否统一动作路由？与既有子域命名模式是否一致？

## 2. API 变更后的强制同步闭环 (Automated Sync)

当修改或新增 Portal API 路由/Handler 时，**必须自动执行以下步骤**：

### 2.1 运行 API 扫描与文档同步
```bash
python3 .agents/scripts/scan_portal_api.py
```
- 确保 `docs-site/docs/api/_index.md` 包含最新的接口定义、请求参数与权限要求。

### 2.2 运行 HTTP 测试校验与同步
```bash
python3 .agents/scripts/verify_http_tests.py
```
- 检查 `test/api/*.http` 中的测试脚本，为新接口或变更接口补充/.http 模拟测试段落。

### 2.3 Launcher Operation 变更同步
如涉及 Launcher 动作变动，必须执行：
```bash
python3 .agents/scripts/extract_actions.py
```
- 确保 `docs-site/docs/development/launcher/actions.md` 与源码保持同步。

## 3. 验证与门禁
修改完成后，必须运行增量门禁校验：
```bash
./.agents/verifiers/verify-changed.sh
```
