---
name: codebase-evolution-audit
description: 针对 design-cloud-engine 的演进与架构债务只读审计与分诊。覆盖分层、DcExec-Agent、Settings、依赖与死代码候选；Proto/Web 专项交接对应 Skill。输出带文件锚点的分级报告，不直接改代码。
---

# design-cloud-engine 演进与架构债务审计

## 事实来源与边界

| 项 | 位置 |
|---|---|
| 技术栈 | Go 1.27；Echo Portal；gRPC 集群；SQLite/PostgreSQL |
| 核心目录 | `internal/server/portal/`、`internal/agent/`、`internal/dcexec/`、`internal/contract/proto/`、`internal/db/`、`cmd/`、`deployment/`、`test/portal/`、`test/node/`、`test/lab/` |
| 架构红线 | `.agents/rules/arch-rules.yaml` → `python3 .agents/verifiers/verify-arch.py` |
| 分层与清理 | `.agents/rules/backend-layering-principles.md`、`cleanup-compliance.md` |
| API 契约 | `internal/server/portal/api/`、`test/portal/*.http`、`verify_http_tests.py --strict` |
| 验证门禁 | 通用门禁见 `.agents/README.md`；本 Skill 只读使用 `scan_evolution_debt.py`、`scan_dead_refs.sh`、`verify-arch.py` |

**只读诊断，分诊优先**。删除、对外 API/Proto 破坏性变更、存储迁移须单独确认。本 Skill 不重复专项规则：Proto 结构重构交 `purge-proto-baggage`；Web 资产审查交 `web-asset-audit`；文档与 `.http` 同步交 `update-project-docs`；Settings/Overview/推送按各自 `rules/*.md` 判定。

## 审计维度（按证据启用）

1. **命名与领域语义** — Handler/Service/Store 职责是否一致；`Session`/`Connection`/`Job` 等跨包是否多义；配置字段单位（字节 vs KB、秒 vs 毫秒）。
   **多义判定三条件**（须全满足才算发现；判定材料必须含产品语义——先读领域速查矩阵/产品文档中该词的定义，禁止只凭代码字面推断）：
   - ① 同名指称**互不相关**的概念。同一领域概念的多个视图（如 `表X` 记录本体 + `表X_资源` 前置授权 + `ServiceX` 管理两者）**不算多义**；
   - ② 包边界与类型系统无法消歧（同名标识符跨包混用，而非各归其包）；
   - ③ 存在实际混淆证据：历史 bug、评审中反复澄清、或新人 onboarding 卡点。
   三条缺任何一条，降级为「无需动作的备注」或不报。（案例：曾把 `connection` 判为一词三义，实则"用户到计算节点的连接"是产品单一概念，被用户驳回。）
2. **死代码与残留** — 无引用导出、兼容别名、注释掉的声明；dc-exec action 与文档是否仍对齐（`extract_actions.py`）。
3. **兼容与 fallback** — 集群/Agent 路径是否仍保留已废弃嗅探或多级文件探测；区分**有意防御**（如运行时目录 fallback）与**可收敛历史分支**。
4. **分层与契约** — Portal handler 是否承载事务/编排；Settings schema 与 `settings-schema-rules.md` 是否一致；Overview/推送是否与 `push-principles.md` 一致。
5. **依赖与模块** — 只读 `go list -u -m` 看 direct 升级；**禁止**在审计流程中擅自 `go mod tidy`（会改写 `go.mod`/`go.sum`）。

## 扫描与复核

```bash
# 候选启发式（默认 internal/，跳过 .pb.go 噪声）
python3 .agents/scripts/scan_evolution_debt.py

# 死代码 / 兼容残留（删除导出符号前）
./.agents/scripts/scan_dead_refs.sh

# 架构依赖边界（全量或 import 变更后）
python3 .agents/verifiers/verify-arch.py

# Proto 结构健康（.proto 变更时）
python3 .agents/scripts/scan_proto_issues.py
```

**误报边界**：正则命中 `fallback`/`legacy` 不等于缺陷；`agent/probe`、`secure_fs` 的运行时 fallback 需读注释与测试。`scan_evolution_debt.py` 输出仅为**候选清单**，每条须打开源码确认。两个已知盲区：类型别名（`type X = Y`）被扫描器视为死别名，须 grep 全部使用点再判；领域命名是否多义以**产品语义**为准，同概念多视图不算（见维度 1 三条件）。

## 风险分级

- **Low**：内部死代码、脚本、注释块；Patch/Minor 依赖（单独 commit，按 README 门禁验证）。
- **Medium**：包内重命名、收敛内部 fallback、Settings 非破坏性整理。
- **High**：对外 HTTP/Proto、RBAC、跨进程认证、Major 依赖、DB 迁移。

## 报告模板

```markdown
# design-cloud-engine 演进债务审计

- 范围：<模块或全库 internal/>
- 扫描：<日期>；命令：scan_evolution_debt / scan_dead_refs / …

## 统计（仅已核实项）
| 维度 | 已确认 | 候选未采纳 |
|------|--------|------------|

## 发现（按风险排序）
| ID | 风险 | 位置 | 证据摘要 | 建议 | 交接 Skill/Verifier |
|----|------|------|----------|------|---------------------|

## 未采纳候选
- <路径:行> — 原因（有意 fallback / 生成代码 / 测试固件）

## 建议顺序
1. …
```

不得将扫描 `total=` 直接写成「健康度得分」；无核实证据时不写总体百分比健康分。
