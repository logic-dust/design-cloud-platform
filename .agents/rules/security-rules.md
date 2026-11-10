<!-- context-optimizer: cohesive-specialist -->
# Agent Security Constraints & Execution Guardrails

在 Harness 开发模式下，任何代理级的工作流执行必须遵循以下硬性的安全红线和防御性编程约定：

## 1. 宿主环境隔离限制 (Sandbox Boundaries)

- **绝对禁止环境泛滥**: 如果工作流涉及证书生成、编译临时产物或 mock 数据库，所有的临时文件**必须**存放在 `build/`、
  `test-results/` 或系统的 `/tmp/` 中。禁止污染 `/etc`、用户根路径 `~` 或项目根目录。
- **环境假定**: 工作流应假定 `Go`、`protoc` 等基础依赖已安装。不试图擅自执行包管理器（如 `apt-get`、`brew`
  ）进行静默安装，若执行环境缺失则应直接抛错向人类反馈。
- **项目根目录洁净度**: 严禁在根目录直接输出 `error.txt`、`temp.go`、`debug.log` 等临时文件。

## 2. DcExec/Engine 架构安全隔离

- **dc-exec 沙箱**: `dc-exec`（`cmd/exec/`）必须保持为轻量化的特权或安全边界进程。代码修改中，严禁让 dc-exec 绕过
  `internal/agent` 直接进行数据库访问或直接连接外部无关组件。核心调度必须汇聚于上层 Engine 接口。
- **特权操作限制**: 工具脚本或 AI 执行流程中，**禁止在不请示的情况下执行带有 `sudo` 起手的特权操作**。相关测试应采用特定的低特权假定或让
  `test/node/run_all.sh` 在预期降权环境下处理。

- 节点登记（`POST /api/nodes/:id/registration`）是公开路由，但必须提供一次性、有时效的服务端预签发 bootstrap token；常数时间比较，验证成功后通过证书绑定消费 token。新增同类接口时必须保留该认证与消费边界；具体有效期、熵和限流由服务/配置代码定义，勿在规则中复制数值。

修改任何代码时，均须先检查凭证泄露、命令注入、路径逃逸与日志敏感数据；按需执行静态扫描与相关测试。高风险边界包括认证会话、控制通信、DcExec、Agent、DB schema 与 Proto。Go 变更验证见根 `AGENTS.md`；安全扫描入口为 `./.agents/verifiers/verify-security.py`。

## 3. 安全审查时不要报为漏洞的有意设计

- `test/portal/http-client.env.json` 中的 `password` 是实验室夹具（`cent7.dev.vm` / localhost），故意入库。真实本机覆盖在已忽略的 `http-client.env.local.json`。
- `deployment/systemd/design-cloud-engine.service` 的 `CAP_SETUID` / `CAP_SETGID`、不设 `NoNewPrivileges`、`ProtectHome=false` 是 `dc-exec` 切换用户并访问 `/home` 所必需。
- `.vscode/common.sh` 把 `/etc/design-cloud` 的 `dbpwd` / `ldapbindpwd` 复制到 `/opt/dce`，只发生在实验室开发机：engine 以 `hpcadmin` 运行，读不到属主为 `dce` 的 0600 密钥文件。
