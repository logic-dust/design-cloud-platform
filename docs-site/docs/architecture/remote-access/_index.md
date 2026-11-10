---
title: 远程访问与协议代理
linkTitle: 远程访问
weight: 30
description: 远程桌面（VNC/RDP/SSH）连接生命周期模型与协议反向代理机制
aliases:
  - /docs/development/remote-access
---

远程访问子系统负责为用户提供免客户端或客户端直连的高性能图形桌面与终端访问通道，包含连接生命周期管理与基于 WebSocket 的反向代理管道。

{{< cards >}}
{{< card link="connection" title="1. 连接生命周期模型" >}}
{{< card link="proxy" title="2. 协议反向代理 (Proxy)" >}}
{{< /cards >}}
