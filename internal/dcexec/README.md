# dc-exec Implementation Package

`dc-exec` 二进制实现位于本目录；**仅 Linux**（`//go:build !windows`）。

## 目录结构

```
internal/dcexec/
├── dc-exec.go          # Handler registry（与 execution.ActionSpec.dc-exec 对齐）
├── action/              # 按领域划分的 handler 实现
└── verify/              # L1–L3 启动前校验
```

入口：`cmd/exec/main.go`。

## 文档

协议、安全模型、动作参数与部署约束以文档站为准：

- [特权执行器（dc-exec）](../../docs-site/docs/architecture/dcexec/_index.md)
- [dc-exec 架构](../../docs-site/docs/architecture/dcexec/architecture.md)
- [dc-exec Action](../../docs-site/docs/architecture/dcexec/actions.md)
- [dc-exec 安全](../../docs-site/docs/architecture/dcexec/security.md)

维护动作矩阵时运行：`python3 .agents/scripts/extract_actions.py`（仅列出 `dcexec.Handlers` 中的动作）。
