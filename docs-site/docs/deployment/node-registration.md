---
title: 节点注册与引导
linkTitle: 节点注册
weight: 90
description: 节点 OTP 注册与证书材料下发流程
---

本文档描述 Agent 节点首次接入集群时的注册流程，涵盖以下步骤：

1. 管理员创建节点白名单并签发一次性 OTP token
2. 节点使用 token 领取证书材料（cert/key/ca）
3. 节点本地落盘后，以已登记证书通过 mTLS 接入 cluster

> 对大多数内部可信集群，优先推荐 Server 统一签发证书 + 自动注册的方式：
> Server 直接签发节点证书，通过共享目录或受控分发获取证书文件（见[部署指南](./guide.md)）。
> 本文覆盖的是基于 OTP token 自助注册的方式，适合无需共享存储的场景。

## 设计目标

- 节点首次入群只需要一次性 token，不需要预先分发节点证书
- 长期信任关系建立在共享 cluster CA 与数据库中的证书绑定之上
- token 只用于注册，下发成功后立即失效
- 节点端一次请求即可拿到启动所需的 `cert/key/ca/serverAddr`

## 参与对象

- `Node`：数据库中的节点记录，包含 `node_id`、角色标记、状态、证书信息和注册 token
- `registration token`：一次性随机 token（24h 过期），用于首次领取证书材料
- `registration materials`：Server 返回给节点的 `cert.pem`、`key.pem`、`ca.pem` 与 `serverAddr`
- `cluster certificate`：后续 mTLS 接入使用的节点证书

## 注册流程

### 第一步：签发 token

**方式 A：HTTP API（需管理员登录）**

创建节点白名单记录（自动生成 token）：

```http
POST /api/admin/system/nodes
Content-Type: application/json
Authorization: Bearer <admin-token>

{"nodeId": "agent-01", "isServer": false}
```

如需轮换 token：

```http
POST /api/admin/system/nodes/agent-01/tokens
Authorization: Bearer <admin-token>
```

**方式 B：CLI（主节点本地操作 DB，免登录）**

```bash
# 默认创建纯 Agent 计算节点（若创建复合管理节点则加 --server）
./design-cloud-engine node issue-token --hostname agent-01
```

节点不存在时自动创建 pending 记录并生成 token；已存在时轮换 token。

**token 规则：**

- token 为一次性凭据，用于首次领取证书材料
- 新 token 会替换旧 token
- 有效期 24 小时
- token 只用于领取证书材料，不参与后续 cluster 长连接

### 第二步：节点注册并领取证书材料

**方式 A：HTTP API**

```http
POST /api/nodes/:nodeID/registration
Content-Type: application/json

{"token": "<registration-token>"}
```

**方式 B：CLI**

```bash
./design-cloud-engine node register --portal-url https://<portal-host>:<port> --token <registration-token> [--insecure-http] [--data-dir <dir>] [--hostname <nodeID>]

# --portal-url / --token：必填；不读 config.toml
# --portal-url 默认强制要求 https:// 协议；若在隔离测试环境使用明文 http://，必须显式附加 --insecure-http 自担中间人窃听与篡改风险
# --hostname <nodeID>：仅用于「在 A 机为 B 机签发」；缺省严格取 os.Hostname()（大小写原样）
# --data-dir：证书基准目录；省略则用平台缺省
```

该命令封装了上述 HTTP 调用，并将返回的证书材料自动落盘。在发送 HTTP 请求前，命令会**预先检查** cert、key、CA
三个目标目录的可写性，任一目录不可写则立即报错退出，避免浪费 OTP token。

> [!IMPORTANT]
> **传输层安全强制约束 (HTTPS 强制与 `--insecure-http` 豁免)**：
> 节点注册交互时，请求上行携带一次性 OTP 令牌，响应下行返回签发的节点私钥与集群根 CA 证书。为防止网络中间人（MitM）窃听令牌或篡改 CA 证书劫持集群，`node register` 与自动化安装脚本默认**强制要求 `--portal-url` 使用 `https://` 协议**。若误用明文 `http://`，命令将打印高危安全告警并阻断退出。仅在受信任的隔离开发/测试沙箱中，才允许显式附加 `--insecure-http`（或环境变量 `DCE_INSECURE_HTTP=1`）由用户自担风险继续。

> **注意：**
> - 在非共享存储（各自安装）的环境下，**必须在真正的目标节点物理机上执行此命令**。
> - 在共享存储（如 NFS，集群所有节点挂载同一 `certs/` 目录）的环境下，您可以在任何可以访问共享存储的节点执行此命令，生成的证书会直接写入共享目录中供目标节点使用。

**方式 C：Linux 安装脚本（`install.sh agent`，一条命令完成领证 + 服务化）**

```bash
sudo bash deployment/install.sh agent \
  --portal-url https://<portal-host>:<port> \
  --token <registration-token> \
  [--servers "mgt01,mgt02"] [--port 52255] \
  [--data-dir /var/lib/design-cloud] [--skip-register]
```

脚本会创建数据目录、落盘引擎与 launcher、调用 `node register` 领证，安装 canonical unit 并写入
configless drop-in 后 `systemctl enable --now`；`--skip-register` 用于证书材料已就位（离线/共享目录交付）的场景。
若本机已有同名服务，替换二进制前会先停服（否则旧进程持有已删除的旧 inode，升级会静默失效）。
判据：节点侧日志 `Starting AGENT` → `Successfully registered with server`，集群 Server 侧
`Certificate fingerprint verified` → `Node marked online in DB`。

**方式 D：Windows PowerShell 脚本（自动化领证与服务安装）**

在 Windows 节点上，推荐使用内置 PowerShell 部署脚本一站式完成 OTP 领证与服务化配置。
与 Linux 节点一致，Windows 计算节点同样**无需 `config.toml`**：引导参数全部由服务命令行承载。

```powershell
powershell -ExecutionPolicy Bypass -File .\deployment\install-win-agent-configless.ps1 `
  -PortalUrl "https://portal.example.com" `
  -Token "<registration-token>"
```

可选参数：`-Servers`（集群 Server 列表，缺省取 `-PortalUrl` 的主机名）、`-Port`（集群 gRPC 端口，
缺省 52255）、`-DataDir`（证书基准目录，缺省 `%PROGRAMDATA%\DesignCloudEngine`）、
`-LogDir` / `-LogLevel`，以及 `-SkipRegister`（证书材料已就位时跳过领证，仅做原地升级）。

脚本会先按 Physical DNS 主机名（与运行时 `os.Hostname()` 相同，通常不等于 `$env:COMPUTERNAME`）向注册接口领取证书，材料保存在 `%PROGRAMDATA%\DesignCloudEngine\certs\`。若本机已注册同名服务，会先停止并卸载，再覆盖二进制、重新安装并启动。签发 OTP 时 `--hostname` 必须与该主机名逐字一致。已引导节点要重新领证，须先由管理员再次签发 token。

**方式 E：离线脱机签发与分发（`node issue-cert`，针对隔离区与自动化批量装机）**

在计算节点无法访问 Portal Web 端口（网络物理隔离区），或者需要在装机前随镜像批量预置证书材料（PXE / Ansible）时，管理员可直接在 Server 节点上执行脱机签发。该命令直接读取本地 CA 私钥签署证书并原子录入数据库指纹，目标节点无需执行 HTTP 注册：

```bash
# 模式 1：导出为 tar.gz 归档（最推荐：方便通过介质或 Ansible 批量推送到隔离节点）
./design-cloud-engine node issue-cert --hostname compute-01 --archive /tmp/compute-01.tar.gz

# 模式 2：导出到指定离线目录
./design-cloud-engine node issue-cert --hostname compute-01 --out-dir /mnt/usb/certs/

# 模式 3：直接落入 Server 的标准集群目录（适合 NFS 共享存储：直接落盘即交付）
./design-cloud-engine node issue-cert --hostname compute-01

# 提示：若非当前目录下的 config.toml，可通过 --config 指定配置文件路径，如：
# ./design-cloud-engine --config /etc/design-cloud/config.toml node issue-cert --hostname compute-01 ...
# 或：./design-cloud-engine node issue-cert --config /etc/design-cloud/config.toml --hostname compute-01 ...
```

在目标节点上，解压材料至 `<data-dir>/certs/cluster/`（缺省 `/var/lib/design-cloud/certs/cluster/`）并启动 Agent 即可直接上线：

```bash
sudo tar -xzf compute-01.tar.gz -C /var/lib/design-cloud/certs/cluster/
sudo bash deployment/install.sh agent --servers "mgt01,mgt02" --skip-register
```

**成功响应（HTTP API）：**

```json
{
  "data": {
    "nodeId": "agent-01",
    "serverAddr": "server-01:50051",
    "certPem": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
    "keyPem": "-----BEGIN EC PRIVATE KEY-----\n...\n-----END EC PRIVATE KEY-----\n",
    "caPem": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
    "certExpiresAt": "2027-03-25T00:00:00Z"
  }
}
```

**接口语义：**

- `node_id` 必须已存在
- token 必须匹配且未过期（常量时间比较，防时序攻击）。有效 token 即授权签发或重新签发，不要求节点仍为 `pending`、也不要求尚未绑定证书
- 管理员对已引导节点再次 `issue-token` 后，节点可凭新 token 重新领取证书；成功后旧证书绑定被新指纹覆盖，token 立即清空
- Server 成功签发证书后，把证书指纹、主题和到期时间写入节点记录
- 该接口本身不会把节点状态改为 `online`（见下文）

### 第三步：节点本地落盘并启动

CLI 自动将证书写入标准规范路径（基准目录 `--data-dir`，缺省 Linux 为 `/var/lib/design-cloud`，Windows 为 `%PROGRAMDATA%\DesignCloudEngine`）：

- `certs/cluster/<hostname>/<hostname>.crt` — 节点证书（权限 0644，置于 0700 节点专属私有目录）
- `certs/cluster/<hostname>/<hostname>.key` — 节点私钥（权限 0600，私钥严格受限）
- `certs/cluster/ca.crt` — 集群根 CA 公钥（权限 0644，平铺于共享目录）

HTTP API 用户需自行规范落盘路径与权限，确保私钥仅 Agent 运行账户可读。

证书就绪后启动 Engine，Agent 使用该证书通过 mTLS 建立 `ControlService/Sync` 连接，Server 验证指纹一致后标记节点为 `online`。

## 注册成功后的状态

- 节点记录保存 `cert_fingerprint`、`cert_subject`、`cert_expires_at`
- 注册 token 已清空
- 节点状态变为 `provisioned`（已领证，等待 Agent 首次上线）；不是仍停在 `pending`，也不是已经 `online`
- 节点本地已具备启动 Agent 所需的全部 TLS 材料

节点不会因为注册 API 成功而变成 `online`。后续 Agent 使用已签发证书建立 `ControlService/Sync` 连接后，Server 才会调用
`MarkOnline()` 推进状态；之后由心跳持续更新 `last_heartbeat_at` / `last_seen_server`。

## 证书生命周期管理与在线自动续签（L3）

集群内节点证书生命周期完全自动化运转，支持在线无感续签与零停机热重载：

```mermaid
sequenceDiagram
    participant S as Server (巡检后台任务)
    participant DB as 数据库 (nodes)
    participant A as Agent 节点

    Note over S,DB: 1. 定期扫描 cert_expires_at 临期节点 (T-90/30/7 告警)
    S->>A: 2. 下发 RenewCertCommand (主动请求续签)
    Note over A: 3. 本地生成全新私钥与 CSR (私钥永不离机)
    A->>S: 4. 上送 RenewCertRequest(csr)
    Note over S: 5. 校验 CN/SAN，签发新证书，更新 DB 指纹 (双指纹窗口)
    S->>A: 6. 回传 RenewCertResponse(cert, ca)
    Note over A: 7. 原子落盘新证书与私钥
    A->>S: 8. 汇报 RenewCertResult(ok) 并以新证书发起重连
    Note over S: 9. 握手校验新指纹，自愈同步最新有效期与 Subject
```

### 1. 到期巡检与阶梯告警
- 由 Server 端的集群定时任务循环驱动（受分布式租约保护，确保多 Server 下单点执行）；
- 以数据库中的 `nodes.cert_expires_at` 为单一事实源，避免遍历磁盘文件；
- 阶梯式告警：当证书剩余有效期进入 **T-90天**（Warn）、**T-30天**（Warn）、**T-7天**（Error）时触发告警，并通过 `nodes.cert_alert_stage` 抑制重复报警。

### 2. 在线 CSR 自动续签
- **私钥永不出机**：续签时 Agent 在本地生成新私钥并构造 CSR，私钥不经过网络传输；
- **在线即时交付**：通过已建立的 gRPC 双向流（`ControlService/Sync`）完成 CSR 上报与证书回传；
- **全自动热生效**：Agent 接收并落盘新证书后，通过底层连接重连无感切换至新证书，不中断正在运行的后台作业与会话，无需人工干预或重启节点服务。

### 3. 双指纹容错与握手自愈
- **双指纹窗口（Current + Prev）**：换证期间，服务端同时信任 `CertFingerprint` 与前一版本 `CertFingerprintPrev`，完全消除换证切换与网络抖动时的断连风险；
- **元数据握手自愈**：mTLS 握手通过后，服务端从 PeerCertificate 提取最新 `Subject` 与 `NotAfter` 自动回写同步至数据库。

### 4. 监听侧零重启热重载（Hot Reload）
- Server 控制面监听采用基于内容哈希的 `certReloader`，通过 Go 标准库 `tls.Config.GetCertificate` 回调实现动态加载；
- 磁盘上的 Server 证书文件一旦更新，下一次客户端握手即自动启用新证书，**服务端守护进程完全免重启**。

## 失败场景

| 场景              | HTTP 状态          | 说明              |
|-----------------|------------------|-----------------|
| token 不匹配       | 401 Unauthorized | 不签发证书，不写入任何元数据  |
| token 已过期       | 409 Conflict     | 需要管理员重新签发 token |
| 未签发可用 token     | 409 Conflict     | 已引导节点要重新领取证书，须先再次签发 token |
| 节点不存在           | 404 Not Found    | 需先创建节点记录        |

## 相关文档

- [部署指南](./guide.md)
- [集群协议](../development/cluster/cluster-protocol.md)
- [Portal API](../api)
