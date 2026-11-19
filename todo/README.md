# 待办总览

> 2026-11-06：原 `cache.md`（节点缓存）、`xrdp.md`（XRDP 运维）、`go.md`（Go 工程化）三份清单已合并至本文件并删除，历史版本见 git。
> 约定：目录只保留这一份清单；每条待办带「优先级 / 状态 / 事实依据」；**完成即从清单删除**，追溯靠 git 历史，不保留已完成项清单。

## 状态约定

| 状态 | 含义 |
| :--- | :--- |
| 待开发 | 方案可用，尚未落地代码 |

## 总览

| 优先级 | 事项 | 状态 | 领域 |
| :--- | :--- | :--- | :--- |
| **P0** | Desktop 迁移到 MyGo 目标架构与分阶段开发方案（详见 `todo/mygo.md`） | 待开发 | 桌面客户端 / Monorepo |
| **P2** | 节点服务端口探针 `service-probe` | 待开发 | 运行态缓存 |

---

## 一、节点运行态缓存与指标

### P2 节点服务端口探针 `service-probe`（待开发）

**目标**：为 Portal 管理界面及远程桌面连接提供宿主机服务（SSH、RDP、XRDP）的实时监听/就绪提示，并在 Proxy 拨号前作为 soft hint（明确 `down` 且数据新鲜时提供前置预警或快速失败）。

未来 Node DTO 规划形态（纯内存拼装，非持久化 Schema）：

```json
"services": {
  "ssh":  { "port": 22,   "status": "up",   "checkedAt": "2026-11-06T12:00:00Z" },
  "rdp":  { "port": 3389, "status": "up",   "checkedAt": "2026-11-06T12:00:00Z" },
  "xrdp": { "port": 3389, "status": "down", "checkedAt": "2026-11-06T12:00:00Z" }
}
```

**架构边界（严禁违背）**
1. **职责分离**：本探针是节点级服务探针，仅关注宿主机端口与守护进程可用性；严禁与关注虚拟显示、用户进程生命周期的 `connection-probe` 混在一起，禁止混入连接席位语义。
2. **纯内存驻留，不落 `nodes` 主表**：状态由控制面内存 Map 或 `Registry` 承载；默认不持久化，避免再次引发写放大。冷启动或切主后允许短暂展示 `unknown`，等待 Agent 下一帧遥测秒级自愈。
3. **机制选型待定**：Agent 本机 socket/netlink 监听检查 vs 控制面直接拨测 `dial_host`；立项时评估内网防火墙与跨平台适配，并确定 DTO / proto 契约。

**现状核实**：全仓仅文档命中 `service-probe`；周期探针只有 `metrics` / `storage-probe` / `connection-probe`（`internal/agent/telemetry.go:96-123`）；Node DTO（`internal/server/service/node/types.go:103-189`）无 `services` 字段，`cluster.proto` 无对应消息，22/3389 监听态无采集。

---

## 二、相关事实源

- EDA 节点侧 XRDP 配置（`KillDisconnected=false`、`startwm` 正常 Logout 收尾、`logind.conf`）事实源：`docs-site/docs/deployment/config.md`「Linux XRDP 设计节点」；目前仅存在于文档，`deployment/` 无 `sesman.ini` / `startwm.sh` 模板落地。
- 连接生命周期与席位探针：`docs-site/docs/architecture/remote-access/connection.md`。
- 门禁与任务路由：`AGENTS.md`、`.agents/README.md`。
