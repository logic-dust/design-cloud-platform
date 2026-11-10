# 发版后检查（cent7 + testNode）

Engine / dc-exec 变更推送到实验室后，在跑 Portal 集成测试前建议按顺序核对（默认 `testNode` = `agent7-1.dev.vm`，控制面 `cent7.dev.vm:1234`）。

1. **部署控制面** — `bash test/portal/run.sh`（或 `bash .vscode/remote.sh build`），确认 `GET /api/version` 的 `commit` 与本次发布一致。
2. **计算节点 Agent 在线** — `GET /api/admin/system/nodes/{{testNode}}`：`status` 为 `online`，`enrollmentStage` 为 `enrolled`；Agent 监听端口与 control **`port` 一致**（本环境一般为 **52255**）。
3. **证书指纹一致** — 节点行 `certFingerprint` 与节点上 `agent7-1.dev.vm.crt` 的 SHA-256 一致；异常时用 `POST /api/admin/system/nodes/certificates` 或重新 `node register`。
4. **dc-exec 与 Engine 同版** — 计算节点 `/opt/dce/dc-exec` 与 cent7 同次构建；`setcap cap_setuid,cap_setgid+ep`；Agent 与 Engine 版本一致。
5. **LSF 环境** — `GET /api/admin/system/settings/scheduler` 的 `envScript` 为绝对路径（如 `/share/apps/lsf/conf/profile.lsf`），否则 launch 可能 500。
6. **回归 Portal API** — `bash test/portal/run.sh --no-build` 或 `make test-api`，**RUN SUMMARY** 为 PASS。

Windows 节点（如 `win1`）仅 RDP 登记时不参与 VNC launch。
