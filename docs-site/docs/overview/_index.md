---
title: 概述
linkTitle: 概述
weight: 10
description: 系统概览
---

Design Cloud Engine 是面向 HPC/EDA 场景的云化调度平台，通过统一的 Portal API 对外提供应用启动、作业调度、文件管理、远程连接代理等功能。

**核心能力：**

- **Portal API** — RESTful 接口，供桌面客户端、Web 前端调用
- **集群调度** — 对接 LSF / Slurm，实现作业提交、控制、查询
- **连接代理** — 基于 WebSocket 的 VNC / RDP / SSH 协议代理
- **文件管理** — 远程文件浏览、上传下载、审批流程
- **多租户** — LDAP 集成、三员管理模式、用户配额控制

**文档导航：**

| 章节                         | 内容                        |
|----------------------------|---------------------------|
| [部署与配置](/docs/deployment)  | 构建部署、配置模型、节点注册、日志管理       |
| [API 参考](/docs/api)        | Portal API 端点、请求响应格式、用户系统 |
| [开发与设计](/docs/development) | 系统架构、设计文档、代码规范、开发指南       |
