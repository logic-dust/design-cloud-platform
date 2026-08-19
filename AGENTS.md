# Agent 启动协议

- 遵守凭证安全与高风险操作边界；未经用户明确同意，不执行 `sudo`、破坏性数据操作或外部发布。
- 既有对外 API 契约的破坏性变更先与用户协商；内部实现不保留历史兼容层。
- Go 变更提交前运行 `./.agents/verifiers/verify-changed.sh`。
- 按任务读取 [`.agents/README.md`](.agents/README.md) 路由到相关规则、技能与事实源；不要预加载无关目录。
