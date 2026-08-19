---
title: 部署指南
linkTitle: 部署指南
weight: 30
description: 构建、安装、systemd 与生产部署
---

本文档覆盖 Design Cloud Engine 从构建到生产运行的完整部署流程。

## 概述

Engine 采用 **`Cluster.Servers` 拓扑配置**驱动的角色自动发现机制：

| 角色       | 核心判断条件                                               | 组件                                 |
|----------|------------------------------------------------------|------------------------------------|
| `server` | 节点主机名匹配 `cluster.servers` 中的任意项                   | Portal + Control + 数据库驱动的服务        |
| `agent`  | 全节点常驻无条件启用                                         | ClusterAgent + Executor + Launcher |

**常见部署架构：**

- **单机部署**：同一台机器同时部署 Server 和 Agent，`servers = ["localhost"]`（或本机主机名），Agent 无条件常驻运行
- **分布式集群**：多台服务器共享一个数据库，所有节点共享相同的 cluster CA 证书
- **多 Server 高可用**：多台 Server 共享同一数据库，必须同步相同的 cluster CA 证书，Agent 按
  `Cluster.Servers` 列表顺序回退连接

---

## 前置条件

### 运行时依赖

| 组件       | 版本要求                    | 说明                  |
|----------|-------------------------|---------------------|
| Linux    | RHEL 7+ / Ubuntu 20.04+ | 生产推荐（Server 与 Linux 计算节点） |
| Windows  | Windows 10/11 / Windows Server 2019+ | 仅限 Agent 节点（图形设计工作站） |
| Database | 见所选驱动要求                 | 持久化后端；可根据部署环境选择具体驱动 |

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
groupadd -r design-cloud
useradd -r -M -g design-cloud -s /sbin/nologin -c "Design Cloud Engine" design-cloud
```

开发机 SSH 账号（如 `hpcadmin`）与进程身份无关，不要混用。

---

## 构建

```bash
# 使用 Makefile（推荐，自动注入版本号）
make engine
make launcher

# 或手动
go build -o build/design-cloud-engine ./cmd/engine
go build -o build/dc-launcher ./cmd/launcher

# 构建 Windows 节点 Agent（纯 Go，CGO_ENABLED=0，产物 build/design-cloud-engine.exe）
make win-agent

# 打安装包（engine + launcher 锁步；默认 RPM）
make package
```

---

## 单机部署

单机模式下，Server 和 Agent 在同一进程中运行，Agent 通过 loopback 连接本地 cluster gRPC 入口。

### 目录布局

```
/opt/design-cloud/
├── design-cloud-engine   # 主程序（0755 design-cloud）
└── dc-launcher           # 特权助手（0700 + setcap）

/etc/design-cloud/
├── config.toml           # ConfigDir：配置（0600；随包模板，%config(noreplace)）
├── dbpwd                 # 密钥（不随包；运维落盘 0600）
└── ldapbindpwd

/var/lib/design-cloud/    # DataDir：集群证书与持久状态（配置项 data_dir / Join 的 --data-dir）
/var/log/design-cloud/    # LogDir：应用/访问/审计日志
/run/design-cloud/        # RuntimeDir：PID flock、launcher token（开机重建）
```

> **ConfigDir ≠ BaseDir**：ConfigDir 是 FHS `/etc/design-cloud`。BaseDir 是 `dirname(--config)`（相对路径拼接根）。打包时二者数值常相同，概念仍分开。集群证书在 **DataDir**，不在 ConfigDir。

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
./design-cloud-engine
```

### 生产模式启动

```bash
# 生产启动（配置文件已就绪，topology 定义了节点角色）
./design-cloud-engine --config /etc/design-cloud/config.toml
```

---

## 分布式部署

### Server 节点

1. **编写共享 `config.toml`**，在 `cluster.servers` 中定义 Server 节点；纯计算节点不需列出，程序会根据本机 hostname
   自动识别角色。
2. **Cluster 证书**。Engine 启动时若检测到 CA 不存在会**自动生成**自签名根证书（`certs/cluster/ca.crt` + `certs/cluster/ca/ca.key`）。单
   Server 部署无需手动操作。如需使用企业内部 CA 或自定义证书，可在首次启动前手动放置（证书目录须遵循权限矩阵：`certs/cluster` 0755、`ca.crt` 0644、`ca/` 及节点目录 0700、私钥 0600）：
   ```bash
   # 示例：用 openssl 自签 CA 并为 server-01 签发节点证书
   # 生成 CA（公钥平铺、私钥嵌套私有目录）
   mkdir -p certs/cluster/ca certs/cluster/server-01
   chmod 0755 certs/cluster && chmod 0700 certs/cluster/ca certs/cluster/server-01
   openssl genrsa -out certs/cluster/ca/ca.key 4096
   openssl req -x509 -new -key certs/cluster/ca/ca.key \
     -days 3650 -out certs/cluster/ca.crt -subj "/CN=DCE Cluster CA"
   chmod 0644 certs/cluster/ca.crt && chmod 0600 certs/cluster/ca/ca.key

   # 为 server-01 签发证书（SAN 需包含节点主机名/IP）
   openssl genrsa -out certs/cluster/server-01/server-01.key 2048
   openssl req -new -key certs/cluster/server-01/server-01.key \
     -out certs/cluster/server-01/server-01.csr -subj "/CN=server-01"
   openssl x509 -req -in certs/cluster/server-01/server-01.csr \
     -CA certs/cluster/ca.crt -CAkey certs/cluster/ca/ca.key \
     -CAcreateserial -days 1095 -out certs/cluster/server-01/server-01.crt
   chmod 0644 certs/cluster/server-01/server-01.crt
   chmod 0600 certs/cluster/server-01/server-01.key
   rm -f certs/cluster/server-01/server-01.csr
   ```
3. **启动**：
   ```bash
   ./design-cloud-engine --config /etc/design-cloud/config.toml
   ```

### Agent 节点

Agent 节点接入集群并建立双向 mTLS 信任关系，系统支持以下三种规范场景：

> [!TIP]
> 关于节点注册机制、参数说明、三种交付模式与安全逃生舱规范的完整细节，请参阅专题文档：**[节点注册与引导](./node-registration.md)**。

#### 场景 A：共享存储部署（内置离线签发直接落盘，推荐）

在拥有集群共享存储（如 NFS，集群所有节点挂载同一 `certs/` 目录）的环境下，管理员在 Server 节点直接执行内置离线签发命令。命令自动读取集群 CA 签署节点证书，并在数据库中原子创建/更新节点绑定指纹，证书材料直接写入标准目录：

```bash
# 在 Server 节点上为 agent-01 签发证书（直接落盘到标准 DataDir/certs/cluster/）
./design-cloud-engine node issue-cert --hostname agent-01

# 若自定义 SAN（例如指定内外网 IP / 别名，缺省自动包含 <nodeID>,127.0.0.1,localhost）
./design-cloud-engine node issue-cert --hostname agent-01 --hosts "agent-01,10.1.1.102"
```

由于共享目录已挂载，`agent-01` 机器直接具备证书材料，无需做任何文件分发，配置启动后即可直接接入。

#### 场景 B：在线 OTP 自助注册（无共享存储）

节点凭一次性 token 自助向 Portal HTTP 服务领取证书材料，无需预先分发证书文件。适合无共享存储、节点与 Portal 网络连通的集群环境。

```bash
# 第一步：在 Server 节点上签发 token（自动创建 pending 节点记录 + 生成 24h 有效的 OTP；默认即为 Agent，若为复合节点则加 --server）
./design-cloud-engine node issue-token --hostname agent-01
```

输出示例：

```
  Node ID     : agent-01
  Token       : 8xK2mNpQrYtUvW3zA5bC7dE
  Created At  : 2026-07-02T18:00:00Z
  Expires In  : 24h0m0s (at 2026-07-03T18:00:00Z)

  Run the following command to retrieve node certificates:
    engine node register --token 8xK2mNpQrYtUvW3zA5bC7dE --portal-url https://<portal-host>:<port>
```

```bash
# 第二步：Agent 节点使用 token 注册并领取证书
# NodeID 默认严格取 os.Hostname()（大小写原样）；仅在“在 A 机为 B 机签发”时才需要 --hostname
./design-cloud-engine node register --token <token> --portal-url https://<portal-host>:<port> [--insecure-http] [--data-dir <dir>]
```

> **注意：**
> - `--portal-url` 与 `--token` **必填**；不读 `config.toml`，不会从拓扑推导 Portal 地址。
> - `--portal-url` 默认**强制使用 `https://`**，严防 OTP 令牌与私钥在网络中明文传输遭 MitM 拦截或 CA 被篡改。若在隔离开发环境使用明文 `http://`，需显式附加 `--insecure-http` 自担风险。
> - 在非共享存储（各自安装）环境下，此命令**必须**在真正的目标节点物理机上执行，并以 **Agent 服务账号**运行（节点目录为 0700），以便证书直接落盘到目标机。

注册成功后，证书自动落盘到（`--data-dir` 缺省为 `/var/lib/design-cloud`）：
- `certs/cluster/<hostname>/<hostname>.crt` — 节点证书（0644，位于 0700 节点目录内）
- `certs/cluster/<hostname>/<hostname>.key` — 节点私钥（0600）
- `certs/cluster/ca.crt` — 集群 CA 公钥（0644，平铺）

注册完成后 token 立即失效，Agent 使用证书通过 mTLS 接入集群。

#### 场景 C：离线脱机签发与介质分发（隔离区与镜像装机）

在计算节点与 Portal 网络物理隔离，或需要在装机前随镜像批量预置证书材料（PXE / Ansible）时，管理员在 Server 节点上脱机导出 `.tar.gz` 压缩包：

```bash
# 在 Server 节点生成脱机离线证书包（自动录入数据库 provisioned 状态与指纹）
./design-cloud-engine node issue-cert --hostname agent-01 --hosts "agent-01,10.1.1.102" --archive /tmp/agent-01.tar.gz
```

通过受控介质或运维分发渠道将压缩包推送到目标机解压至标准路径：

```bash
# 在目标节点 agent-01 上解包到标准证书目录
sudo mkdir -p /var/lib/design-cloud/certs/cluster
sudo tar -xzf /tmp/agent-01.tar.gz -C /var/lib/design-cloud/certs/cluster/
```

解压后证书、私钥与 CA 公钥自动放置到位，目标 Agent 启动后直接凭借绑定的证书以 mTLS 连接 Server。

<details>
<summary>附：底层 OpenSSL 手动签署参考（非必须）</summary>

若不使用内置 `node issue-cert` 命令，亦可手动通过 OpenSSL 为节点签署证书（需管理员手动通过 API 或 DB 录入证书指纹）：

```bash
mkdir -p certs/cluster/agent-01 && chmod 0700 certs/cluster/agent-01
openssl genrsa -out certs/cluster/agent-01/agent-01.key 2048
openssl req -new -key certs/cluster/agent-01/agent-01.key \
  -out certs/cluster/agent-01/agent-01.csr -subj "/CN=agent-01"
openssl x509 -req -in certs/cluster/agent-01/agent-01.csr \
  -CA certs/cluster/ca.crt -CAkey certs/cluster/ca/ca.key \
  -CAcreateserial -days 730 -out certs/cluster/agent-01/agent-01.crt
chmod 0644 certs/cluster/agent-01/agent-01.crt
chmod 0600 certs/cluster/agent-01/agent-01.key
rm -f certs/cluster/agent-01/agent-01.csr
```
</details>

#### Agent 配置

```toml
[cluster]
port = 52255
servers = ["server-01"]

[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
```

> Agent 会自动从 `Cluster.Servers` 和 `Cluster.Port` 构造连接地址，并按固定路径读取
> `certs/cluster/<hostname>/<hostname>.crt`、`certs/cluster/<hostname>/<hostname>.key` 与 `certs/cluster/ca.crt`
> （配置文件模式：根目录为 `data_dir`，空则回退 BaseDir；Join 模式：根目录为 `--data-dir`）。

#### Linux 节点 Agent 部署（install.sh 一键脚本，无配置文件）

Linux 计算节点不需要配置文件：先用 OTP 领取证书材料，再以无配置方式常驻。`deployment/install.sh`
的 `agent` 角色把这三步合成一条命令（建目录 → 落盘引擎 → `node register` → 生成并启动 systemd 服务）：

```bash
# 在主节点签发 OTP（默认 Agent 节点）
./design-cloud-engine --config ./config.toml node issue-token --hostname agent-01

# 在目标节点（root 执行安装，引擎本身以服务账号运行、拒绝 root）
sudo bash deployment/install.sh agent \
  --portal-url https://portal.example.com \
  --token <registration-token>
```

该脚本将自动执行：
1. 创建数据目录（缺省 `/var/lib/design-cloud`，含 `certs/cluster/`；日志在 `/var/log/design-cloud`，不在 data-dir）；
2. 落盘 `design-cloud-engine` 与 `dc-launcher`（含 setcap）；
3. 调用 `node register --portal-url ... --token ... --data-dir ...` 领取 TLS 证书材料（节点密钥本地生成、不外发）；
4. 安装 canonical `design-cloud-engine.service`，再写入 drop-in 覆盖 `ExecStart` 为 configless（`--servers` / `--port` / `--data-dir` / `--log-dir`），并 `systemctl enable --now`。

> 可选参数：`--servers "mgt01,mgt02"`（缺省取 `--portal-url` 的主机名）、`--port 52255`、
> `--log-dir`、`--log-level INFO`、`--data-dir`、`--install-dir`、`--service-user`、`--service-name`；
> 证书材料已就位（离线/共享目录交付）时用 `--skip-register`。

启动成功的判据：节点侧 `journalctl -u <service> | grep -E 'Starting AGENT|Successfully registered'`，
集群 Server 侧出现 `Certificate fingerprint verified` → `Node marked online in DB`。

#### Windows 节点 Agent 部署（PowerShell 一键脚本）

Windows 节点作为受管图形工作站接入时，推荐使用官方部署脚本 `deployment/install-win-agent-configless.ps1` 自动化完成注册与系统服务安装（同样无需 `config.toml`）。

在 Server 签发 Token 后，在 Windows 目标机以**管理员身份打开 PowerShell** 执行：

```powershell
# 一键部署（configless：无需准备 config.toml；NodeID 由引擎严格取 os.Hostname()）
powershell -ExecutionPolicy Bypass -File .\deployment\install-win-agent-configless.ps1 `
  -PortalUrl "https://portal.example.com" `
  -Token "<registration-token>"
```

该脚本将自动执行：
1. 规范化目录至 `%PROGRAMDATA%\DesignCloudEngine\`（含 `certs\`、`logs\`）；
2. 校验并落盘 `design-cloud-engine.exe`；
3. 调用 `node register --portal-url ... --data-dir ...` 领取 TLS 证书材料；
4. 调用 `service install --servers ... --data-dir ... --log-dir ... --log-level ...` 将引导参数写入 SCM 启动命令行，并 `service start` 立即启动。

> 可选参数：`-Servers "mgt01,mgt02"`（缺省取 Portal 主机名）、`-Port 52255`、`-LogLevel INFO`、`-DataDir`、`-InstallDir`。

> **节点 ID**：安装脚本默认使用与 Agent 运行时相同的主机名（Windows `GetComputerNameEx(ComputerNamePhysicalDnsHostname)`，即 Go `os.Hostname()`）。这通常**不等于** `$env:COMPUTERNAME`（NetBIOS，多为全大写）。签发 OTP 时 `--hostname` 必须与该值**逐字一致**。可在目标机上先跑一次安装脚本（它会在参数校验后打印 `Node ID`），或在 PowerShell 中用同一 API 读取后再签发。不要手工 `ToLower`。原地升级会先停止并卸载已有服务，再覆盖二进制，避免文件占用导致复制失败。

### 多 Server 节点（高可用）

所有 Server 节点**必须共享同一套 CA**，Agent 才能被任意 Server 节点信任。配置和单 Server 完全相同，差异仅在于：

1. CA 私鑰 `ca.key` 须通过安全渠道手动分发到每台 Server（私钥不走网络自动同步）
2. `cluster.servers` 必须列出所有 Server

```bash
# 1. 主节点初始化 CA 并签发自己的证书（用系统 PKI 工具，如 openssl）
# 参见上方"Server 节点"步骤

# 2. 将 CA（含私鑰）复制到其他 Server 节点 DataDir（ca.crt 平铺，ca.key 入私有子目录）
scp certs/cluster/ca.crt server-02:/var/lib/design-cloud/certs/cluster/
ssh server-02 'mkdir -p /var/lib/design-cloud/certs/cluster/ca && chmod 0700 /var/lib/design-cloud/certs/cluster/ca'
scp certs/cluster/ca/ca.key server-02:/var/lib/design-cloud/certs/cluster/ca/

# 3. 在 server-02 上签发自己的证书（CA 已存在，直接使用；集群 Server 证书 3 年）
mkdir -p certs/cluster/server-02 && chmod 0700 certs/cluster/server-02
openssl genrsa -out certs/cluster/server-02/server-02.key 2048
openssl req -new -key certs/cluster/server-02/server-02.key \
  -out /tmp/server-02.csr -subj "/CN=server-02"
openssl x509 -req -in /tmp/server-02.csr \
  -CA certs/cluster/ca.crt -CAkey certs/cluster/ca/ca.key \
  -CAcreateserial -days 1095 -out certs/cluster/server-02/server-02.crt
chmod 0644 certs/cluster/server-02/server-02.crt && chmod 0600 certs/cluster/server-02/server-02.key
```

多台 Server 可共用同一份配置文件，只需要声明统一拓扑：

```toml
[cluster]
servers = ["server-01", "server-02"]
```

#### Agent 连接策略（Strict Ordered Failover）

Agent 按照 `Cluster.Servers` 列表顺序连接。主 Server 不可达时依次尝试备用 Server，恢复后自动重连回主节点。

#### 后台任务与 DB Lease

多 Server 环境下，部分后台任务通过数据库租约（lease）保证同时只有一台 Server 执行。

所有后台循环及缓存失效机制的完整清单，见 [`docs-site/docs/development/background-tasks.md`](../development/background-tasks.md)。

Connection 清理由管理员通过 API 手动触发（`connection_cleanup` lease 仅用于多 Server 互斥），不在后台自动执行。

#### 多 Server 下的节点状态语义

当 Agent 连接到不同的 Server 时，各 Server 的内存连接状态可能不一致，但节点资源上报与心跳都会持续写入数据库。

- `GET /api/admin/system/nodes/:id/status` 返回的是数据库中的最近一次 `ResourceReport` 快照（`/api/nodes` 公开端点仅提供注册用 `POST /:id/registration`，无查询接口）
- 节点在线状态最终仍以数据库中的 `last_heartbeat_at` 与状态调和结果为准
- `last_seen_server` 这类字段属于内部节点表状态，不是节点查询接口的主返回模型
- health checker 发现 stale agent 连接后，会先将内存连接移除，并尽快把该节点同步为 DB offline；周期调和仍作为兜底机制保留

#### 多 Server 下的执行语义补充

- standby 收到命令面请求时，会转发给当前 leader 执行
- 若 server 停机或目标 agent 断连，挂起中的 dispatch 请求会尽快失败返回，而不是只等待超时

#### 故障排查

```bash
# 检查所有 Server CA 是否一致
for s in server-01 server-02; do
  echo -n "$s: "; ssh $s "openssl x509 -in /var/lib/design-cloud/certs/cluster/ca.crt -noout -fingerprint"
done

# 查看 DB lease 状态
psql -h db.example.com -U dc -d dc -c "SELECT task_name, owner_id, expires_at FROM task_leases;"

# 手动释放卡平的 lease（最后战术）
psql -h db.example.com -U dc -d dc -c "DELETE FROM task_leases WHERE task_name IN ('session_cleanup', 'connection_cleanup');"
```

---

## TLS 证书配置

Engine 使用两套独立的 TLS 证书：

| 证书           | 用途                        | 配置路径              | 签发方式                   |
|--------------|---------------------------|-------------------|------------------------|
| Portal HTTPS | GUI / Portal API HTTPS 访问 | `portal.cert/key` | `gen-tls-files.sh`     |
| Cluster mTLS | Server ↔ Agent 双向认证       | 固定路径约定            | openssl / 内部 CA / 手动管理 |

### Portal HTTPS 证书

**开发调试**：监听 `localhost` 时，可使用受信任的本地开发证书或自签名 HTTPS。

**本地开发**：使用 `mkcert`，它会自动将 Root CA 安装到系统信任库：

```bash
mkcert -install
mkcert -key-file certs/portal/portal.key -cert-file certs/portal/portal.crt dc.cloud.hpc localhost 127.0.0.1
```

**部署环境（内网/生产）**：编辑 `deployment/gen-tls-files.sh` 顶部的变量配置，然后运行：

```bash
# 1. 编辑 gen-tls-files.sh，设置以下变量：
#    out_dir        → 输出目录（应与 config.toml 中 `portal` 的路径一致）
#    host_array     → 实际访问的域名和 IP 地址列表
#    organizationName → 你的组织名称
#    ca_prefix / signed_prefix → 对应 config.toml 中的文件名

# 2. 运行生成脚本
cd deployment/
./gen-tls-files.sh
```

生成的文件对应 `config.toml` 中的路径：

```toml
[portal]
tls = true
cert = "certs/portal/portal.crt"
key = "certs/portal/portal.key"
```

> 注意：配置文件键名严格小写（解析器开启 `DisallowUnknownFields`，未知键会导致启动失败）；
> 相对路径基于 **BaseDir**（`dirname(--config)`）解析；集群证书基于 **DataDir**（`data_dir`，空则回退 BaseDir）。

> 使用私有 CA 时，将 `portal.ca.crt` 分发给 GUI 客户端所在系统，或通过企业 CA 管理工具下发。

### Cluster mTLS 证书

使用系统 PKI 工具（如 `openssl`）管理，见 [分布式部署](#分布式部署) 章节。

**证书文件布局**（根目录为 DataDir，默认打包 `/var/lib/design-cloud`）：

```text
<DataDir>/certs/cluster/
├── ca.crt              # CA 公钥 — 分发给所有节点 (0644)
├── ca/
│   └── ca.key          # CA 私钥 — 仅 Server 保留 (0600)
└── <hostname>/
    ├── <hostname>.crt
    └── <hostname>.key
```

**证书路径约定**：Engine 从 `<DataDir>/certs/cluster/...` 读取（配置模式看 `data_dir`；Join 看 `--data-dir`）。

---

## Launcher 特权组件配置

`dc-launcher` 是 Agent 调用的特权执行助手（`cap_setuid/setgid`），用于以目标用户身份执行命令（如用户认证、文件操作）。生产环境中的
Agent 节点必须配置 Launcher。

推荐特权模型如下：

- **Engine 继续以普通服务用户运行，不以 root 常驻运行**
- **Launcher 不使用 setuid-root，而是使用 `cap_setuid,cap_setgid` file capabilities**
- **密码相关少量 privileged action 在 launcher 内显式切换到 root 后执行**
- **其余 action 一律切换到目标普通用户执行**

详细安全规范参见 [启动器安全](../development/launcher/security.md)。

### 一键打包与 RPM 部署（生产主路径）

每台机器安装**同一份** `design-cloud-engine` RPM（不分角色）。角色在进程启动后由 hostname 对 `cluster.servers` 解析。engine 与 launcher 版本锁步，避免静默协议错位。

```bash
make package                    # 正式包，默认 PKG_TYPE=rpm GOARCH=amd64
# make package-dev              # 开发快照（版本带 git hash）
# GOARCH=arm64 make package     # 交叉打 arm64 包（gen-config 仍在本机构建）
yum install -y build/packages/design-cloud-engine-*.rpm

# 编辑配置与密钥文件后启动（包不会自动 enable）
vi /etc/design-cloud/config.toml
/opt/design-cloud/design-cloud-engine check-config --config /etc/design-cloud/config.toml
systemctl enable --now design-cloud-engine
```

脚本职责拆分：

- **`%pre`**：创建 `design-cloud` 用户/组（若尚无；允许 IPA 预置同名账号）
- **`%post`**：重跑 `setcap`、收口已知数据目录属主、应用 tmpfiles
- **`%posttrans`**：`daemon-reload`；仅当 unit 已 active 时 `try-restart`（**不** `enable`）

已改过的 `config.toml` 走 `%config(noreplace)` / `.rpmnew`。

**configless 计算节点**（避免把含库/LDAP 字段的 toml 摊到所有节点）：同一 RPM → `node register` →
`systemctl edit design-cloud-engine` 按包内
`/usr/share/doc/design-cloud-engine/systemd/configless.conf.example` 覆盖 `ExecStart`。

无包/源码树可用 `deployment/install.sh server|agent`（拷贝同一份 canonical unit）。

> 二进制必须落在本地盘。不要把 engine 或 launcher 放在 NFS 上执行（capability xattr 与 systemd 执行语义均不支持）。

### 手动部署步骤

如果不使用 RPM 包，可以参照以下步骤手动在**节点本地磁盘**安装：

```bash
groupadd -r design-cloud || true
useradd -r -M -g design-cloud -s /sbin/nologin design-cloud || true

install -m 755 build/design-cloud-engine /opt/design-cloud/design-cloud-engine
install -m 700 build/dc-launcher /opt/design-cloud/dc-launcher
chown design-cloud:design-cloud /opt/design-cloud/design-cloud-engine /opt/design-cloud/dc-launcher

setcap "cap_setuid,cap_setgid+ep" /opt/design-cloud/dc-launcher
getcap /opt/design-cloud/dc-launcher
```

说明：

- 不要把整个 Engine 进程改为 root 运行；Launcher 的意义就在于把高权限窗口收敛到更小的二进制与更少的 action 上。
- 当前推荐模型不是 setuid-root，而是 file capabilities + launcher 内部显式身份切换。
- **[重要提示] Launcher 必须部署在节点的本地磁盘文件系统上。** 不要将其放在 NFS 等网络共享目录中运行。
- 生产环境 PID/token 落在 `/run/design-cloud`（systemd `RuntimeDirectory=`）；源码树回退到 `/run/user/<uid>/...` 或 `/tmp/...`。
- 运行目录由 RPM tmpfiles.d / 程序统一创建与管理。

然后在 `config.toml` 中配置：

```toml
[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
```

---

## 服务管理

### 使用 systemd（推荐用于生产）

```bash
# 1. 复制 service 文件
cp deployment/design-cloud-engine.service /etc/systemd/system/

# 2. 按实际路径编辑 WorkingDirectory 和 ExecStart
# 注意：如果 Engine 需要绑定 80/443 端口，.service 文件中已默认开启 AmbientCapabilities=CAP_NET_BIND_SERVICE
vi /etc/systemd/system/design-cloud-engine.service

# 3. 启动
systemctl daemon-reload
systemctl enable design-cloud-engine
systemctl start design-cloud-engine
systemctl status design-cloud-engine
```

> 该样例 Server 与 Agent **共用一份** canonical unit（`ExecStart` 为 `--config` 形态）。configless
> 计算节点不要改 unit 本体：用 `systemctl edit` / drop-in 覆盖 `ExecStart`（示例见包内
> `configless.conf.example`）。`install.sh agent` 会拷贝同一份 unit 并自动写入 drop-in。

### 使用内置管理脚本（`run.sh`）

`deployment/run.sh` 提供进程级别的管理（适合无 systemd 环境）：

```bash
./run.sh start
./run.sh stop
./run.sh status
./run.sh log      # 跟踪日志
```

### Windows 服务管理（SCM）

Windows 节点 Agent 原生支持以 Windows 服务常驻运行（基于 SCM，支持开机自启与异常重启）：

```powershell
# 以管理员权限打开 PowerShell

# 1. 注册并安装为系统服务（--config 是顶层 flag，必须写在子命令之前）
.\design-cloud-engine.exe --config C:\ProgramData\DesignCloudEngine\config.toml service install

# 2. 启动服务
.\design-cloud-engine.exe service start

# 3. 检查服务运行状态
.\design-cloud-engine.exe service status

# 4. 停止 / 卸载服务
.\design-cloud-engine.exe service stop
.\design-cloud-engine.exe service uninstall
```

### Windows 节点运行约定

- **RDP 前置条件**：节点需启用远程桌面（注册表 `fDenyTSConnections=0`）且 `TermService` 处于运行状态；Agent 按"注册表策略 + 服务状态 + 本地端口握手"**三级同时满足**判定就绪性，判定不可靠时不上报，服务端保持既有状态。客户端到节点的入向 RDP 端口（注册表 `WinStations\RDP-Tcp\PortNumber`，默认 `3389`）需在防火墙放通。
- **数据目录与 ACL**：运行时状态、证书与日志统一落在 `%PROGRAMDATA%\DesignCloudEngine`。引擎写目录和私密文件时会切断父目录继承，把 Owner 设为当前进程账户，并只允许 `SYSTEM`、`Administrators` 与当前进程账户持有读、写和控制权限（等价于 Linux 的 `0700`/`0600`）。配置或私钥对其他账户只读也会被拒绝；权限过宽时启动会尝试收紧，失败则直接报错。
- **运行账户**：以 Windows 服务方式常驻（安装需管理员权限，默认 LocalSystem）。数据目录 ACL 只放行 `SYSTEM`/`Administrators`；若改用其他服务账户，需自行授予该账户对数据目录与配置文件的访问权。
- **磁盘监控口径**：`monitor.mounts` 的服务端默认值是 Linux 口径的 `"/"`。Windows 节点在"仅默认根挂载"时会自动展开为全部固定本地盘符（`C:\`、`D:\`…）；显式配置 `mounts` 时以配置为准。
- **阶段一能力边界**：Windows 节点仅注册 `LICENSE_GET_STATUS` / `LICENSE_LIST_FEATURES` 两个直接执行动作（系统监控与许可证查询）。本地用户/组管理、Slurm/LSF 调度以及 Linux 专属文件操作在 Windows 上不可用，请求会返回 `action not supported on current platform`。Portal **尚未**按节点 `os` 灰置不支持的操作，调用方需自行按 `os` 收敛展示。
- **许可证探测 CLI 依赖**：许可证运行状态与 Feature 查询在目标 Agent 节点以 Direct 动作通过裸名调用 `lmstat` 执行，依赖 Agent 进程环境中的 PATH 解析。Linux 节点需确保 `lmstat` 处于系统 PATH 中；Windows 节点若仅安装了 `lmutil.exe`（无独立 `lmstat.exe`），需确保 PATH 中存在名为 `lmstat.exe` 的可执行文件（或复制/建立符号链接）。
- **节点能力可判别**：Agent 注册时上报的 `os` 会落到节点记录并经 `GET /api/admin/system/nodes`（及节点详情）返回，Portal 可据此收敛对 Windows 节点展示的操作。


### 常用运维命令

```bash
# 查看运行状态
./design-cloud-engine service status

# 优雅停止（等待正在执行的任务完成）
./design-cloud-engine service stop

# 裸跑场景如需重启：先停止，再重新执行原启动命令
# 例如：
# ./design-cloud-engine service stop
# ./design-cloud-engine --config /etc/design-cloud/config.toml

# 查看版本
./design-cloud-engine --version

# 生成默认配置
./design-cloud-engine gen-config

# 在主节点上为 agent-01 签发 OTP token（自动创建 pending 节点记录）
./design-cloud-engine node issue-token --hostname agent-01

# 方式二：节点通过 OTP 注册领取 cert/key/ca（在目标节点上执行；--hostname 仅用于"为别的机器签发"）
./design-cloud-engine node register --token <token> --portal-url https://<portal-host>:<port> [--data-dir <dir>]
```

---

## 常见问题

### 配置文件权限错误（`!!! INSECURE`）

Engine 启动时会检查 `config.toml` 权限，过于宽松则拒绝启动：

```bash
chmod 600 config.toml
```

### Agent 连接失败 `certificate signed by unknown authority`

Agent 本地使用的 cluster CA 证书与 Server 使用的 cluster CA 不一致：

```bash
# 确认两边 CA 是同一个文件
diff /on/agent/certs/cluster/ca/ca.crt /on/server/certs/cluster/ca/ca.crt
```

### Agent 连接时被应用层拒绝（TLS 正常但被踢出）

证书指纹未在 Server 数据库注册。通过 Admin API 完成 Agent 注册。

### GUI / API 访问提示证书不受信任

将生成的 `portal.ca.crt` 安装到操作系统的受信任 CA 证书列表中。若在本地开发，改用 `mkcert` 可避免此步骤。

### 验证证书内容

```bash
# 查看证书详情（包括 SAN）
openssl x509 -in certs/portal/portal.crt -text -noout | grep -A5 "Subject Alternative Name"

# 验证证书链
openssl verify -CAfile certs/portal/portal.ca.crt certs/portal/portal.crt

# 测试 HTTPS 连接
curl -v --cacert certs/portal/portal.ca.crt https://dc.cloud.hpc
```
