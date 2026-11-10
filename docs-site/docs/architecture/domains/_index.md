---
title: 核心业务领域模型
linkTitle: 业务领域
weight: 40
description: 用户与鉴权、存储与缓存、事件流与概览看板、系统配置及后台任务等核心领域设计
---

本节按主要业务能力组织设计文档。这里的“领域”表示代码与文档中的功能归属，不意味着每个包都已定义为独立的 DDD 有界上下文；具体边界以各能力的职责、数据所有权和依赖规则为准。Server 内部的 Service、Application 与组合根关系见[依赖装配](../../development/dependency-wiring.md)。

{{< cards >}}
{{< card link="auth-and-users" title="1. 用户认证、三员分权与会话体系" >}}
{{< card link="storage-and-cache" title="2. 数据库、事务与缓存" >}}
{{< card link="events-and-overview" title="3. 状态概览看板与实时事件流" >}}
{{< card link="settings" title="4. 统一系统配置与动态生效" >}}
{{< card link="background-tasks" title="5. 分布式后台任务与租约机制" >}}
{{< /cards >}}
