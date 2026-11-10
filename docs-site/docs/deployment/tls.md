---
title: TLS 证书配置
linkTitle: TLS 证书
weight: 40
description: Portal HTTPS 证书、Cluster mTLS 证书与客户端 CA 注入
---

Engine 使用两套独立的 TLS 证书：

| 证书           | 用途                        | 配置路径              | 签发方式                   |
|--------------|---------------------------|-------------------|------------------------|
| Portal HTTPS | GUI / Portal API HTTPS 访问 | `portal.cert/key` | `dc-engine cert gen-portal` |
| Cluster mTLS | Server ↔ Agent 双向认证       | 固定路径约定            | `engine node issue-cert` / 自动 OTP 引导 |

## Portal HTTPS 证书

**开发调试**：监听 `localhost` 时，可使用受信任的本地开发证书或自签名 HTTPS。

**本地开发**：使用 `mkcert`，它会自动将 Root CA 安装到系统信任库：

```bash
mkcert -install
mkcert -key-file certs/portal/portal.key -cert-file certs/portal/portal.crt dc.cloud.hpc localhost 127.0.0.1
```

**部署环境（推荐：Engine 内置 Go 命令）**：无需依赖外部 openssl，跨平台直接生成 ECDSA (P-256) 或 RSA 自签 CA 与服务端证书：

```bash
# 默认生成 ECDSA 证书（支持指定多域名与 IP，自动输出 SHA-256 指纹）：
./dc-engine cert gen-portal --hosts "dc.cloud.hpc,10.0.0.10,localhost,127.0.0.1" --out-dir conf/certs/portal
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

### Windows 客户端在线安装器注入 (PE Overlay)

为使内网 Windows 客户端开箱即用且免受证书报警干扰，可通过内置命令将自签 CA 证书动态注入在线安装器（纯 Go PE Overlay 机制，跨平台毫秒级生成）：

```bash
# 1. 编译 Windows 在线安装器模板（或使用发布的预编译模板）：
make desktop-installer   # 输出至 build/dc-desktop-installer.exe

# 2. 将本集群的 CA 证书与服务端访问地址注入安装器：
./dc-engine cert inject-installer \
  --ca conf/certs/portal/portal.ca.crt \
  --input build/dc-desktop-installer.exe \
  --output build/DesignCloudDesktop-Installer.exe \
  --server-url https://dc.cloud.hpc:1234
```

用户在 Windows 上双击 `DesignCloudDesktop-Installer.exe` 时，将自动将嵌入的 CA 证书导入系统「受信任的根证书颁发机构（ROOT）」，并拉起运行同目录下的客户端安装包（如 `DesignCloudDesktop-win-Setup.exe`）；找不到本地 Setup 时，会通过内嵌 Portal 地址从 `/api/desktop/releases/installer` 下载并校验 SHA256。将注入后的文件放入 `data/releases/desktop/` 后，门户下载页会通过 `/api/desktop/releases/bootstrap-installer` 提供该文件，实现桌面客户端与系统浏览器双重免告警安全互通。

## Cluster mTLS 证书

Engine 启动时若检测到 CA 不存在会**自动生成**自签名根证书（`certs/cluster/ca.crt` + `certs/cluster/ca/ca.key`），单 Server 部署无需手动操作。如需使用企业内部 CA 或自定义证书，可在首次启动前手动放置：

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

**证书目录权限矩阵**：`certs/cluster` 0755、`ca.crt` 0644、`ca/` 及节点目录 0700、私钥 0600。

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

Agent 节点证书的签发、分发与 OTP 自助注册流程见 [节点注册与引导](./node-registration.md)；多 Server 共享 CA 的同步步骤见 [高可用集群部署](./cluster-ha.md)。
