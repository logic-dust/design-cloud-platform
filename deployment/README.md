# Deployment Assets

部署脚本与打包资产。规范与操作说明以 [`docs-site/docs/deployment/`](../docs-site/docs/deployment/) 为准。

## 名称对照

| 面 | 名称 |
|---|---|
| RPM / DEB 包名 | `design-cloud-engine` |
| systemd 单元 | `design-cloud-engine.service` |
| Linux 安装前缀 | `/opt/design-cloud`（`dc-engine`、`dc-exec`） |
| 系统用户 / 组 | `design-cloud`（可含短横线；与路径同名） |
| Windows 二进制 | `dc-engine.exe`（安装目录默认 `C:\Program Files\DesignCloudEngine`） |
| 配置 / 数据 / 日志 / 运行时 | `/etc/design-cloud`、`/var/lib/design-cloud`、`/var/log/design-cloud`、`/run/design-cloud` |
| CLI | `dc-engine …`（如 `dc-engine cert gen-portal`） |
| DB 角色（无关 OS 用户） | PostgreSQL 库/用户仍常用 `dce`（`config` 里 `database.user`） |

## 目录

| 路径 | 说明 |
|---|---|
| [`packaging/`](./packaging/) | `make package` → nfpm；含 scriptlets、`lib/`、`systemd/`、tmpfiles、logrotate、configless 样例 |
| [`packaging/systemd/`](./packaging/systemd/) | 权威 systemd 单元（RPM 与 `install.sh` 共用） |
| [`packaging/lib/`](./packaging/lib/) | 安装脚本共享逻辑（服务用户、setcap、tmpfiles） |
| [`install.sh`](./install.sh) | 无包/源码树引导（`server` \| `agent`）；生产以 RPM 为准 |
| [`install-win-agent-configless.ps1`](./install-win-agent-configless.ps1) | Windows configless 节点安装脚本（配套 [`install-win-agent.cmd`](./install-win-agent.cmd) 绕过 PowerShell 执行策略；`make win-package` 打成 zip） |
| [`db/postgres-create-db.sh`](./db/postgres-create-db.sh) | PostgreSQL 首次建库/用户（运维辅助） |
| [`db/migrations/`](./db/migrations/) | PostgreSQL 存量库阶梯式 SQL 迁移脚本与 `migrate.sh`（按版本链顺序升级） |
| [`share/vnc/job-gui-wrapper.sh`](./share/vnc/job-gui-wrapper.sh) | 作业内 TurboVNC 包装脚本（RPM 安装至 `/usr/share/design-cloud-engine/scripts/vnc/`） |

**日志**：应用与 HTTP 访问日志走 stdout → journald；`--log-dir` 仅存放 Portal 可 tail 的审计/安全日志。

**Portal TLS**：使用 `dc-engine cert gen-portal`（见 [tls.md](../docs-site/docs/deployment/tls.md)）。

文档入口：[安装](../docs-site/docs/deployment/install.md) · [运维](../docs-site/docs/deployment/operations.md) · [发版](../docs-site/docs/deployment/release.md)
