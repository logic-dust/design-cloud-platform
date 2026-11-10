# test/node/

节点与 dc-exec 的手工集成测试层。这些用例需要真实二进制、特权操作或特定节点环境，因此不进入自动化门禁，由 `make test-e2e` 显式触发。

## 前置条件

```bash
make engine dc-exec          # 或 ./test/node/run_all.sh --build
```

- 需要 `build/dc-engine` 与 `build/dc-exec` 两个产物，缺失时入口脚本直接报错退出。
- `agent-service.sh`、`security-features.sh` 涉及 agent service 与 dc-exec 拉起：要求 `dc-exec` 具备 `cap_setuid,cap_setgid`（部署方式见 RPM / 安装文档）；未授权时按脚本输出区分「环境不满足」与「实现缺陷」。
- 全部用例使用本地临时目录，不依赖 cent7 / agent7-1 测试集群。

## 用例

| 脚本 | 覆盖 |
|---|---|
| [node-modes.sh](./node-modes.sh) | 拓扑驱动的角色启动：server-only 与 agent-only |
| [configless.sh](./configless.sh) | 无配置引导：模式阶梯 fail-fast、bootstrap 参数校验、证书缺失指引、节点注册 portal-url 校验、证书权限矩阵 |
| [agent-service.sh](./agent-service.sh) | agent service 功能与 dc-exec 集成（基于 config.toml） |
| [security-features.sh](./security-features.sh) | dc-exec 安全特性 + server 认证 smoke |

## 用法

```bash
./test/node/run_all.sh              # 全部用例
./test/node/run_all.sh --build      # 先构建产物
bash test/node/node-modes.sh        # 单个用例
```

`run_all.sh` 只汇总本层用例的通过/失败，不跑 `go test`、覆盖率或基准——那些属于 `make test` 与 `.agents/verifiers`。

## 共享工具

[_common.sh](./_common.sh) 提供统一日志与断言函数，供本目录脚本 `source`。改造现有用例时复用它，不要重复实现。
