# test/

跨进程、跨节点的集成测试分三层，互不替代：

| 层 | 目录 | 入口 | 性质 |
|---|---|---|---|
| Portal HTTP 集成 | [portal/](./portal/) | `make test-api` | 自动化主力；`.http` + Python runner，覆盖 197 个路由模板 |
| 本地节点 / dc-exec | [node/](./node/) | `make test-e2e` | 需 `build/` 产物、root 与 setcap；不进提交门禁 |
| 实验室手工场景 | [lab/](./lab/) | 见各脚本 README | 依赖 cent7 / agent7 等固定环境 |

## 不在本目录的测试

- **Go 单元测试**：`internal/.../*_test.go`，`go test ./...`；提交前门禁见 [.agents/README.md](../.agents/README.md)。
- **路由清单**：[`portal/portal_routes.txt`](./portal/portal_routes.txt) 由 `routes_inventory_test.go` 生成，勿手改。

## 常用入口

```bash
make test           # Go 单测
make test-api       # Portal HTTP（test/portal/run.sh --skip-deploy）
make test-e2e       # 本地节点 / DcExec（test/node/run_all.sh）
```

发版后在实验室集群上的检查清单见 [lab/post-deploy-checklist.md](./lab/post-deploy-checklist.md)。
