# Agent Context Router

本文件用于声明项目级约束并选择下一步读取的上下文。先定位实现，再按需加载；不通读 `.agents/` 或 `docs-site/docs/`。

## 入口与验证

- 项目约束：遵循下方“项目级约束”；根目录 `AGENTS.md` 保留常驻门禁摘要并指向本文件

**三层门禁（不要混用为「每次都跑全量」）：**

| 场景 | 命令 | 覆盖要点 |
|------|------|----------|
| 日常 Go / proto / 模块变更 | `./.agents/verifiers/verify-changed.sh` | 变更包 gofmt、build、vet、test；安全红线；删行时 deadrefs；`portal/api` 变更时 `.http` **strict**；`cmd/`/`internal/` Go 变更时 Windows 交叉编译。**不跑**全仓 `make test`、`make lint`（staticcheck）、`make vuln`。 |
| 发版、大重构、合并前 | `./.agents/verifiers/verify-all.sh --full` | 上表之外：全量单测、arch、deadrefs、http strict、Proto 扫描、**`make lint vuln`**、依赖 advisory 等。 |
| 变更感知调度 | `./.agents/verifiers/verify-all.sh`（无 `--full`） | 按 diff 类型调用 harness / http / web 单测 / `verify-changed`。**干净工作区直接退出**；仅改 `docs-site/**`（且未动 `test/portal`、`.agents`、`internal/server/portal/web`）时**不执行任何检查**。 |

**其他入口：**

- Harness / Skill / 规则修改后：`.agents/verifiers/verify-harness.sh`（`verify-all` 在检测到 `AGENTS.md` 或 `.agents/` 变更时也会调用）
- Windows 冒烟（需 `WIN_SMOKE_HOST`，人工按需）：`.agents/verifiers/verify-win-smoke.sh`
- 架构边界：`.agents/rules/arch-rules.yaml`（`verify-arch.py`，fail-closed；`python3 .agents/verifiers/verify-arch.py --self-test` 复验正负 fixture）
- 架构依赖按需导出：`python3 .agents/scripts/export_arch_graph.py`（默认 stdout；落盘须显式传入 `--output <path>`）。从当前 `go list` 依赖图实测；包数量与调用行号易过期，不要作为仓库跟踪文件。日常边界门禁仍是 `verify-arch.py`（`arch-rules.yaml`，真实 `go list`）
- Portal 路由与 `.http` 静态对齐：`python3 .agents/scripts/verify_http_tests.py --strict`（`test/portal/**` 或 `portal/api` Go 变更时由门禁触发；**只改 API 文档时须手动跑**，见 `update-project-docs` Skill）

## 项目级约束

- 保护凭证安全；未经用户明确同意，不执行 `sudo`、破坏性数据操作或外部发布。
- 既有对外 API 契约的破坏性变更先与用户协商；内部实现不保留历史兼容层。

## 任务路由

| 任务 / 路径 | 按需读取 | 事实源 / 验证 |
|---|---|---|
| 后端分层；`internal/**` | `.agents/rules/backend-layering-principles.md`；依赖边界读 `arch-rules.yaml` | 源码、`verify-arch.py` |
| Portal API | `.agents/rules/api-development.md`；文档与 `.http` 编写同步读 `.agents/skills/update-project-docs/SKILL.md`；改旧契约读 `cleanup-compliance.md` | API 源码、`docs-site/docs/api/`、`test/portal/*.http`、`.agents/scripts/` API 检查 |
| DB / Model / migration | `.agents/rules/backend-layering-principles.md`；兼容清理读 `cleanup-compliance.md` | 实际 model、DB 与迁移 |
| Settings schema | `.agents/rules/settings-schema-rules.md` | Settings schema 源码与测试 |
| Proto | `.agents/rules/proto-refactoring-rules.md`；按需读 `.agents/skills/purge-proto-baggage/SKILL.md` | `.proto`、生成配置、`make proto`、Proto 扫描 |
| 认证、凭证、特权执行 | `.agents/rules/security-rules.md` | 实现、测试、`verify-security.py` |
| Overview | `.agents/rules/overview-principles.md` | 聚合实现与测试 |
| 推送 / SSE | `.agents/rules/push-principles.md` | 事件、订阅实现与 API 契约 |
| Go 特性 / 数值类型 | `.agents/rules/modern-go-style.md` | `go.mod`、实际类型定义 |
| 重构、删除、改名 | `.agents/rules/cleanup-compliance.md` | 全仓引用、`scan_dead_refs.sh` |
| 测试或构建失败 | `.agents/rules/troubleshooting.md` | 完整日志、失败测试与调用链 |
| API 测试（Portal HTTP 集成，执行/诊断） | `.agents/skills/test-portal-api/SKILL.md` | `make test-api` / `test/portal/run.sh`、`verify_http_tests.py --strict`；**不是** `make test-e2e` / `test/node/`（本地节点与 dc-exec）；实验室场景见 `test/lab/` |
| 更新开发文档 | `.agents/skills/update-project-docs/SKILL.md` | `docs-site/docs/` 与实现 |
| Web 资源审计 | `.agents/skills/web-asset-audit/SKILL.md` | 资源引用与构建入口 |
| 演进审计 | `.agents/skills/codebase-evolution-audit/SKILL.md` | `scan_evolution_debt.py`、`scan_dead_refs.sh`、`verify-arch.py` |
| 架构 / 部署 / 业务背景 | 只读任务相关 `docs-site/docs/` 章节 | 源码、Schema、Proto、配置、构建、CI |

## 路径提示

- `internal/server/portal/api/**`：一起核对 handler、路由、`.http` 测试和 API 文档。
- `internal/db/**`、`internal/model/**`、数据库迁移目录：先确认实际 schema / migration 来源。
- `internal/contract/proto/**`、`internal/server/service/settings/**`：按对应规则及生成/测试链路检查。
- `.agents/**`：只读取正在修改的规则、Skill、脚本或验证器。
- `docs-site/docs/**`：只读取相关章节并与实现核实。
- `test/portal/**`：Portal HTTP 集成；`test/node/**`：本地节点与 dc-exec；`test/lab/**`：实验室场景。三者勿混用。

默认只加载启动约束和任务相关规则/Skill；不预载整棵文档树、源码树、生成物、锁文件、构建目录或数据文件。
