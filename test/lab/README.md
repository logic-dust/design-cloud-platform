# 实验室手工场景

发版后在 cent7 / agent7 环境的核对步骤见 [post-deploy-checklist.md](./post-deploy-checklist.md)。

## VNC display 选号（保留旧占用）

`vnc-display-scenario.sh` 在 **cent7 + agent7-1** 上验证：**不清理节点上已有 Xvnc**，Launch 选号不与占用冲突。

| 步骤 | 内容 |
|------|------|
| 前置 | 记录 baseline `Xvnc` display；**不** `vncserver -kill` |
| 规格 | `scenario-fix-d1/d2` 固定 `:1`/`:2`；`scenario-reserve-d3` 预留 `:3`（不 Launch）；`scenario-dynamic-pool` 动态 |
| 固定 Launch | 201 成功占号；若旧 X 已占同号 → **409 视为通过** |
| 手工 | `:3` 若已有进程则保留，否则 `vncserver :3`（无 connection 行） |
| 动态 Launch | **201**；Agent 选号 = `min { n∈[1,99] | n ∉ live∪avoid }`（live=pgrep，`avoid` 含规格 1/2/3） |

```bash
bash test/lab/vnc-display-scenario.sh
# 仅清 connection 行、仍保留 Xvnc: CLEANUP_CONNECTIONS=1 bash test/lab/vnc-display-scenario.sh
```

规格已存在则复用。默认 **不** batch-delete connection（Delete 会远程杀受管 VNC）。
