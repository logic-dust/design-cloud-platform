---
name: update-project-docs
description: 在 Portal API、Launcher Action、配置或架构变更后，按影响范围同步项目文档与 .http 测试，确保文档与源码实现一致
---

# 更新项目文档 (Update Project Docs)

> 本 SKILL 覆盖所有"源码 → 文档/测试"的同步任务。所有同步操作不能基于猜测，必须以源码和自动化脚本解析结果为唯一准则。

## 自动化判定

`verify-changed.sh` 在相关代码变更时会自动运行提取/覆盖检查；手动运行仅用于编辑前即时查看缺项，最后以门禁结果为准。

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

## 章节二：Launcher Action 文档

**提取 Action 矩阵：**

```bash
python3 ./.agents/scripts/extract_actions.py
```

**执行步骤：**

1. 运行提取脚本，输出 Action 列表与 Privileged 属性矩阵。
2. 解析 `internal/execution/payloads.go` 中的 `validate:"..."` 与 `json:"..."` Tag。
3. 阅读 `internal/launcher/action/*.go` 补充代码级校验逻辑（绝对路径过滤、黑名单等）。
4. 覆写 Launcher 动作文档（`docs-site/docs/development/launcher/actions.md`）。

## 章节三：配置文件与 Settings 同步

当配置项（`internal/config/`）或运行时设置（`internal/server/service/settings/schema/`）发生变动时：

1. **启动配置 (`config.toml`) 变更**：
   - 更新 `docs-site/docs/deployment/config.md`（配置文件文档），确保字段名（如 `hosts`）、默认值、取值范围与严格解析规则 100% 对齐；
   - 检查并同步 `config.dev.toml` 与 `deployment/` 下的安装配置模板。
2. **运行时设置 (`settings`) 变更**：
   - 检查 `internal/server/service/settings/schema/defaults.go` 的默认值与范围；
   - 同步 `docs-site/docs/development/settings.md` 与对应领域文档（如 `session.md`、`cache.md`）。

## 章节四：.http 测试同步 (test/api/)

**.http 文件是测试也是 API 调用示例**，路由变更必须同步（rules/cleanup-compliance.md 硬阈值：未同步测试文件 = 0）。

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
- 管理员端点：包含 -systemadmin.http / -securityadmin.http 后缀的文件

---

## 验证闭环

1. **若仅涉及文档/格式更新**：`git status` 确认修改路径与格式无遗漏即可，无需执行全量单元测试。
2. **若伴随代码改动**：运行增量门禁：
   ```bash
   ./.agents/verifiers/verify-changed.sh
   ```
3. **纪律**：同步不能靠猜测；路由、参数、Scope 变更必须同时出现在 `.http` 测试文件中。
