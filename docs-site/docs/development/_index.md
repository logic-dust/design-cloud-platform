---
title: 开发者指南
linkTitle: 开发者指南
weight: 40
description: 本地研发环境准备、编码规范、依赖装配、Action 扩展与测试门禁
---

欢迎参与 Design Cloud Engine 开发。本目录面向后端与嵌入式前端开发者，提供从本地环境搭建、工程开发规范、组件装配到新增特权动作及提交前门禁测试的完整指引。

> [!NOTE]
> 系统的整体运行架构、集群协议机制与各业务领域设计请参阅顶级专栏：[核心架构与领域设计](/docs/architecture)。

## 推荐开发旅程

```
┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  1. 环境与规范   │ ──> │  2. 装配与扩展   │ ──> │  3. 测试与验证   │
│  dev-env/convent │     │wire/action/web   │     │testing/verifiers │
└──────────────────┘     └──────────────────┘     └──────────────────┘
```

{{< cards >}}
{{< card link="dev-environment" title="1. 本地开发环境准备" >}}
{{< card link="code-conventions" title="2. 代码规范与工程约束" >}}
{{< card link="dependency-wiring" title="3. 依赖装配与服务生命周期" >}}
{{< card link="execution-development" title="4. 扩展特权 Action 与调度驱动" >}}
{{< card link="web-design" title="5. 嵌入式前端开发规范" >}}
{{< card link="testing" title="6. 测试策略与门禁验证" >}}
{{< /cards >}}
