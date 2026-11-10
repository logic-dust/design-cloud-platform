---
name: test-portal-api
description: 专门用于运行、诊断与迭代 test/portal 中的 Portal HTTP API 测试套件。执行对真实 Engine 的 REST 集成测试（勿与 make test-e2e / test/node 混淆）；当发现测试脚本存在断言不合理、依赖缺失或接口演进未同步时，自动审计并迭代更新测试脚本。
---

# Portal API 自动化测试与脚本迭代 (Test Portal API)

> 驱动：`bash test/portal/run.sh`（remote build + 测试）、`test/portal/run_http_tests.py`、`test/portal/http-client.env.json`。人类快捷：`make test-api`。

---

## 核心原则

1. **真实 HTTP**：面向 cent7 或本机已运行的 Engine。
2. **部署入口**：`test/portal/run.sh` 默认 **`bash .vscode/remote.sh build`**（远程编译，同 VS Code **remote: build**）；**`--push`** 走 **`remote.sh push`**（本地编译上传）。`run_http_tests.py` 仅测试，可选 `--remote-build` / `--remote-push`。`make test-api` 为 `--skip-deploy`。
3. **执行顺序**：`*.http`（非 `@manual`）→ Python 扩展（`cases_extended` / `cases_mutations`）→ RBAC → 最后 **auth_guard**。
4. **与 `.http` 同源**：端点变更的 `.http` 编写与文档同步由 `update-project-docs` Skill 负责（见其章节四）；本 Skill 只负责运行、诊断与迭代用例。
5. **零 PyPI 依赖**；失败时对照 `internal/server/portal/api/` 区分实现 vs 脚本。

---

## 推荐命令

```bash
make test-api                                    # 不部署
bash test/portal/run.sh            # 默认 remote.sh build + 测试
bash test/portal/run.sh --push     # 本地编译上传
bash test/portal/run.sh --no-build
python3 test/portal/run_http_tests.py --strict --allow-skip
```

默认环境、地址与测试账号均以 `test/portal/http-client.env.json` 为准；本文不复制具体值。

跑完必看终端 **RUN SUMMARY**（按模块 pass/fail/skip、失败请求列表）。

---

## 脚本维护

- `.http`：`# @manual` / `# @expect` 约定见 `http_suite.py`。
- Python 扩展：`cases_extended.py`、`cases_mutations.py`；`probe` / `strict_expected` / `--strict` 用于契约模式。
- 状态码：创建 `[200,201]`，异步 `[200,202]`；冒烟可含 `404`，**不要**把 `502` 当成功。

---

## 回归

1. `bash test/portal/run.sh` 或 `make test-api` → RUN SUMMARY 为 PASS。
2. 路由变更后的 `.http` 同步与 `verify_http_tests.py --strict`：按 `update-project-docs` Skill 章节四执行。
3. Go 改动：按 `.agents/README.md` 三层门禁执行（日常变更用 `verify-changed.sh`）。
