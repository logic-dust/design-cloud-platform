### Design Cloud Engine API 集成测试入口

本文件索引 **REST Client 手跑** 与 **`run_http_tests.py` 自动化** 两种用法。

## 测试分层（建议门禁）

目标：**单测扛逻辑、静态对齐扛契约漂移、集成测试扛装配与真实环境**；三者不互相替代。

| 层级 | 何时跑 | 命令 | 依赖 | 通过标准 |
|:---:|---|---|---|---|
| **L0 契约** | 每次改 `portal/api` 或 `test/portal`；`verify-changed` / `verify-all` 已内含 | `python3 .agents/scripts/verify_http_tests.py --strict` | 无在线 Engine | 197 路由模板均有 `.http` 示例；Scope/领域文件归属正确 |
| **L1 单测** | 每次 Go 变更（按包） | `./.agents/verifiers/verify-changed.sh` 或 `go test` 目标包 | 无 | `internal/server/portal/api` 等包测试绿 |
| **L2 集成冒烟** | 合并前 / 每日（有实验室时） | 见下方「L2 最小子集」 | `http-client.env.json` 中 Engine 可达 | `run_http_tests.py` **非 strict** 可 `--allow-skip`；探针 404 不计失败 |
| **L3 集成全量** | 发版前 / 周更夜间 | `make test-api` 或 `bash test/portal/run.sh` | 同上 + 可选 `remote.sh build` | 全套非 `@manual` 块 + Python 扩展绿 |
| **L4 严格集成** | 大版本 / 改认证·RBAC·Settings 写路径 | `python3 test/portal/run_http_tests.py --strict` | 稳定实验室 | 无 skip（除显式 `--allow-skip` 审批） |
| **L5 手跑** | 破坏性 / 长连接 / 缺夹具 | IDE 执行带 `# @manual` 的块 | 操作者判断 | 人工签收；**不进入** L2–L4 失败统计 |

当前库存：**296** 个 `.http` 请求块 = **260** 自动 + **36** `@manual`（`verify_http` 报的 28 条「仅 manual 路由」= 尚无**任何** `.http` 块的模板，与 `@manual` 块不是同一概念）。

### L2 最小子集（约 15–25 分钟，够抓「路由没挂上 / 中间件错了」）

有 `cent7.dev.vm`（或等价）时，合并前可只跑：

```bash
for d in 01-public-api 02-session 16-settings 18-overview; do
  python3 test/portal/run_http_tests.py --no-color --allow-skip --domain "$d" || break
done
```

认证与读路径为主；**不写**平台设置、不跑 `auth_guard`（错误密码会封 IP，仍应留在全量末尾）。一次只接受一个 `--domain`，故用循环。

### `@manual` 归类纪律（新增时打标签）

| 原因 | 示例域 | 是否尝试自动化 |
|---|---|---|
| 破坏性 / 不可逆 | 全会话删除、删节点/证书、工单永久删除 | 否 → `@manual` |
| 强环境依赖 | 调度器真实 `jobId`、Proxy WebSocket 数据面 | 否或 `# @probe` |
| 已由 Python 扩展覆盖 | 多步 RBAC、`cases_mutations` | 勿在 `.http` 再抄一遍 |
| 仅文档 / 调试 | SSE 长连接 curl 说明 | `@manual` + 注释 |

能自动化的：**优先写进非 manual 块**，用 `# @probe` + `# @expect` 表达「资源可能不存在」。

### 与 `make test` 的关系

- `make test`（全仓 `go test ./...`）**不包含** HTTP 集成；发版或大重构用 `./.agents/verifiers/verify-all.sh --full`。
- **不要**用集成测试替代 L0：没有 `.http` 对齐时，集成全绿也可能漏掉新路由。
- **不要**指望单测替代 L2：Echo 注册、Scope、DB 事务与 JSON 外形仍要靠真机 HTTP 兜底。

## REST Client 手跑（按业务模块）

### 1. 认证与基础

- [01-public-api.http](./01-public-api.http) — 公开版本与 Desktop 发布接口
- [02-session.http](./02-session.http) — 登录，写入 `{{accessToken}}`

登录成功后 Token 存入全局变量，有效期内无需重复登录。

### 2. 资源操作（需登录）

- [06-apps.http](./06-apps.http) — 应用列表、详情、启动（先跑 [06-apps-system-admin.http](./06-apps-system-admin.http) 创建 `testAppId`）
- [07-scheduler.http](./07-scheduler.http) — 作业查询与操作
- [08-connections.http](./08-connections.http) — 连接与代理
- [10-files.http](./10-files.http) — 文件浏览、直链下载、创建审批单
- [10-files-security-admin.http](./10-files-security-admin.http) — **审批同意后的** Token 兑换与下载（须在 10-files 创建申请之后）

### 3. 系统 / 安全管理

- [03-bans-security-admin.http](./03-bans-security-admin.http) — 封禁查看与解除
- [12-nodes-system-admin.http](./12-nodes-system-admin.http) — 节点与证书
- [13-licenses-system-admin.http](./13-licenses-system-admin.http) / [13-licenses.http](./13-licenses.http)
- [14-users.http](./14-users.http) / [14-users-system-admin.http](./14-users-system-admin.http)
- [16-settings.http](./16-settings.http) / [16-settings-system-admin.http](./16-settings-system-admin.http) / [16-settings-alert-system-admin.http](./16-settings-alert-system-admin.http)
- [17-audit.http](./17-audit.http) — 审计日志与执行记录（**流式**见 [22-sse-push.http](./22-sse-push.http)）
- [21-tickets-system-admin.http](./21-tickets-system-admin.http) / [21-tickets.http](./21-tickets.http)
- [04-sessions-security-admin.http](./04-sessions-security-admin.http) / [05-access-security-admin.http](./05-access-security-admin.http)

### 4. 调试与系统状态

- [18-overview.http](./18-overview.http) — 非流式 Overview JSON
- [19-status-storage.http](./19-status-storage.http) — 系统整体状态与共享存储快照
- [22-sse-push.http](./22-sse-push.http) — SSE 握手（长连接完整验证见文件内 curl 说明）

---

## 自动化测试

### 日常入口

```bash
make test-api                    # test/portal/run.sh --skip-deploy（不测部署）
bash test/portal/run.sh          # 默认 remote.sh build + 全量测试
bash test/portal/run.sh --push   # remote.sh push
bash test/portal/run.sh --no-build
```

**谁会部署？** 只有带 `remote.sh` 的包装脚本会动远端；`run_http_tests.py` 本身默认不部署，需 `--remote-build` / `--remote-push`，或交给上面两个 shell 入口。

`run_http_tests.py` 顺序由 [`http_suite.HTTP_FILE_ORDER`](./http_suite.py) 固定（与上表手跑章节**不完全相同**）：

1. **引导**：公开 API → 登录 → **`03-bans`（清历史封禁，提高跨次运行稳定性）** → 用户/系统用户
2. **配置**：`16-settings` 只读 → `16-settings-system-admin` / alert 写入
3. **业务域**：同一领域 **admin 建资源 → 用户 API**（apps / connections / licenses / tickets）；文件域为 **`10-files` → `10-files-security-admin`（先同意审批再换 Token/下载）**
4. **只读/态势**：audit、overview、storage
5. **安全收尾**：`04-sessions-security-admin`、`05-access-security-admin`（会话/访问控制仍靠后，避免提前触发封禁）
6. **`22-sse-push`**（长连接握手放 `.http` 套件末尾）
7. Python：**`cases_extended` / `cases_mutations`** → RBAC → 最后 **`auth_guard`（错误密码等，必须最后）**

`10-files.http` 在开头用 `batch-delete` 清 `dc_http_*` 遗留；目录/回收站闭环仍在该文件内。

带 `# @manual` 的块仅 IDE 手跑，自动化跳过。

带 `# @probe` 的块表示**目标资源可能不存在**（占位 id / 依赖上游创建 / 已被清理）：

- 非 strict 模式：状态码须命中 `# @expect`（通常把 `404` 一并列入）。
- strict 模式：返回 `404` 时按“探针”语义 **skip**，不计为失败；`502` 始终失败。

存在性依赖的 mutation（如 `PUT/DELETE .../{{id}}`、审批链 `review`/`tokens`）应加 `# @probe` 并在 `# @expect` 内包含 `404`，避免硬编码占位 id（默认 `1`）在资源缺失时造成伪失败。

环境见 `http-client.env.json`（默认 `dev-ldap-hpcadmin` → `cent7.dev.vm:1234`）。

- `testNode`：实验室 Linux 计算节点 **Node ID**（与 `os.Hostname()` 一致，例如 `agent7-1.dev.vm`），用于 `08-connections-system-admin` / `12-nodes` 等；控制面在 `cent7`，Windows 工作站 Node ID 为 `win1`（非 FQDN）。
- 用户侧连接用例只拉起夹具规格 **`fixed-test-vnc-resource`**（由 admin 段创建）；runner 通过 `http_suite` 捕获 `userResourceID`，**不会**再误用资源列表第一项。

跑完看终端末尾 **RUN SUMMARY**。

```bash
python3 test/portal/run_http_tests.py --domain nodes
python3 test/portal/run_http_tests.py --strict --allow-skip
python3 test/portal/run_http_tests.py --remote-build
```

改 API 路由时，提交门禁会跑：

```bash
python3 .agents/scripts/verify_http_tests.py --strict
```

（静态对齐 `*.http`，不发 HTTP。）

AI 维护：Skill `/test-portal-api`。

实验室发版核对清单见 [../lab/post-deploy-checklist.md](../lab/post-deploy-checklist.md)。

**Python 扩展**：`cases_extended.py` / `cases_mutations.py` 补充校验与多步状态流；新端点优先写入 `*.http`，避免在 extended 中重复路径。
