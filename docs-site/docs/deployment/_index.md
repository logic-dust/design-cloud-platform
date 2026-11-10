---
title: 部署与运维
linkTitle: 部署运维
weight: 20
description: 环境要求、单机与高可用集群部署、传输安全、服务托管与生命周期运维
---

本文档目录覆盖从基础设施规划、首次安装、参数配置、传输安全加固，到服务管理、集群高可用扩展及日常运维排障的完整生命周期。

## 推荐运维路径

```
┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  1. 安装与配置   │ ──> │  2. 安全与集群   │ ──> │  3. 托管与运维   │
│  install/config  │     │ha/tls/node-regist│     │oper/trouble/upgr │
└──────────────────┘     └──────────────────┘     └──────────────────┘
```

{{< cards >}}
{{< card link="install" title="1. 安装与首次部署" >}}
{{< card link="config" title="2. 配置文件详解" >}}
{{< card link="cluster-ha" title="3. 高可用集群部署" >}}
{{< card link="tls" title="4. 证书与传输安全" >}}
{{< card link="node-registration" title="5. 节点接入与注册" >}}
{{< card link="operations" title="6. 服务管理与日常运维" >}}
{{< card link="troubleshooting" title="7. 常见故障排查" >}}
{{< card link="upgrade" title="8. 系统升级与迁移" >}}
{{< card link="release" title="9. 发版打包与制品交付" >}}
{{< /cards >}}
