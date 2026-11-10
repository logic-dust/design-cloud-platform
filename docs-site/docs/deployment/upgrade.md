---
title: 升级与回滚
linkTitle: 升级与回滚
weight: 80
description: 版本升级步骤、配置与数据库变更处理、回滚路径
---

本文描述引擎与节点的版本升级与回滚流程。构建与版本号规则见 [发布与制品](./release.md)；Schema 演进机制见 [存储与缓存架构](/docs/architecture/domains/storage-and-cache)。

## 升级前检查

1. **备份数据库**：升级会在启动期执行 `AutoMigrate`，可能修改表结构。PostgreSQL 使用逻辑/物理备份，SQLite 备份数据文件；备份时避免持续写入。
2. **备份配置与证书**：`/etc/design-cloud/config.toml`、`dbpwd`、`ldapbindpwd` 与 `/var/lib/design-cloud/certs/cluster/`。
3. **确认版本兼容**：`engine` 与 `dc-exec` 版本锁步（同一 RPM 内），不要混用新旧二进制。
4. **读取 rpmnew**：升级后检查 `.rpmnew` 文件，手工合并配置差异。

## RPM 升级步骤

```bash
# 1. 停止服务（包内 %posttrans 仅对 active unit 执行 try-restart）
systemctl stop design-cloud-engine

# 2. 备份数据库与配置（略，见上）

# 3. 安装新包（同机原地升级）
yum install -y build/design-cloud-engine-<new-version>.rpm

# 4. 校验配置（未知键/权限问题在启动前暴露）
/opt/dce/dc-engine check-config --config /etc/design-cloud/config.toml

# 5. 启动并核对版本
systemctl start design-cloud-engine
/opt/dce/dc-engine --version
```

`%config(noreplace)` 保证已改过的 `config.toml` 不被覆盖；新模板以 `.rpmnew` 落盘。

## 节点升级

- **Linux 计算节点**：configless 节点只有二进制与 drop-in 需要更新；重新执行 `deployment/install.sh agent`（证书材料已就位时加 `--skip-register`）即可覆盖二进制并重启服务。
- **Windows 节点**：重新执行 `deployment/install-win-agent-configless.ps1`；脚本会先停止并卸载已有服务，再覆盖二进制，避免文件占用导致复制失败。
- **滚动升级顺序**：先升级 Standby Server，再升级 Leader；Agent 按 `Cluster.Servers` 顺序回连，升级期间允许短暂断连并按 [高可用集群部署](./cluster-ha.md) 的故障转移行为重连。

## 回滚

1. 停止新版本服务。
2. 重新安装上一版 RPM（保留旧版本安装包）。
3. 恢复升级前的数据库备份（新版本可能已扩展 Schema；旧二进制不保证向下兼容新列）。
4. 恢复配置与证书备份，`check-config` 通过后启动。
5. 核对 `/api/version` 的 `version` / `commit` / `releaseDate` 确认回滚生效。

> 回滚数据库会丢失升级后产生的数据；只在升级失败且无法前滚修复时使用，并提前告知业务方。
