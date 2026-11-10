任务路由、项目约束与提交前门禁见 [`.agents/README.md`](.agents/README.md)。

- 工具链最低要求：Go 1.27.2+；`go.mod` 的 `go` / `toolchain` 指令管理语言版本与工具链切换，不代替最低补丁版本要求。
- 提交前按变更类型选择门禁，不默认跑全量：日常 Go / `.proto` / `go.mod` 变更 → `./.agents/verifiers/verify-changed.sh`；发版或大重构 → `./.agents/verifiers/verify-all.sh --full`；仅文档 / 纯 Harness 文案 → 见 README「入口与验证」。
