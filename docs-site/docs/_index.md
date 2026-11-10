---
title: 文档中心
linkTitle: 文档
weight: 1
description: Design Cloud Engine 产品定位、核心边界、能力体系与快速导览
aliases:
  - /docs/overview
cascade:
  type: docs
---

**Design Cloud Engine（DCE）** 是专为企业内部 HPC / EDA 研发场景打造的统一接入与管理平台。通过统一 Portal API 与嵌入式 Web 控制台，为科研与工程设计人员提供应用启动、作业监控与控制、安全文件传输及远程图形/命令行会话连接（VNC / RDP / SSH）。

> [!NOTE]
> **DCE 的核心定位是不侵入、不替代外部调度器**。它作为接入层与管理编排中枢，负责多集群统一门户与接入治理，底层的排队计算与物理资源分配依然交由 LSF、Slurm 等业界标准调度器完成。

## 核心产品边界

| DCE 负责 | 外部专业系统负责 |
|---|---|
| **统一接入**：Web 门户、Portal API、统一鉴权与三员管理 | **排队调度**：外部作业队列与节点算力/内存/GPU 资源调度 |
| **应用中心**：应用模板、参数校验与跨调度器作业提交入口 | **生命周期**：调度器内核中的作业执行状态机与优先级重排 |
| **远程桌面**：协议代理隧道（VNC / RDP / SSH）与会话管控 | **协议服务**：计算节点上实际运行的 VNC Server / xrdp / sshd |
| **文件管理**：文件浏览器、安全传输、大文件流式上下行与删除回收 | **存储基建**：底层 NFS / Lustre / GPFS 并行文件系统配额与权限 |
| **集群编排**：节点自动注册、TLS 双向证书轮换与 Agent 守护 | **机房网络**：物理交换机、VLAN 隔离、防火墙规则与企业 PKI 体系 |

## 核心能力

- **应用与作业**：多调度器作业提交、LSF / Slurm 适配、状态实时流推送、作业日志在线查看
- **图形与远程**：网页端免密 VNC、RDP 远程桌面、Web SSH 终端、客户端挂起重连
- **数据与存储**：大文件流式传输、个人与共享目录浏览、回收站与文件生命周期、存储容量监控
- **集群与安全**：动态节点发现与注册、mTLS 证书自动轮换、三员分权治理、全局审计追踪

## 关键术语与概念

- **Engine**：系统的主执行程序。Linux 节点可按配置同时以 Server 或 Agent 模式运行；Windows 节点仅作为 Agent 计算节点运行。
- **Portal**：面向终端用户和 Web 前端的 HTTP / WebSocket / SSE 服务层，提供业务 API 与轻量嵌入式 Web 控制台。
- **Control**：集群编排管理中心（基于 gRPC），维护全节点拓扑连接，负责任务下发、状态收集与证书签发。
- **Agent**：常驻于各个工作节点的客户端代理，与 Control 保持长连接心跳，负责主机指标采集与任务执行。
- **dc-exec**：Linux 特权守护辅助子进程，负责降权用户文件操作、作业控制及受控提权动作。

## 快速导览

详细章节请通过左侧导航栏浏览，常见角色可从以下入口开始：

| 角色 / 诉求 | 推荐入口 |
|---|---|
| **部署与运维工程师** | [安装与首次部署](./deployment/install.md) · [配置文件详解](./deployment/config.md) · [高可用集群部署](./deployment/cluster-ha.md) · [常见故障排查](./deployment/troubleshooting.md) |
| **前端与三方集成开发** | [Portal API 契约总览](./api) · [用户与会话 API](./api/user.md) · [管理员与三员 API](./api/admin.md) |
| **系统与架构研发** | [核心架构与领域设计](./architecture) · [开发者指南](./development) · [测试策略与门禁验证](./development/testing.md) |
