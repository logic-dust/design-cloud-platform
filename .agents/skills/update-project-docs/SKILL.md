---
name: update-project-docs
description: 在 Portal API、dc-exec Action、配置或架构变更后，按影响范围同步项目文档与 .http 测试，确保文档与源码实现一致
---

# 更新项目文档 (Update Project Docs)

> 本 SKILL 覆盖所有"源码 → 文档/测试"的同步任务。所有同步操作不能基于猜测，必须以源码和自动化脚本解析结果为唯一准则。

## 自动化判定

`verify-changed.sh` 会在 API 变更时严格检查 .http 路由覆盖，并在 dc-exec Action 变更时运行 Action 摘要提取器。两者都不比较或写入文档；文档必须按本 Skill 后续步骤人工更新并复核。

---

## 章节一：Portal API 文档 (docs-site/docs/api/)

**提取端点清单：**

```bash
python3 ./.agents/scripts/scan_portal_api.py
```

**执行步骤：**

1. **提取路由列表**：运行上方脚本，打印所有端点、路由分组与 Handler 映射。
2. **分析请求响应结构**：深入 `internal/server/portal/api/` 的 handler 源码，提取绑定的 JSON Tag 与 `validate` 校验规则。
3. **按访问面与业务领域分工覆写**（Portal API 分「公开 / 用户 / 管理」三种访问面）：
   - **`user.md`**：承载全部**非 `/api/admin/*`** 端点（登录用户 + 公开端点）的详细参数、请求体与响应 JSON；域内带"鉴权：否"标注的**公开端点**就地保留完整上下文；
   - **`admin.md`**：承载全部 System / Security / Audit Admin 管理端点的详细参数与响应 JSON；
   - **`_index.md`**：**严禁写入重复的端点请求/响应细节**！仅维护全局架构总览、基础约定（URL/返回格式/状态码规范）、权限模型（三员模式与 AdminScope 矩阵）以及领域端点速查矩阵与跳转链接；
   - **公开端点汇总纪律**：所有免认证端点（登录、节点注册、版本/发布、大屏访客等）除在 `user.md` 域内标注"鉴权：否"外，**必须同步登记到 `_index.md` 的「公开端点（无需认证）」清单表**——新增/移除公开端点时两处都要改。

**文档定位纪律**（读者是 API 调用者）：

- ❌ 不要出现 Handler 函数名、Go 源码文件路径、中间件链等后端实现细节
- ❌ 严禁在 `_index.md` 中重复贴出已在 `user.md` / `admin.md` 定义的参数表格与响应示例
- ✅ 需要清晰的业务领域划分、路径、方法、鉴权要求、请求参数、响应格式

## 章节二：dc-exec Action 文档

**提取 Action 矩阵：**

```bash
python3 ./.agents/scripts/extract_actions.py
```

**执行步骤：**

1. 运行提取脚本，输出 **已在 `dcexec.Handlers` 注册** 的 Action 列表及 `Privilege`（`root` / `TargetUser`）；Direct 动作不在此脚本输出中，以 `actions.md` 总表为准。
2. 解析 `internal/execution/payloads.go` 中的 `validate:"..."` 与 `json:"..."` Tag。
3. 阅读 `internal/dcexec/action/*.go` 补充代码级校验逻辑（绝对路径过滤、黑名单等）。
4. 覆写 dc-exec Action文档（`docs-site/docs/architecture/dcexec/actions.md`）。

## 章节三：配置文件与 Settings 同步

当配置项（`internal/platform/config/`）或运行时设置（`internal/contract/schema/`）发生变动时：

1. **启动配置 (`config.toml`) 变更**：
   - 更新 `docs-site/docs/deployment/config.md`（配置文件文档），确保字段名（如 `hosts`）、默认值、取值范围与严格解析规则 100% 对齐；
   - 检查并同步 `config.dev.toml`；RPM/`install.sh` 的配置模板由 `gen-config` 生成（见 `deployment/packaging/`，非仓库内静态 `config.toml` 文件）。
2. **运行时设置 (`settings`) 变更**：
   - 检查 `internal/contract/schema/defaults.go` 的默认值与范围；
   - 同步 `docs-site/docs/architecture/domains/settings.md` 与对应领域文档（如 `auth-and-users.md`、`storage-and-cache.md`）。

## 章节四：架构与开发文档

架构文档按读者问题分工，避免同一条分层规则在多个页面各自维护：

| 文档 | 负责回答 | 不重复维护 |
|---|---|---|
| `docs-site/docs/overview/architecture.md` | 进程与节点角色、运行时组件、Portal/Control/Agent/dc-exec 交互 | Server 内部包级依赖细则、Repo/事务/缓存清单 |
| `docs-site/docs/development/dependency-wiring.md` | Server composition root、依赖方向、跨域调用与 application 准入、服务生命周期 | Repo 清单、分页契约和缓存实例清单 |
| `docs-site/docs/architecture/domains/storage-and-cache.md` | 数据库事实源、Store/Repo、事务/分页边界与缓存决策 | Server 全局装配流程 |
| `docs-site/docs/development/code-conventions.md` | 简明开发约束和命名规则 | 与依赖装配、存储专题相同的详细架构说明 |
| `docs-site/docs/architecture/domains/_index.md` | 领域专题导航和范围说明 | 未经建模确认的 DDD 有界上下文承诺 |

当 Server 分层、目录归属或跨域依赖发生变化时：

1. 先检查 `internal/server/server.go`、`internal/server/assembly.go`、受影响包的构造函数和 import，确认真实依赖方向与启动顺序；架构规则或门禁变化时同时核对 `.agents/rules/arch-rules.yaml` 和 `verify-arch.py`。
2. 将详细依赖规则只维护在 `dependency-wiring.md`；按影响更新运行时总览、领域专题或开发约定，并由其它页面链接到唯一事实源。
3. 描述 `application` 时区分用例编排与聚合查询：它不是所有 Service 的必经代理；普通单域行为留在对应领域 Service。只有源码中真实存在的目录、职责和依赖才写入文档。
4. 改名、移动或删除文档时，全局检索相对链接、导航卡片、别名和源码路径；修复失效引用并保留需要的 URL alias。
5. 对领域归属的结论应依据数据所有权、业务职责和实际依赖；不因目录名或包数就宣称每个模块都是 DDD 有界上下文。

## 章节五：.http 测试同步 (test/portal/)

**.http 文件是测试也是 API 调用示例**，本 Skill 是 `.http` 编写与路由同步的唯一权威（执行与诊断由 `test-portal-api` Skill 负责；本地节点/实验室脚本在 `test/node`、`test/lab`，不要写进本章）。路由变更必须同步（`rules/cleanup-compliance.md` 硬阈值：未同步测试文件 = 0）。

**校验工具：**

```bash
python3 ./.agents/scripts/verify_http_tests.py --strict
```

自动检查：测试覆盖度（缺失端点）、Scope 纯洁性（public 文件禁带 Bearer 头）、领域归属（端点是否放在正确文件）。

**执行步骤：**

1. 运行校验脚本定位缺失/失配/误放位置的端点。
2. 比对源码绑定结构体（`json:"fooBar"`），确保 `.http` 中 JSON 键名无拼写错配。
3. 新增缺失示例请求，或将错放的接口迁移到对应领域文件。
4. 重新运行 `verify_http_tests.py --strict` 确认 100% 通过。

**文件分层约定：**

- `01-public-api.http`：仅无需鉴权的公共端点
- 普通用户端点：`02-session.http`、`06-apps.http` 等（Bearer 头）
- 管理员端点：包含 -system-admin.http / -security-admin.http 后缀的文件

---

## 验证闭环

1. **仅文档 / 格式（无 Go、无 `test/portal`、无 `.agents` Harness）**：
   - `git status` 确认范围；**不**等同于已跑 `make lint vuln` 或 `verify-changed.sh`（`verify-all.sh` 默认会跳过纯 `docs-site/**` 变更）。
   - 若改了 **`docs-site/docs/api/`** 中与路由、方法、鉴权相关的叙述：对照 `python3 .agents/scripts/scan_portal_api.py` 与实现，并运行  
     `python3 .agents/scripts/verify_http_tests.py --strict`（确保文档描述与 `portal_routes.txt` / `.http` 仍一致；未改 `.http` 时也要防文档写错端点）。
   - 若只改部署/开发叙述、不涉及 API 契约：无需上述 HTTP 脚本。架构文档有结构性变更时，构建文档站（`hugo --source docs-site --destination <临时输出目录>`）并检查相对 Markdown 链接、导航和 alias。
2. **伴随 Go / `.http` / Harness 改动**：按 `.agents/README.md` 三层门禁执行；日常变更用 `verify-changed.sh`，发版前用 `verify-all.sh --full`。
3. **纪律**：同步不能靠猜测；路由、参数、Scope 变更必须同时出现在 `.http` 测试文件中。
