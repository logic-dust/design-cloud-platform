---
name: purge-proto-baggage
description: 审查 internal/contract/proto/ 中的 Protobuf 定义，识别并系统性修复命名不一致、reserved 保留字段残留、oneof 包装命名混乱、结构体冗余等历史包袱问题
---

# 清理 Proto 历史包袱

> 本 SKILL 是一个**独立可直接触发**的专项技能，也可由 `/refactor-cleanup` 在扫描时自动调用。
> 它分为两个强制阶段：**先分析上报**，经用户选择确认后，**再执行破坏性重构**。

## 触发方式

- **直接触发**：用户主动要求审查 `.proto` 文件的历史包袱
- **工作流调用**：`/refactor-cleanup` 在扫描 `internal/contract/proto/` 时检测到问题，自动触发本 SKILL

## 阶段一：全面扫描与分析（自动化脚本辅助，只读）

> [!IMPORTANT]
> **本阶段严禁修改任何文件。** 目标是给用户一份清晰的问题清单与风险评估，
> 让用户在知情的情况下决定修什么、不修什么。

### 1. 运行 Protobuf 检查自动化工具

```bash
python3 ./.agents/scripts/scan_proto_issues.py
```

输出：`reserved` 字段列表、`oneof` 蛇形命名与类型名不一致项、字段序号空洞分析。

### 2. 检查结构冗余（字段集合重复）

```bash
grep -n "connection_id\|job\|username\|host\|port\|display\|protocol\|pid" internal/contract/proto/*.proto
```

### 3. 生成分析报告并请求用户确认

将扫描结果整理为结构化报告，每个问题项附上风险等级和建议操作，然后 **暂停并等待用户选择**。

## 阶段二：执行用户选定的修复项

> [!IMPORTANT]
> **只执行用户明确选择的项目**，未选中的问题项保持原样，不做任何"顺手"修改。

1. **修改 `.proto` 文件**（先新增 -> 再替换 -> 再改名 -> 最后清理 reserved 与序号）
2. **重新生成 Go 代码**：`make proto`
3. **修复所有 Go 编译错误**：`go build ./...`
4. **格式化**：`gofmt -w internal/contract/proto/`

修改 `.proto` 后，项目增量门禁会自动运行 Proto 健康扫描；分析阶段可手动扫描预览。最终按门禁结果及编译/测试结果收尾。

## 协议契约规范

Proto 是内部集群通信协议，无外部消费者。字段号可直接复用、无需 reserved 保留——详见 [rules/proto-refactoring-rules.md](../rules/proto-refactoring-rules.md)。
