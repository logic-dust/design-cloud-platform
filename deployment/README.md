# Deployment Assets

本目录存放部署脚本、service 文件和辅助资源。部署规范与操作说明以 `docs-site/docs/deployment/guide.md` 为准。

正式部署说明请统一参考：

- [docs-site/docs/deployment/guide.md](../docs-site/docs/deployment/guide.md)

目录内容：

- `packaging/`：`design-cloud-engine` 单包（engine + launcher 锁步）。`make package` 正式包，
  `make package-dev` 开发快照（版本带 git hash）。默认 `PKG_TYPE=rpm`，`GOARCH` 可选（如 `arm64`）。
  安装到 `/opt/design-cloud`，服务账号 `design-cloud`，systemd unit + tmpfiles + `/etc` 配置模板一并带上。
- `design-cloud-engine.service`：systemd 单元权威源（随包安装；每台机器同一份，角色由 config 拓扑决定）。
- `install.sh`：**无包/源码树**引导脚本。角色是位置参数（`server` | `agent`）：
  - **`server`**：物化 Engine+Launcher → 生成 `/etc` 配置 → 安装 unit；
  - **`agent`**：物化两者 → `node register` → 安装 unit + configless drop-in → `systemctl restart`。
  有 RPM 的机器以包为准；本脚本不替代 yum。
- `install-win-agent-configless.ps1`：Windows 节点 Agent 一键部署
- `gen-tls-files.sh`：证书辅助生成
- `run.sh`：本地启动辅助脚本

注意：

- 生产安装主路径是 **RPM**；`install.sh` 只服务开发机与无包环境
- 不随包发数据库/LDAP 密钥文件；改好 `/etc/design-cloud/config.toml` 后再 `systemctl enable --now`
- configless 不装第二份 unit：用 `systemctl edit` / drop-in（示例见包内
  `/usr/share/doc/design-cloud-engine/systemd/configless.conf.example`）
- 这里的脚本是部署资产，不是配置模型的权威来源
- 关键行为、配置项、角色发现和证书约束应以 `docs-site/docs/deployment/guide.md`、`docs-site/docs/deployment/config.md` 和源码实现为准
- PostgreSQL 高可用属于外部基础设施能力；Engine 应始终连接单一固定 DB endpoint
