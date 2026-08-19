---
name: test-portal-api
description: 专门用于运行、诊断与迭代 test/api 中的 Portal HTTP API 测试套件。执行多环境端到端集成测试，当发现测试脚本存在断言不合理、依赖缺失或接口演进未同步时，自动审计并迭代更新测试脚本。
---

# Portal API 自动化测试与脚本迭代 (Test Portal API)

> 本 Skill 负责 Design Cloud Engine Portal HTTP API 的端到端集成测试与测试资产持续维护。
> 驱动核心位于 `test/api/run_http_tests.py` 与 `test/api/http-client.env.json`。

---

## 核心原则与守则

1. **真实请求驱动**：测试必须面向真实运行的 Engine 实例（开发机本地或远程测试集群），验证从 HTTP 路由、身份认证、中间件权限校验到业务逻辑的完整链路。
2. **测试脚本持续自愈 (Self-Healing)**：当测试失败时，必须深入后端源码（`internal/server/portal/api/`）判别是**服务实现缺陷**还是**测试脚本不合理**；属于脚本过时或断言不合理者，主动修正测试脚本。
3. **保持零外部依赖**：测试脚本 `test/api/run_http_tests.py` 严格保持纯 Python 3 标准库实现，不引入额外第三方 PyPI 依赖。
4. **与 `.http` 规范 100% 对齐**：任何端点与参数的增删改，必须与 `test/api/*.http` 文件保持同源一致。

---

优先本机运行 `python3 test/api/run_http_tests.py --addr http://localhost:1234`（使用默认测试账号前，确认本机测试环境账号适配）。`--addr` 可直接指定地址，不依赖环境文件；若需要使用具名环境，再通过 `--env` 选择，先确认其配置与目标可达。不得在命令行明文传递生产凭证。

### Phase 2: 执行集成测试

全量测试：
```bash
python3 test/api/run_http_tests.py --env dev-centvm-ldap-hpcadmin
```

按领域单域测试（自动处理登录与 Token 注入）：
```bash
# 测试节点管理与三节点拓扑
python3 test/api/run_http_tests.py --domain nodes

# 测试运行时配置与 JSON Schema
python3 test/api/run_http_tests.py --domain settings

# 测试应用与调度
python3 test/api/run_http_tests.py --domain apps
```

带完整请求/响应 Body 打印与 JSON 报表：
```bash
python3 test/api/run_http_tests.py --verbose --json
```

---

### Phase 3: 诊断判定与脚本迭代更新 (Script Update Protocol)

当执行测试出现 FAIL 时，按以下决策树进行分析与迭代：

```
                    ┌─────────────────────────┐
                    │  API 测试返回非预期状态码  │
                    └───────────┬─────────────┘
                                │
               查阅后端 handler 源码与 REST 规范
                                │
       ┌────────────────────────┴────────────────────────┐
       ▼                                                 ▼
【业务实现缺陷 / Panic】                         【测试脚本不够合理】
• 出现未捕获 500 Internal Error               • REST 规范状态码演进（如创建应 201 而脚本断言 200）
• 数据库锁死 / 节点离线崩溃                   • 异步操作返回 202 Accepted 被脚本误判
• 字段拼写错误导致序列化遗漏                   • 域过滤运行缺乏自动登录依赖
       │                                                 │
 提报后端缺陷并修复源码                           更新 test/api/run_http_tests.py
```

#### 脚本更新规范要求：
1. **统一状态码宽容度**：资源创建使用 `expected_status=[200, 201]`，异步任务使用 `expected_status=[200, 202]`，可选包资源使用 `expected_status=[200, 404]`；
2. **动态 Token 管理**：对于需要不同角色（如 `admin` vs `test1`）进行权限隔离验证的用例，使用闭包或专用 Holder 动态解析 Token；
3. **节点角色模型**：断言必须基于单一 `isServer`，严禁回退历史 `isControl` / `isAgent`；
4. **连接防护验证**：保持对 `allowConnections` (bool) 与 `maxConnections` (int) 字段的严格断言。

---

### Phase 4: 回归与双向同步验证

完成测试与脚本更新后，必须执行以下双重验证：

1. **回归全量 HTTP 测试**：
   ```bash
   python3 test/api/run_http_tests.py --env dev-centvm-ldap-hpcadmin
   ```
   必须达到 **100% PASS**。

2. **验证 `.http` 文件测试覆盖度与规范性**：
   ```bash
   python3 ./.agents/scripts/verify_http_tests.py --strict
   ```

3. **运行全量增量门禁**：
   ```bash
   ./.agents/verifiers/verify-changed.sh
   ```
