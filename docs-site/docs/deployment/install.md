---
title: 安装与首次部署
linkTitle: 安装部署
weight: 10
description: 角色拓扑、前置条件、目录布局与单机/分布式安装
---

本文档覆盖 Design Cloud Engine 从零到首次启动。多 Server 高可用见 [高可用集群部署](./cluster-ha.md)，证书细节见 [TLS 证书配置](./tls.md)，日常服务管理见 [运维与服务管理](./operations.md)。

## 概述

Engine 采用 **`Cluster.Servers` 拓扑配置**驱动的角色自动发现机制：

| 角色       | 核心判断条件                                               | 组件                                 |
|----------|------------------------------------------------------|------------------------------------|
| `server` | 节点主机名匹配 `cluster.servers` 中的任意项                   | Portal + Control + 数据库驱动的服务        |
| `agent`  | 全节点常驻无条件启用                                         | ClusterAgent + Executor + dc-exec |

**常见部署架构：**

- **单机部署**：同一台机器同时部署 Server 和 Agent，`servers = ["localhost"]`（或本机主机名），Agent 无条件常驻运行
- **分布式集群**：多台服务器共享一个数据库，所有节点共享相同的 cluster CA 证书
- **多 Server 高可用**：多台 Server 共享同一数据库，必须同步相同的 cluster CA 证书，Agent 按
  `Cluster.Servers` 列表顺序回退连接

## 前置条件

### 运行时依赖

| 组件       | 版本要求                    | 说明                  |
|----------|-------------------------|---------------------|
| Linux    | RHEL 7+ / Ubuntu 20.04+ | 生产推荐（Server 与 Linux 计算节点） |
| Windows  | Windows 10/11 / Windows Server 2019+ | 仅限 Agent 节点（图形设计工作站） |
| Database | 见所选驱动要求                 | 持久化后端；PostgreSQL 或 SQLite，见 [存储与缓存架构](/docs/architecture/domains/storage-and-cache) |

### 网络端口

| 端口      | 协议    | 用途                             |
|---------|-------|--------------------------------|
| `443`   | HTTPS | GUI 和 Portal API（可通过配置修改）      |
| `52255` | gRPC  | Cluster 协议入口，Agent 接入（可通过配置修改） |
| `3389`  | TCP   | Windows 节点入向：远程桌面（TermService），端口以注册表 `WinStations\RDP-Tcp\PortNumber` 为准 |

### 系统用户（推荐）

RPM `%pre` 会创建专用系统账号（也可事先在 FreeIPA 建同名 nologin 账号，包会复用）：

```bash
# 与包装默认一致（无 home）
groupadd -r dce
useradd -r -M -g dce -s /sbin/nologin -c "Design Cloud Engine" dce
```

开发机 SSH 账号（如 `hpcadmin`）与进程身份无关，不要混用。

## 目录布局

```
/opt/dce/
├── dc-engine             # 主程序（0755 dce）
└── dc-exec           # 特权助手（0700 dce + setcap）

/etc/design-cloud/
├── config.toml           # ConfigDir：配置（0600；随包模板，%config(noreplace)）
├── dbpwd                 # 密钥（不随包；运维落盘 0600）
└── ldapbindpwd

/var/lib/design-cloud/    # DataDir：集群证书与持久状态（配置项 data_dir / Join 的 --data-dir）
/var/log/design-cloud/    # LogDir：审计/安全日志（app/access 走 journald）
/run/design-cloud/        # RuntimeDir：PID flock、dc-exec token（开机重建）
```

> **ConfigDir ≠ BaseDir**：ConfigDir 是 FHS `/etc/design-cloud`。BaseDir 是 `dirname(--config)`（相对路径拼接根）。打包时二者数值常相同，概念仍分开。集群证书在 **DataDir**，不在 ConfigDir。

## 构建

构建统一由仓库根 `Makefile` 驱动（`make engine` / `make dcexec` / `make win-agent` / `make package`）。工具链要求、版本注入、开发/生产构建差异见 [开发环境 · 构建系统](/docs/development/dev-environment#构建系统)，本文不重复。

## 单机部署

单机模式下，Server 和 Agent 在同一进程中运行，Agent 通过 loopback 连接本地 cluster gRPC 入口。

### 生成配置文件

```bash
cp config.dev.toml config.toml
chmod 600 config.toml
```

然后根据实际情况修改 `config.toml`，参见 [配置文件](./config.md)。

### 一键启动（开发模式）

开发模式（`-tags dev` 构建）会自动完成数据库迁移、证书生成和 Agent 注册：

```bash
# 以开发模式启动（单机，拓扑同时命中 Server 和 Agent）
./dc-engine
```

### 生产模式启动

```bash
# 生产启动（配置文件已就绪，topology 定义了节点角色）
./dc-engine --config /etc/design-cloud/config.toml
```

## 分布式部署

### Server 节点

1. **编写共享 `config.toml`**，在 `cluster.servers` 中定义 Server 节点；纯计算节点不需列出，程序会根据本机 hostname 自动识别角色。
2. **Cluster 证书**。Engine 启动时若检测到 CA 不存在会**自动生成**自签名根证书（`certs/cluster/ca.crt` + `certs/cluster/ca/ca.key`），单 Server 部署无需手动操作。使用企业内部 CA 或自定义证书时，按 [TLS 证书配置 · Cluster mTLS](./tls.md#cluster-mtls-证书) 在首次启动前放置材料。
3. **启动**：

   ```bash
   ./dc-engine --config /etc/design-cloud/config.toml
   ```

### Agent 节点

Agent 节点接入集群并建立双向 mTLS 信任关系，支持三种交付场景：**共享存储离线签发**（`node issue-cert` 直接落盘，推荐）、**在线 OTP 自助注册**（`node issue-token` + `node register`）、**离线介质分发**（`--archive` 导出后解包）。三者最终都把 `cert/key/ca` 落到 `<DataDir>/certs/cluster/`，Agent 据此以 mTLS 接入。

Linux 与 Windows 计算节点均可使用官方一键脚本（`deployment/install.sh agent`、`deployment/install-win-agent-configless.ps1`），无需 `config.toml`。完整的命令、参数、脚本行为与启动成功判据见 **[节点注册与引导](./node-registration.md)**。

下列 `[cluster]` 片段仅说明 **带 `config.toml` 的 Agent 布局**；计算节点一键安装以 node-registration 中的 configless 流程为准，勿与上段混用。

Agent 侧至少需要声明集群入口：

```toml
[cluster]
port = 52255
servers = ["server-01"]
```
