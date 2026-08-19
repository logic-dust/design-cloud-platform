# Agent Context Router

本文件仅用于选择下一步读取的上下文。先定位实现，再按需加载；不通读 `.agents/` 或 `docs-site/docs/`。

## 入口与验证

- 全局约束：根目录 `AGENTS.md`
- 机器校验的架构边界：`.agents/rules/arch-rules.yaml`（`verify-arch.py`）
- Go 增量门禁：`.agents/verifiers/verify-changed.sh`
- Harness 修改后：`.agents/verifiers/verify-harness.sh`

## 任务路由

| 任务 / 路径 | 按需读取 | 事实源 / 验证 |
|---|---|---|
| 后端分层；`internal/**` | `.agents/rules/backend-layering-principles.md`；依赖边界读 `arch-rules.yaml` | 源码、`verify-arch.py` |
| Portal API | `.agents/rules/api-development.md`；改旧契约读 `cleanup-compliance.md` | API 源码、`docs-site/docs/api/`、`test/api/*.http`、`.agents/scripts/` API 检查 |
| DB / Model / migration | `.agents/rules/backend-layering-principles.md`；兼容清理读 `cleanup-compliance.md` | 实际 model、DB 与迁移 |
| Settings schema | `.agents/rules/settings-schema-rules.md` | Settings schema 源码与测试 |
| Proto | `.agents/rules/proto-refactoring-rules.md`；按需读 `.agents/skills/purge-proto-baggage/SKILL.md` | `.proto`、生成配置、`make proto`、Proto 扫描 |
| 认证、凭证、特权执行 | `.agents/rules/security-rules.md` | 实现、测试、`verify-security.py` |
| Dashboard | `.agents/rules/dashboard-principles.md` | 聚合实现与测试 |
| 推送 / SSE | `.agents/rules/push-principles.md` | 事件、订阅实现与 API 契约 |
| Go 特性 / 数值类型 | `.agents/rules/modern-go-style.md` | `go.mod`、实际类型定义 |
| 重构、删除、改名 | `.agents/rules/cleanup-compliance.md` | 全仓引用、`scan_dead_refs.sh` |
| 测试或构建失败 | `.agents/rules/troubleshooting.md` | 完整日志、失败测试与调用链 |
| API 测试 | `.agents/skills/test-portal-api/SKILL.md` | `test/api/*.http`、API 验证脚本 |
| 更新开发文档 | `.agents/skills/update-project-docs/SKILL.md` | `docs-site/docs/` 与实现 |
| Web 资源审计 | `.agents/skills/web-asset-audit/SKILL.md` | 资源引用与构建入口 |
| 演进审计 | `.agents/skills/codebase-evolution-audit/SKILL.md` | 当前实现 |
| 架构 / 部署 / 业务背景 | 只读任务相关 `docs-site/docs/` 章节 | 源码、Schema、Proto、配置、构建、CI |

## 路径提示

- `internal/server/portal/api/**`：一起核对 handler、路由、`.http` 测试和 API 文档。
- `internal/db/**`、`internal/model/**`、数据库迁移目录：先确认实际 schema / migration 来源。
- `internal/contract/proto/**`、`internal/server/service/settings/**`：按对应规则及生成/测试链路检查。
- `.agents/**`：只读取正在修改的规则、Skill、脚本或验证器。
- `docs-site/docs/**`：只读取相关章节并与实现核实。

默认只加载启动约束和任务相关规则/Skill；不预载整棵文档树、源码树、生成物、锁文件、构建目录或数据文件。
