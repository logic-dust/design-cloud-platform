---
title: 测试策略与门禁验证
linkTitle: 测试体系
weight: 60
description: 单测、契约测试、集成测试与提交前门禁矩阵
aliases:
  - /docs/development/guide/testing
---

本文描述各层测试的职责、运行方式与提交前应选的验证入口。变更类型与门禁对照见仓库根 [`.agents/README.md`](../../../.agents/README.md)。仓库内跨进程集成资产的目录索引见 [`test/README.md`](../../../test/README.md)。

## 分层

| 层级 | 范围 | 入口 |
|---|---|---|
| 单元测试 | 各包 `*_test.go`；使用内存 SQLite / fake 替代外部依赖 | `go test ./...` |
| 架构边界 | `db.Store`、配置注入与分层约束 | `python3 .agents/verifiers/verify-arch.py` |
| 构建/静态检查 | 全量编译、`go vet`、`gofmt`、staticcheck、govulncheck | `verify-build.sh`、`make lint`、`make vuln` |
| Portal HTTP 契约 | `test/portal/*.http` 与已注册路由的一致性（含 `# @manual`） | `python3 .agents/scripts/verify_http_tests.py --strict` |
| Portal HTTP 集成 | 真实 Engine + 数据库的 REST 套件（`test/portal/`） | `make test-api` → `test/portal/run.sh --skip-deploy` |
| 本地节点 / dc-exec | 角色模式、configless、agent service、dc-exec 安全（`test/node/`） | `make test-e2e` → `test/node/run_all.sh`（需 `build/`、特权；不进提交门禁） |
| 实验室场景 | cent7 / agent7 等固定环境（`test/lab/`） | 见各脚本与 [发版核对清单](../../../test/lab/post-deploy-checklist.md) |

> **命名**：`make test-e2e` 只跑 **本地** `test/node/`，不是 Portal HTTP。Portal 自动化入口是 `make test-api` / `test/portal/run.sh`。不要把两者混称「e2e」。

## 常用命令

```bash
# 全量单元测试
go test ./...
./.agents/verifiers/verify-tests.sh --all

# 指定包 / race
./.agents/verifiers/verify-tests.sh ./internal/server/service/user/...
./.agents/verifiers/verify-tests.sh --race

# 构建与架构边界
./.agents/verifiers/verify-build.sh
python3 .agents/verifiers/verify-arch.py

# 静态分析与漏洞扫描（工具以 go1.27.2 构建）
make lint
make vuln
```

## 门禁矩阵

| 变更类型 | 推荐门禁 |
|---|---|
| 日常 Go / `.proto` / `go.mod` | `./.agents/verifiers/verify-changed.sh`（核心门禁 + 条件检查） |
| 并发相关改动 | `./.agents/verifiers/verify-changed.sh --race` |
| 仅构建验证 | `./.agents/verifiers/verify-changed.sh --skip-tests` |
| `test/portal` 变更 | `verify-all.sh`（变更感知模式）自动跑 `verify_http_tests.py --strict` |
| 发版 / 大重构 | `./.agents/verifiers/verify-all.sh --full` |
| 仅文档改动 | 文档站构建（见下），无 Go 门禁 |

`verify-changed.sh` 只验证 git 工作区中发生变更的包，附加执行依赖整洁度、安全红线扫描、死代码扫描与 API/文档同步检查；`verify-all.sh --full` 额外覆盖 Windows 交叉编译、lint/vuln 与 proto 检查。

## Portal HTTP 契约与集成

- 新增或修改 Portal 端点时，同步更新 `test/portal/*.http`。
- 静态契约：`python3 .agents/scripts/verify_http_tests.py --strict`（路由覆盖、鉴权 scope、文件域归属）。`# @manual` 块计入契约覆盖，但 **`make test-api` 不会执行**；校验输出会单独报告「仅 @manual」路由数。
- 对真实 Engine 跑套件：`make test-api`（默认 `--skip-deploy`）；需要先部署实验室控制面时用 `bash test/portal/run.sh`（默认 `remote.sh build`）。
- 手跑 REST Client 索引见 `test/portal/README.md`。

## 文档站构建

文档改动不触发 Go 门禁。若安装了 Hugo，可在 `docs-site/` 下执行 `hugo --gc --minify` 校验构建与链接布局。
