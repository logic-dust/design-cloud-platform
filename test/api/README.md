### Design Cloud Engine API 集成测试入口

### 本文件作为索引，指导如何按模块执行集成测试。

## 1. 认证与基础 (必须最先执行)

- [01-public-api.http](file:./01-public-api.http) : 验证系统版本。
- [02-session.http](file:./02-session.http) : 执行登录获取 `{{accessToken}}`。

登录成功后，Token 会存入 REST Client 全局变量，有效期内无需重复登录。

## 2. 资源操作 (需登录)

- [06-apps.http](file:./06-apps.http) : 应用列表、详情、启动 (Launch)。
- [07-scheduler.http](file:./07-scheduler.http) : 作业状态查询、停止/挂起操作。
- [08-connections.http](file:./08-connections.http) : GUI 应用连接详情、代理隧道。
- [10-files.http](file:./10-files.http) : 文件浏览、上传、下载、回收站管理。

## 3. 系统管理 (需 Admin 权限)

- [03-bans-securityadmin.http](file:./03-bans-securityadmin.http) : 封禁记录查看与解除。
- [12-nodes-systemadmin.http](file:./12-nodes-systemadmin.http) : 节点状态监控、证书签发、Token 生成。
- [13-licenses-systemadmin.http](file:./13-licenses-systemadmin.http) : 许可证服务 (FlexLM) 的创建、更新与状态监控。
- [14-users.http](file:./14-users.http) : 用户管理、角色同步。
- [15-users-systemadmin.http](file:./15-users-systemadmin.http) : 系统用户管理（状态、访问权限、最大连接数）。
- [16-settings.http](file:./16-settings.http) : 平台基础配置查询。
- [16-settings-systemadmin.http](file:./16-settings-systemadmin.http) : 系统全量配置更新。
- [16-settings-alert-systemadmin.http](file:./16-settings-alert-systemadmin.http) : 告警阈值配置与 JSON Schema 校验。
- [17-audit.http](file:./17-audit.http) : 审计日志查询。
- [21-tickets.http](file:./21-tickets.http) : 工单系统管理与项目配置。

## 4. 调试与系统状态

- [18-dashboard.http](file:./18-dashboard.http) : 仪表盘概览、系统状态。
- [19-status-storage.http](file:./19-status-storage.http) : 共享存储快照。

## 5. 自动化测试脚本 (`run_http_tests.py`)

目录内提供纯 Python 标准库编写的自动化集成测试脚本，可直接联动 `http-client.env.json` 批量执行：

```bash
# 全量测试（默认使用 dev-centvm-ldap-hpcadmin 环境）
python3 test/api/run_http_tests.py

# 指定测试环境（如开发机本地）
python3 test/api/run_http_tests.py --env dev-localhost

# 指定领域过滤运行（自动处理鉴权登录）
python3 test/api/run_http_tests.py --domain nodes
python3 test/api/run_http_tests.py --domain settings

# 详细输出与 JSON 格式报表
python3 test/api/run_http_tests.py --verbose --json
```

AI 维护：可通过专门的 Skill `/test-portal-api` 进行测试调度与脚本自愈迭代。
