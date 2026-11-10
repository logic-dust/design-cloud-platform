---
title: 日志体系
linkTitle: 日志
weight: 45
description: 应用/访问日志（journald）与审计/安全文件日志
aliases:
  - /docs/deployment/logging
---

日志分流、journald 与审计文件 tail、logrotate 样例已并入 **[服务管理与日常运维 · 日志体系与审计运维](./operations.md#日志体系与审计运维)**，避免与 unit / `config.toml` `[log]` 重复维护。

要点：

- 应用与 Portal 访问日志：**stdout → journald**（RPM unit 已配置 `StandardOutput=journal`）；不再生成 `app.*` / `access.*` 文件。
- 审计 / 安全日志：**`log.dir`** 下 `audit.<host>.log` / `security.<host>.log`，供 Portal `GET /api/admin/audit/logs/*` 与 SSE；RPM 附带 `/etc/logrotate.d/design-cloud-engine` 样例（仅审计/安全）。
- 字段细节见 [配置文件 · Log](./config.md)；运维命令与脱敏见 [运维](./operations.md#日志体系与审计运维)。
