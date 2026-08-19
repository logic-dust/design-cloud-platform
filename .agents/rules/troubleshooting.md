<!-- context-optimizer: cohesive-specialist -->
- 诊断优先依据完整日志与可复现测试；修复根因，不屏蔽失败、不删除断言、不用 `t.Skip()` 伪造通过。
- 构建或测试失败时，先理解错误再修复和重跑；阻塞或无法安全修复时如实汇报，不以固定重试轮数替代判断。

## 验证入口

- 日常 Go 改动：`./.agents/verifiers/verify-changed.sh`
- 并发改动：`./.agents/verifiers/verify-changed.sh --race`
- 全量测试：`./.agents/verifiers/verify-tests.sh --all`
- 构建验证：`./.agents/verifiers/verify-build.sh`
