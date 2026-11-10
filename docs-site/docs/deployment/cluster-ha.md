---
title: 高可用集群部署
linkTitle: 高可用部署
weight: 30
description: 多 Server 高可用拓扑、CA 共享、Agent 故障转移、DB 租约与多节点状态语义
aliases:
  - /docs/deployment/ha-deploy
---

本文档描述生产环境中多 Server（Leader/Standby）高可用拓扑部署方案与注意事项。Control 面架构设计原理见 [Control 高可用](/docs/architecture/cluster/control-ha)；单机与基础拓扑见 [安装与首次部署](/docs/deployment/install)。

---

## 拓扑规划与 CA 共享要求

所有 Server 节点**必须共享同一套集群 CA**，以确保 Agent 节点发起的 mTLS 握手能被任意一台 Server 节点信任。

各 Server 节点的配置文件大体相同，核心差异在于：
1. **CA 私钥分发**：CA 私钥 `ca.key` 须通过安全受控渠道（如运维私网或保密管道）手动同步到每台 Server 节点的本地磁盘，系统不通过网络自动广播 CA 私钥。
2. **集群拓扑声明**：`cluster.servers` 必须完整列出所有主备 Server 节点的主机名。

```bash
# 1. 主节点 (server-01) 初始化 CA 并签发自己的证书
# 详见 "证书与传输安全" 文档的 Cluster mTLS 章节

# 2. 将 CA（含私钥）安全复制到其他 Server 节点的数据目录
scp certs/cluster/ca.crt server-02:/var/lib/design-cloud/certs/cluster/
ssh server-02 'mkdir -p /var/lib/design-cloud/certs/cluster/ca && chmod 0700 /var/lib/design-cloud/certs/cluster/ca'
scp certs/cluster/ca/ca.key server-02:/var/lib/design-cloud/certs/cluster/ca/

# 3. 在 server-02 上基于共享 CA 签发本机 Server 证书
mkdir -p certs/cluster/server-02 && chmod 0700 certs/cluster/server-02
openssl genrsa -out certs/cluster/server-02/server-02.key 2048
openssl req -new -key certs/cluster/server-02/server-02.key \
  -out /tmp/server-02.csr -subj "/CN=server-02"
openssl x509 -req -in /tmp/server-02.csr \
  -CA certs/cluster/ca.crt -CAkey certs/cluster/ca/ca.key \
  -CAcreateserial -days 1095 -out certs/cluster/server-02/server-02.crt
chmod 0644 certs/cluster/server-02/server-02.crt && chmod 0600 certs/cluster/server-02/server-02.key
```

多台 Server 节点共享同一份 `config.toml` 配置示例：

```toml
[cluster]
# 显式声明主备 Server 顺序列表
servers = ["server-01", "server-02"]
```

---

## Agent 连接故障转移策略 (Strict Ordered Failover)

Agent 节点启动时，严格按照 `cluster.servers` 列表从前往后的顺序尝试连接：
1. **主选优策略**：始终优先连接列表第一位的 Primary Server（如 `server-01`）。
2. **降级连接**：当 Primary Server 不可达或失联超过超时阈值时，自动退避切换至备用 Server（`server-02`）。
3. **主节点回切 (Failback)**：Agent 在后台定期探测更高优先级的 Server 是否复活。一旦 Primary 节点恢复健康，Agent 将在平滑关闭当前备机连接后，自动切回主 Server。

---

## 后台任务与分布式数据库租约 (DB Lease)

在多 Server 并行部署下，为了防止多个 Server 实例对同一批后台任务（如超时连接清理、过期 Session 清理等）产生并发冲突，系统采用**数据库行级分布式租约**：

- 各 Server 在执行周期性后台轮询前，必须先在数据库中竞态抢占租约锁。
- 租约附带有限 TTL（例如 30s），持有租约的 Server 负责续约。若当前 Leader 节点异常宕机，租约到期后由备用 Server 自动接管任务。
- 所有后台任务循环清单与调度策略详见 [分布式后台任务与租约](/docs/architecture/domains/background-tasks)。

---

## 多 Server 模式下的状态一致性

- **连接状态感知**：当不同 Agent 连接在不同的 Server 时，各 Server 内存中的直接连接映射虽然独立，但节点的心跳和指标均定时持久化至统一的共享数据库。
- **状态查询口径**：管理端调用 `GET /api/admin/system/nodes/:id/status` 获取的是数据库中持久化的最新状态指标快照，保证无论运维请求被反向代理分发到哪台 Server，获取到的节点视图都是统一的。
