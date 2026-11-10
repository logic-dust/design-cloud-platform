# Deployment Assets

部署脚本、systemd 单元与打包资产。规范与操作说明以 [`docs-site/docs/deployment/`](../docs-site/docs/deployment/) 为准。

| 路径 | 说明 |
|---|---|
| [`systemd/design-cloud-engine.service`](./systemd/design-cloud-engine.service) | 权威 systemd 单元（RPM 与 `install.sh` 共用） |
| [`packaging/`](./packaging/) | `make package` → nfpm；含 scriptlets、tmpfiles、logrotate 样例 |
| [`install.sh`](./install.sh) | 无包/源码树引导（`server` \| `agent`）；生产以 RPM 为准 |
| [`install-win-agent-configless.ps1`](./install-win-agent-configless.ps1) | Windows configless 节点 |
| [`postgres-create-db.sh`](./postgres-create-db.sh) | PostgreSQL 首次建库/用户（运维辅助） |
| [`share/vnc/job-gui-wrapper.sh`](./share/vnc/job-gui-wrapper.sh) | 作业内 TurboVNC 包装脚本（RPM 安装至 `/usr/share/design-cloud-engine/scripts/vnc/`） |
| [`lib/install-common.sh`](./lib/install-common.sh) | 安装脚本共享逻辑（用户、setcap、tmpfiles） |

**日志**：应用与 HTTP 访问日志走 stdout → journald；`--log-dir` 仅存放 Portal 可 tail 的审计/安全日志。

**Portal TLS**：使用 `design-cloud-engine cert gen-portal`（见 [tls.md](../docs-site/docs/deployment/tls.md)）。

文档入口：[安装](../docs-site/docs/deployment/install.md) · [运维](../docs-site/docs/deployment/operations.md) · [发版](../docs-site/docs/deployment/release.md)
