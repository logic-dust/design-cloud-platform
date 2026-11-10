---
title: 文档中心
linkTitle: 文档
weight: 1
description: Design Cloud Engine 文档主入口与知识全景
cascade:
  type: docs
---

欢迎查阅 Design Cloud Engine（DCE）官方技术文档。本文档库按主题与工程角色组织为五大知识模块：

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        Design Cloud Engine 文档中心                     │
├──────────────┬──────────────┬──────────────┬──────────────┬─────────────┤
│ 1. 系统总览  │ 2. 部署运维  │ 3. 核心架构  │ 4. 开发者指南│ 5. API 契约 │
│ (overview/)  │(deployment/) │(architecture)│(development/)│   (api/)    │
├──────────────┼──────────────┼──────────────┼──────────────┼─────────────┤
│ • 定位与边界 │ • 安装与配置 │ • 集群长连接 │ • 本地开发环境│ • Portal API│
│ • 能力矩阵   │ • 高可用集群 │ • 特权执行器 │ • 编码与规范 │ • 鉴权与会话│
│ • 架构总览   │ • 证书与管理 │ • 远程桌面代理│ • 依赖生命周期│ • 用户与管理│
│ • 术语导读   │ • 升级与排障 │ • 业务领域模型│ • 门禁与测试 │ • 错误码约定│
└──────────────┴──────────────┴──────────────┴──────────────┴─────────────┘
```

## 快速导航

### 1. [系统总览](./overview)
快速了解系统的产品边界、能力矩阵与分层设计：
- [产品定位与能力矩阵](./overview): DCE 核心边界与阅读路径
- [系统架构总览](./overview/architecture.md): Server、Agent、DcExec、Portal 与 Control 协作全景

### 2. [部署与运维](./deployment)
生产级单机与高可用集群部署、安全加固与生命周期运维：
- [安装与首次部署](./deployment/install.md): 前置条件、安装步骤与角色自动发现
- [配置文件详解](./deployment/config.md): `config.toml` 配置项与参数模型
- [高可用集群部署](./deployment/cluster-ha.md): 多 Server 拓扑、共享 CA 与故障转移
- [证书与传输安全](./deployment/tls.md): Portal HTTPS 与集群 mTLS 双证书体系
- [节点接入与注册](./deployment/node-registration.md): OTP 节点注册与证书分发
- [服务管理与日常运维](./deployment/operations.md): systemd/SCM 托管、dc-exec 特权配置与审计日志轮转
- [常见故障排查](./deployment/troubleshooting.md): 启动异常、握手失败与状态诊断
- [系统升级与迁移](./deployment/upgrade.md): 原地升级步骤、数据库演进与回滚方案
- [发版打包与制品交付](./deployment/release.md): 版本注入、RPM 打包与 Desktop 交付

### 3. [核心架构与领域设计](./architecture)
深入系统底层通信协议、执行隔离与核心领域设计：
- **[集群控制系统](./architecture/cluster)**: gRPC 双向长连接协议、Control 高可用选主、任务派发与 Agent 状态机
- **[特权执行器 (dc-exec)](./architecture/dcexec)**: 进程 IPC 架构、内置 Action 规范与提权隔离防护
- **[远程访问与协议代理](./architecture/remote-access)**: 桌面连接模型、VNC/RDP/SSH 反向代理透明管道
- **[业务能力与架构专题](./architecture/domains)**: 认证与三员分权、数据库与缓存、实时事件流与动态配置 Schema

### 4. [开发者指南](./development)
面向后端与前端开发者的工程指导：
- [本地开发环境准备](./development/dev-environment.md): 工具链、编译指令与调试
- [代码规范与工程约束](./development/code-conventions.md): 命名规范、防御性编程与架构约束
- [依赖装配与生命周期](./development/dependency-wiring.md): Server 组合根、跨域调用边界与生命周期
- [扩展特权 Action 与调度驱动](./development/execution-development.md): 编写与注册新 Action、对接新调度器
- [嵌入式前端开发规范](./development/web-design.md): 门户与大屏设计语言、色彩与组件体系
- [测试策略与门禁验证](./development/testing.md): 单元测试、HTTP 契约测试与提交前门禁

### 5. [API 契约参考](./api)
对外暴露的标准 RESTful API 契约与接口文档：
- [Portal API 契约总览](./api): 鉴权机制、通用错误结构与接口清单
- [用户与会话 API](./api/user.md): 认证登录、作业查看、文件管理与桌面连接
- [管理员与三员 API](./api/admin.md): 系统员、安全员与审计员专属管理端点
