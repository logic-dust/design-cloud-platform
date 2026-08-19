---
name: codebase-evolution-audit
description: 全面扫描与诊断项目在经历多次演进迭代后累积的四大架构暗伤：命名不准与概念多义、历史冗余与废弃资产、过度兼容与兼容层蔓延、业务外溢与语义污损。生成结构化研判报告并给出治理路线建议。
---

# 代码库演进与架构债务治理 (Codebase Evolution & Technical Debt Audit)

> **定位**：本 SKILL 是一个**全局诊断型架构治理技能**。
> 当项目经历过多次业务重构、模块拆分、协议演进或团队交接后，系统极易在代码库各处沉淀“命名漂移、死代码残留、多重过度兼容、分层职责击穿”等暗伤。
> 本技能提供一套标准化的**静态扫描、启发式研判、分级分类与治理方案输出**体系。

---

## 职责边界

- 本 Skill 负责代码库演进债务、历史冗余、兼容层和分层语义污染的证据化审计。
- `purge-proto-baggage` 负责 Proto 专项清理；`update-project-docs` 负责文档同步。
- Go 现代化和通用死代码清理分别交给全局 `go-modernize`、`refactor-cleanup`。
- 本 Skill 只读诊断；任何删除、协议变更或跨层重构必须另行确认。

## 核心原则

1. **只读诊断，严禁擅自修改**：扫描阶段仅做静态分析与代码阅读，绝不直接执行任何破坏性重构或文件编辑。
2. **拒绝盲目清理**：必须结合业务上下文区分“历史无用包袱”与“必要安全防御/协议规范”。
3. **输出可落地行动方案**：每项发现必须附带精准代码锚点、风险评级（Low / Medium / High）与对比重构建议。

---

## 五大审计维度与诊断标准

### 维度 1：命名不准与术语多义 (Naming Inaccuracy & Terminology Drift)
- **多义词泛化**：同一名称在不同模块表示不同实体（如 `Session` 混用）。
- **度量单位含混**：变量名未标明单位（如 `DiskLimit`、`Timeout`），或底层返回 Byte 字段却叫 `SizeKB`。
- **职责脱节**：`GetXxx` 包含写副作用，或 `ValidateXxx` 包含了持久化操作。

### 维度 2：历史冗余与废弃资产 (Dead Assets & Ghost Logic)
- **陈旧脚本与工具**：已被替代但未清理的单机部署/迁移脚本。
- **孤立代码与无用符号**：无外部引用的错误变量 `ErrXxx`、常量、兼容别名（`type X = Y`）、大段注释代码。
- **废弃兜底与冗余 I/O**：已有主动通道（UDS/RPC）却仍保留向磁盘写入未消费的临时 JSON 并轮询。

### 维度 3：过度兼容与兼容层蔓延 (Over-compatibility & Redundant Fallbacks)
- **内部通信过度兼容**：私有集群协议升级后仍保留 `reserved` 字段、旧版本嗅探。
- **多级兼容探测**：遍历多个候选文件名/旧路径尝试读取。
- **死分支防线**：生产环境已无法启用的历史逻辑分支。

### 维度 4：业务外溢与语义污损 (Semantic Pollution & Layer Leaks)
- **控制器职责外溢**：Handler 中编写跨表事务、配额核验与事件派发，Service 层沦为透传。
- **特定场景魔术后门**：中间件硬编码特定的 Bypass Token、Magic Header 或固定 UID。
- **上下文污染**：Context 中塞入无类型 Magic String 传递业务关键参数。

### 维度 5：外部依赖健康度与演进审计 (Dependency Evolution & Health Audit)
- **归档与弃用依赖**：上游已标记 Deprecated / Archived 的依赖库。
- **未及时 Tidy 的残留依赖**：代码已不再使用但 `go.mod` 依然残留的依赖。
- **依赖演进分级管理原则**：
  - **🟢 小变动 (Patch/Minor 平滑升级)**：如 `v1.2.0 -> v1.2.1`。处置：可直接更新，`go mod tidy` 并跑通单测合入。
  - **🔴 大变动 (Major/核心框架升级)**：跨主版本号或核心库（Web引擎/GORM/gRPC）。处置：**极其审慎**，严禁与业务混杂；建独立分支/commit，核查 Breaking Changes，跑 `./.agents/verifiers/verify-all.sh --full`。

---

## 扫描与诊断标准工作流

### 1. 静态扫描命令
```bash
# 死代码与兼容残留（推荐直接调用封装脚本）
./.agents/scripts/scan_dead_refs.sh

# 兼容别名与注释代码
grep -rniE "type\s+[A-Za-z0-9_]+\s*=\s*[A-Za-z0-9_]+" internal/
grep -rniE "^[[:space:]]*//\s*(func|type|var|const)\s+" internal/

# 只读检查依赖使用与升级信息，不运行 `go mod tidy`（该命令会改写 go.mod/go.sum）。需要整理依赖时，单独说明并确认后再执行。
go list -u -m -f '{{if and (not .Indirect) .Update}}{{.Path}}: {{.Version}} -> {{.Update.Version}}{{end}}' all 2>/dev/null
```

### 2. 风险定级准则
- **🟢 Low (无痛清理)**：纯内部实现细节（删除死代码、无用变量、废弃历史脚本；Patch/Minor 升级）。
- **🟡 Medium (局部收敛)**：模块内部领域命名重命名、补齐单位、收敛内部 fallback。
- **🔴 High (架构/API 变更)**：对外 API、跨进程协议、分层职责击穿、跨 Major 主版本依赖升级。

---

## 报告输出标准模板

在扫描诊断完成后，向用户输出结构化治理报告：

```markdown
# 🏛️ 项目演进与架构债务治理诊断报告

## 📊 总体概览
- **扫描范围**：`{目标模块/全库}` | **健康度研判**：`{良好 / 存在中度概念漂移 / 存在重度冗余}`
- **问题统计**：命名多义 ({X}) | 历史冗余 ({Y}) | 过度兼容 ({Z}) | 业务外溢 ({W}) | 依赖升级 ({V})

## 🔍 深度诊断清单
### 1. 命名不准与核心领域多义
| 现状命名与位置 | 核心冲突 / 歧义根因 | 推荐精准命名方案 | 改造风险 |
| :--- | :--- | :--- | :--- |
| `[符号名](file:///path/to/file#L1)` | 歧义描述 | 推荐命名 | 🟢 Low / 🟡 Medium |

### 2. 历史冗余与废弃资产
- **[文件/符号](file:///path/to/file#L1)**: 冗余说明与删除建议。

### 3. 过度兼容与冗余防线
- **[位置](file:///path/to/file#L1)**: 兼容层与 fallback 移除建议。

### 4. 业务外溢与分层击穿
- **[位置](file:///path/to/file#L1)**: 业务下沉至 Service 层建议。

### 5. 外部依赖演进与升级建议
- **🟢 小变动 (Patch/Minor)**: `pkg`: `v1.0.0 -> v1.0.1`（平滑升级并 tidy）
- **🔴 大变动 (Major/核心库)**: `pkg`: `v1 -> v2`（需独立分支/commit 审慎评估）

## 🛠️ 分阶段治理行动建议与专项技能联动
| 诊断发现场景 | 推荐承接的专项技能 | 职责说明 |
|:---|:---|:---|
| **Protobuf 历史包袱** | `purge-proto-baggage` | 审查与重构 `internal/contract/proto/` |
| **前端资产与样式污染** | `web-asset-audit` | 审查与精简 `internal/server/portal/web/` |
| **API 契约与文档测试同步** | `update-project-docs` | 同步 `docs-site/docs/` 与 `.http` 测试 |
| **Go 现代化与死代码消除** | 全局 `go-modernize` / `refactor-cleanup` | 语法升级与死代码消除 |
```
