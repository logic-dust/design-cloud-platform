---
title: 特权执行器 (dc-exec)
linkTitle: 特权执行器
weight: 20
description: dc-exec 的职责定位、进程边界与安全模型
aliases:
  - /docs/architecture/dcexec
---

`dc-exec` 是工作节点上供 Agent 调用的本地特权执行辅助程序，负责以目标用户身份执行需要权限提升的系统级操作（如特权文件管理、密码校验、会话管理等）。

> [!NOTE]
> `dc-exec` 仅在 **Linux** 系统上启用与运行。它依赖 Linux 进程凭证模型（`cap_setuid` / `cap_setgid` / `setgroups`）及 seccomp BPF 安全机制。Windows 节点使用专用的 Direct Executor。

{{< cards >}}
{{< card link="architecture" title="1. 启动器进程与 IPC 架构" >}}
{{< card link="actions" title="2. 内置 Action 规范与注册" >}}
{{< card link="security" title="3. 安全边界与提权防护" >}}
{{< /cards >}}

生产环境安装部署、`setcap` 赋权与 systemd 单元配置见 [服务管理与日常运维 · dc-exec 特权组件](/docs/deployment/operations#dcexec-特权组件配置)。
