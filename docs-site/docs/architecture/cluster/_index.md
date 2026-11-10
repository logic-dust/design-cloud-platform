---
title: 集群控制系统
linkTitle: 集群系统
weight: 10
description: gRPC 长连接通信、Control 选主高可用、Agent 运行时架构与幂等任务分发
aliases:
  - /docs/development/cluster
---

集群控制系统承担 Design Cloud Engine 中心管理节点（Server）与所有分布式计算工作节点（Agent）之间的高性能长连接通信、状态收集、心跳保活与任务调度执行。

{{< cards >}}
{{< card link="protocol" title="1. 节点长连接通信协议" >}}
{{< card link="control-ha" title="2. Control 高可用与故障转移" >}}
{{< card link="task-dispatch" title="3. 任务调度与分发" >}}
{{< card link="agent" title="4. Agent 节点内部架构" >}}
{{< card link="idempotency" title="5. 执行幂等与重试保证" >}}
{{< /cards >}}
