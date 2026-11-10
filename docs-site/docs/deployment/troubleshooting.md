---
title: 故障排查
linkTitle: 故障排查
weight: 70
description: 启动、证书握手、数据库、节点离线与租约异常的定位与处置
---

本文按故障域列出常见现象、判据与处置。日志管理与落盘位置见 [服务管理与日常运维](./operations.md)；Control 面转发机制见 [Control 高可用](/docs/architecture/cluster/control-ha)。

## 启动失败

| 现象 | 原因 | 处置 |
|---|---|---|
| 启动日志出现 `!!! INSECURE` 并退出 | `config.toml` 或密钥文件权限过宽 | `chmod 600 config.toml`；密钥文件须 owner-only |
| 解析配置时报未知字段 | 解析器开启 `DisallowUnknownFields`，键名必须严格小写 | 删除拼写错误/多余键，或 `check-config --config ...` 先行校验 |
| 绑定 `80/443` 失败 | 进程无 `CAP_NET_BIND_SERVICE` | 使用 systemd unit 内置的 `AmbientCapabilities=CAP_NET_BIND_SERVICE`，或改用高位端口 |
| 端口被占用 | 同机重复启动或旧进程残留 | `./dc-engine service stop`，确认 PID 释放后再启动 |

启动前统一用静态校验，不依赖是否已绑定端口：

```bash
/opt/dce/dc-engine check-config --config /etc/design-cloud/config.toml
```

## 证书握手失败

### `certificate signed by unknown authority`

Agent 本地 cluster CA 与 Server 使用的 CA 不一致：

```bash
# 确认两边 CA 是同一个文件
diff /on/agent/certs/cluster/ca/ca.crt /on/server/certs/cluster/ca/ca.crt
```

多 Server 场景还需确认所有 Server 的 CA 指纹一致：

```bash
for s in server-01 server-02; do
  echo -n "$s: "; ssh $s "openssl x509 -in /var/lib/design-cloud/certs/cluster/ca.crt -noout -fingerprint"
done
```

### TLS 正常但连接被应用层拒绝

证书指纹未在 Server 数据库注册。通过节点注册流程录入后重启 Agent，见 [节点注册与引导](./node-registration.md)。

### GUI / API 提示证书不受信任

将生成的 `portal.ca.crt` 安装到操作系统的受信任 CA 列表；本地开发改用 `mkcert`。Windows 客户端可用安装器 CA 注入，见 [TLS 证书配置 · Windows 客户端在线安装器注入](./tls.md#windows-客户端在线安装器注入-pe-overlay)。

### 证书内容与链路校验

```bash
# 查看证书详情（包括 SAN）
openssl x509 -in certs/portal/portal.crt -text -noout | grep -A5 "Subject Alternative Name"

# 验证证书链
openssl verify -CAfile certs/portal/portal.ca.crt certs/portal/portal.crt

# 测试 HTTPS 连接
curl -v --cacert certs/portal/portal.ca.crt https://dc.cloud.hpc
```

## 数据库连接异常

- 密码来源优先级为 `database.password` > `database.password_file`（默认 `/etc/design-cloud/dbpwd`）。使用密码文件时，文件必须 owner-only，否则启动会被拒绝。
- `ssl_mode = disable` 会输出明文连接告警；生产环境应按企业规范改为 `require` / `verify-ca` / `verify-full`。
- 连接池参数需满足逻辑约束：`max_idle_conns <= max_open_conns`，`max_open_conns` 在 `1..500`，`conn_max_lifetime` 为 `0` 或 `600..86400` 秒；非法组合会在启动期报错。
- PostgreSQL 首次部署可用 `deployment/postgres-create-db.sh` 创建库与用户（以 root/postgres 执行，需传入 `user_password=...`）：

```bash
sudo user_password='<password>' bash deployment/postgres-create-db.sh
```

字段含义与默认值见 [配置文件 · 数据库](./config.md)。

## 节点离线 / 注册失败

- 节点在线状态以数据库的 `online` / `offline` / `maintenance`、`last_heartbeat_at` 与状态调和结果为准；多 Server 下各节点内存连接状态可能不同，以 DB 快照为准。
- health checker 会先移除 stale agent 内存连接，再尽快同步 DB offline；周期调和是兜底，不必手工改库来"修在线状态"。
- 判断 Agent 是否成功启动与注册：

```bash
# 节点侧
journalctl -u design-cloud-engine | grep -E 'Starting AGENT|Successfully registered'
# Server 侧：出现证书指纹校验与上线标记
journalctl -u design-cloud-engine | grep -E 'Certificate fingerprint verified|Node marked online in DB'
```

- 注册命令在发送 OTP 前会预检 cert/key/CA 三个目录可写；失败通常是目标机目录权限或服务账号不匹配。完整参数见 [节点注册与引导](./node-registration.md)。

## 租约卡死

多 Server 的后台任务通过数据库租约互斥。查看与手工释放：

```bash
# 查看 DB lease 状态
psql -h db.example.com -U dc -d dce -c "SELECT task_name, owner_id, expires_at FROM task_leases;"

# 手动释放卡死的 lease（最后战术；先确认持租节点确实已下线）
psql -h db.example.com -U dc -d dce -c "DELETE FROM task_leases WHERE task_name IN ('session_cleanup', 'connection_cleanup');"
```

租约机制与多 Server 状态语义见 [高可用集群部署](./cluster-ha.md)。
