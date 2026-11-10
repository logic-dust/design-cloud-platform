# Lab：systemd 生产形态烟测（无 dlv）

与 [DEBUG_GUIDE.md](../DEBUG_GUIDE.md) 中的 **push/build（dlv）** 并存、互不替代：

| 流程 | 入口 | 运行身份 / 配置 | 用途 |
|------|------|-----------------|------|
| **dlv 开发** | `./.vscode/remote.sh push\|build` | `hpcadmin`，`/opt/design-cloud/config.toml`，数据多在安装树 | 断点调试、快速迭代 |
| **systemd 烟测** | `./.vscode/remote.sh smoke` 或 `./.vscode/lab/systemd-desktop-smoke.sh` | `design-cloud`，`/etc/design-cloud/config.toml`，FHS 数据根 | 对齐 RPM/生产：Desktop API、路径、unit |

默认实验室主机与 SSH 变量见 [env.sh](../env.sh)（`REMOTE_HOST=cent7.dev.vm` 等）。

## 一键烟测

在仓库根目录：

```bash
./.vscode/remote.sh smoke
# 或
./.vscode/lab/systemd-desktop-smoke.sh
```

会：停掉占用端口的 **dlv** / 手工 `dc-engine` → 用本地 `releases.win.json` + **touch 占位** 更新 Desktop 目录 → `systemctl restart design-cloud-engine` → `curl` 校验 `latest` / `installer`。

可选更新二进制（二选一）：

```bash
# 在 cent7 上 rsync + make（推荐；含 Linux engine/dc-exec 与 Windows dc-engine.exe）
./.vscode/lab/systemd-desktop-smoke.sh --remote-build

# 或上传本机产物（仅 engine；不含 dc-exec）
make -s ENGINE_OUT=build/dc-engine-dev engine-dev
./.vscode/lab/systemd-desktop-smoke.sh --install-engine build/dc-engine-dev
```

## 数据布局实验（软链 / 配置）

```bash
./.vscode/lab/systemd-desktop-smoke.sh --layout fhs      # /var/lib/design-cloud/data/desktop/
./.vscode/lab/systemd-desktop-smoke.sh --layout symlink   # data → /share/apps/design-cloud/data
./.vscode/lab/systemd-desktop-smoke.sh --layout config  # 顶层 data_dir 指 /share/...
./.vscode/lab/systemd-desktop-smoke.sh --restore-fhs
```

### 实测结论（cent7）

- **软链**：`/var/lib/design-cloud/data` → `/share/apps/design-cloud/data`，Desktop API **通过**。
- **显式 `data_dir`**：指向 `/share/apps/design-cloud/data`，**通过**；`data_dir` 须写在首个 `[section]` 之前，否则 TOML 报 `unknown field`。
- **生产**：RPM unit 默认 `ReadWritePaths=/var/lib/design-cloud`；`data_dir` 在 `/share/...` 时可能要 drop-in 扩路径（cent7 的 `lab-files.conf` 已放松沙箱）。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `DESKTOP_RELEASES_DIR` | 兄弟仓 `design-cloud-desktop/Releases` | 本地 manifest 源 |
| `LAB_SHARE_DATA` | `/share/apps/design-cloud/data` | symlink / config 布局 |
