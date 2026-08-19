---
title: 文档
linkTitle: 文档
weight: 1
description: Design Cloud Engine 文档主入口
cascade:
  type: docs
---

本文档区按主题拆分为多个分组。

建议优先阅读：

1. [概述](./overview) — 快速了解系统能力
2. [部署与配置](./deployment) — 构建、部署、配置
3. [Portal API](./api) — 开始 API 集成
4. [开发与设计](./development) — 深入系统设计与开发

## 分组导航

### 概述

系统能力总览与文档导航。

### 部署与配置

- [部署指南](./deployment/guide.md): 构建、安装、systemd 与证书部署
- [配置文件](./deployment/config.md): 配置项、默认值和角色发现规则
- [节点注册](./deployment/node-registration.md): 白名单、token 与证书绑定
- [日志与审计](./deployment/logging.md): 日志分类、审计边界和脱敏策略

### API 参考

- [Portal API](./api): 全部 API 端点一览、请求响应格式与鉴权

### 开发与设计

**设计文档**

- [系统架构](./development/architecture.md): Engine / Launcher / Server / Agent / Portal / Cluster 组件职责
- [系统设置](./development/settings.md): 13 领域分类、Schema 自省、节点覆盖、两级缓存与动态分发
- [集群](./development/cluster): 集群协议、执行幂等性、任务派发
- [启动器](./development/launcher): 特权执行边界、动作定义与安全机制
- [会话设计](./development/session.md): 登录会话、校验和续期机制
- [用户系统](./development/user-system.md): 用户模型、角色权限、认证与同步
- [远程访问](./development/remote-access): 连接模型、协议代理
- [Dashboard 规范](./development/dashboard.md): 前后端精度分工、多调度器状态映射与流式推送
- [全局事件与实时流](./development/events-and-streaming.md): SSE 单向事件流、轻量失效信号与审计增量日志
- [缓存架构](./development/cache.md): 四要素决策模型、全系统缓存对照与 DB 版本号轮询失效机制
- [后台任务](./development/background-tasks.md): Server/Agent 周期性后台循环、分布式租约与状态同步

**开发指南**

- [开发指南](./development/guide): 开发环境、规范与依赖装配
