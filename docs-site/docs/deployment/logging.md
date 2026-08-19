---
title: 日志与审计
linkTitle: 日志与审计
weight: 50
description: 应用日志、访问日志、审计日志与脱敏规则
---

## 概述

日志体系分成两大类：

- 应用日志体系：用于普通运行、调试、Portal 访问与组件告警
- 审计日志体系：用于认证、授权、节点管理、执行与安全事件留痕

两者都基于 Go 标准库 `log/slog`，但职责、输出位置和保留策略不同。

## 日志分类

运行时主要有 4 类日志：

| 类型         | 作用            | 典型来源                                             |
|------------|---------------|--------------------------------------------------|
| `app`      | 应用与组件运行日志     | `internal/logger`、Server / Agent / Launcher 管理逻辑 |
| `access`   | Portal 请求访问日志 | `internal/server/portal`                         |
| `audit`    | 一般审计事件        | `internal/audit`                                 |
| `security` | 安全相关审计事件      | `internal/audit`                                 |

其中：

- `app` 与 `access` 由普通日志系统控制
- `audit` 与 `security` 由审计管理器单独写入

## Dev 与 Prod 差异

### Dev 构建

Dev 构建使用自定义文本 handler，定义在：

- `internal/logger/dev.go`

特点：

- 文本格式，便于人读
- 自动附带 `host` 和 `version`
- `Warn` 及以上级别附带源码位置
- `component` 会被提取并前置展示
- 所有级别都可输出

示例：

```text
2026-03-25 10:00:00.123 INFO [portal_server] request completed uri=/api/version status=200 method=GET ip=10.0.0.10
2026-03-25 10:00:01.456 WARN [cluster_execution] execution request timed out nodeID=cn01 execution_id=req-1234
    at /home/levin/.../internal/server/control/server.go:300
```

### Prod 构建

Prod 构建使用 JSON handler，定义在：

- `internal/logger/prod.go`

特点：

- JSON 结构化输出
- 适合日志采集、索引与检索
- 由配置控制日志级别
- 自动附带 `host` 和 `version`

示例：

```json
{
  "time": "2026-03-25T10:00:00Z",
  "level": "INFO",
  "msg": "execution request timed out",
  "component": "cluster_execution",
  "nodeID": "cn01",
  "execution_id": "req-1234",
  "host": "mgt01",
  "version": "1.0.0"
}
```

## 配置

应用与访问日志由 `[log]` 配置块控制：

```toml
[log]
dir = "/var/log/design-cloud"
app_level = "INFO"
access_level = "INFO"
max_size = 100
max_age = 30
max_backups = 10
```

源码树开发可用相对路径 `dir = "logs"`（相对 **BaseDir** = 配置文件所在目录）。生产示例为 `/var/log/design-cloud`；unit 也可再传 `--log-dir` 覆盖。

字段说明：

| 字段                 | 说明               |
|--------------------|------------------|
| `log.dir`          | 日志目录             |
| `log.app_level`    | 应用日志级别           |
| `log.access_level` | 访问日志级别           |
| `log.max_size`     | 单个日志文件大小上限，单位 MB |
| `log.max_age`      | 日志保留天数           |
| `log.max_backups`  | 保留的日志备份文件数量      |

## 轮转与压缩

日志写入统一通过：

- `internal/logger/logger.go`

`CreateLogWriter()` 创建。

日志轮转基于 `timberjack`，支持：

- 文件轮转
- 最大大小限制
- 最大保留天数
- 最大备份数量
- 压缩

默认压缩算法是：

- `zstd`

这同样适用于审计日志。

## 脱敏规则

日志属性会经过统一替换逻辑：

- `internal/logger/logger.go`

默认会掩码以下敏感 key：

- `password`
- `token`
- `secret`
- `key`
- 以及名称中包含 `password` 的字段

此外，`error` 会被转换为字符串输出。

这意味着：

- 普通应用日志
- 访问日志
- 审计日志

都会复用同一套基础脱敏逻辑。

## 应用日志使用方式

全局应用 logger 初始化入口在：

- `cmd/engine/main.go`
- `internal/logger/logger.go`

常见用法：

```go
import "design-cloud-engine/internal/logger"

log := logger.WithContext("component", "cluster_execution")
log.Info("Execute request received", "nodeID", "cn01", "execution_id", "req-1234")
log.Warn("Execution timed out", "nodeID", "cn01", "execution_id", "req-1234")
```

推荐约定：

- 组件初始化时创建带 `component` 的子 logger
- 不要在每次调用时重复拼组件名到消息里
- 优先使用结构化字段，不要把关键信息塞进字符串

## 访问日志

访问日志由：

- `internal/server/portal/portal.go`

中的 request logger 负责。

会记录：

- URI
- 状态码
- 方法
- IP

并在出错时额外记录：

- `msg`：`"request failed"`
- `error`：具体错误信息

并对慢请求（> 500ms）做额外标记。

访问日志与普通应用日志分离，便于：

- 接口访问分析
- 排障
- 后续接入集中式日志检索

## 审计日志

审计日志实现位于：

- `internal/audit/logger.go`
- `internal/audit/service.go`
- `internal/audit/types.go`

审计日志有两条独立输出：

- 主审计日志：普通审计事件
- 安全审计日志：安全与风控相关事件

特点：

- 始终是 JSON
- 与应用日志分离
- 保留期更长
- 适合合规留痕和外部收集

默认策略：

- 审计日志保留 180 天
- 主审计日志默认 100MB / 10 文件
- 安全审计日志默认 50MB / 20 文件

常见审计事件包括：

- 登录/认证
- Session 操作
- API 操作
- 节点注册与证书绑定
- 执行请求
- 安全拦截与拒绝

## 代码结构

```text
internal/logger/
├── dev.go
├── prod.go
└── logger.go

internal/audit/
├── logger.go
├── service.go
└── types.go

internal/server/portal/
└── portal.go
```

## 文档边界

日志系统分成：

- 运行日志
- 访问日志
- 审计日志

因此 `logging.md` 同时覆盖：

- 输出分类
- dev/prod 差异
- 轮转策略
- 脱敏规则
- 审计边界

作为主线。
