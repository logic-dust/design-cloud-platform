---
title: 发布与制品
linkTitle: 发布与制品
weight: 90
description: 版本注入、安装包构建、Desktop 客户端制品与发布命令
---

本文描述从版本号到可发布制品的完整链路。开发环境与工具链见 [开发环境](/docs/development/dev-environment)。生产换包：`systemctl stop design-cloud-engine` → `rpm -Uvh` / 等价安装 → `check-config` → `systemctl start`；全新空库：引擎启动期 `Store.Migrate()`（GORM `AutoMigrate`）按 `internal/model` 建表。PostgreSQL 存量库须先按 `deployment/db/migrations/` 执行版本链 SQL（当前仅 `001`：0.1.x→0.2.x）。**不**提供版本回滚、`.rpmnew` 合并指引或自动数据搬迁。

## 版本注入

版本号由 `Makefile` 统一计算并通过 `ldflags` 注入：

- `VERSION`：版本号，支持 `X.Y.Z` 或 `X.Y.Z-R` 格式（`BASE_VERSION` 可被外部 `VERSION=` 覆盖）；带 `-R` 时自动智能拆分软件版本与包 Release
- `PKG_RELEASE`：包修订/发布号（默认 1，如 1、2、1.el7）；仅更新打包配置而不变动源码时可单独递增
- `COMMIT`：git 短 hash；`DATE`：构建日期 UTC；作为独立元数据注入，不拼进软件版本串
- `make print-version` 输出当前纯净软件版本号（如 `0.2.5`）
- `make print-pkg-version` 输出当前包版本号（含 Release，如 `0.2.5-1`）
- `GET /api/version` 通过 `version` + `commit` + `releaseDate` 三个字段组合唯一定位一次构建，并返回 `minDesktopVersion`（settings `desktop.minSupportedVersion` 的公开投影）

## 安装包

```bash
make package                # 正式包：默认 0.2.5-1，GOARCH=amd64 PKG_TYPE=rpm
make package PKG_RELEASE=2  # 打包修订包：代码不变，Release 递增为 2（0.2.5-2）
make package VERSION=0.2.5-1.el7 # 支持直接传带 Release 的版本
make package-dev            # 开发快照：Release 自动附加 git 短 hash
GOARCH=arm64 make package   # 交叉打 arm64 包（gen-config 仍在本机构建）
make package SERVERS=mgt01,mgt02 LOG_LEVEL=DEBUG  # 定制包内 config.toml 的管控列表与日志级别
make package PKG_CONFIG=./my.config.toml          # 直接导入自定义 config.toml（与 SERVERS/LOG_LEVEL 互斥）
```

- `package` 先构建 `engine` 与 `dcexec`，再由 `deployment/packaging/build-packages.sh` 调用 nfpm 打包；engine 与 dc-exec 版本锁步。
- 构建机需安装 `nfpm`（`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`）。**CentOS 7 自带 bash 4.2**：打包脚本已避开空数组的 `"${arr[@]}"` 展开（`set -u` 下 4.2 会判 unbound），在 4.2 与 5.x 上均可直接 `make package`。
- 默认产物在 `build/packages/`（打完包后 `build/` 下不再保留 dc-engine/dc-exec 中间产物）；`PKG_TYPE` 支持 `rpm` / `deb` / `all`。
- RPM 脚本职责（`%pre` 建用户、`%post` 设 `setcap`/属主/tmpfiles、`%posttrans` `daemon-reload` + `try-restart`）见 [运维与服务管理](./operations.md#一键打包与-rpm-部署生产主路径)。
- 包内附带：`/usr/share/design-cloud-engine/scripts/vnc/job-gui-wrapper.sh`（作业 GUI 包装）、`/etc/logrotate.d/design-cloud-engine`（审计/安全日志样例，app/access 走 journald）。
- Windows Agent 单独构建与打包：`make win-agent` 产出 `build/dc-engine.exe`；`make win-package` 将 `dc-engine.exe` 与 `install-win-agent-configless.ps1`、`install-win-agent.cmd` 打包为 `build/packages/design-cloud-agent-<version>-windows-amd64.zip`（目标机解压后管理员运行 `install-win-agent.cmd -PortalUrl ... -Token ...` 即可一键领证并安装服务）。

## Desktop 客户端制品

Desktop 客户端由本仓库之外的客户端项目构建，Engine 负责**托管、在线上传与分发**。对外是否可下载由发布目录中的清单与制品决定（例如 `releases.win.json` / Setup；无文件即 404）。管理台 `desktop` 设置域只保留 `maxAssets`、`installerFileName`、`minSupportedVersion` 等策略字段，不再提供分发软开关。

**在线管理**：管理台具备 `ScopeDesktop` 权限的管理员可通过 `/api/admin/system/desktop/*` 接口在线清点、流式上传与删除主程序制品（`Setup.exe`、`Installer.exe`、`Portable.zip`、`*.nupkg`、`releases.{win|linux|osx}.json`）及 Sidecar 插件包（详见 [管理 API · 桌面客户端发布](../api/admin.md#桌面客户端发布desktop)）。Engine 自动校验清单/ZIP 完整性、计算 SHA256、原子落盘并广播缓存失效。

### 主程序（Velopack）

通过管理台上传或由运维将 `releases.{win|linux|osx}.json` 与对应更新包置于 **`<data_dir>/desktop/`**（打包默认 **`/var/lib/design-cloud/data/desktop`**；`data_dir` 省略时等于 `<state_dir>/data`）。客户端通过 `GET /api/desktop/releases/latest` 与 `GET /api/desktop/releases/download/*` 更新。

- `releases.{channel}.json` 由 Velopack 生成，`Assets[].FileName` 是权威文件名；**不要改成固定 `latest` 名**，否则会破坏清单匹配、Full/Delta 语义与客户端更新缓存。
- 「最新版」通过接口表达（`/releases/latest`、`/releases/download/latest`），而不是通过重命名磁盘文件表达。

### 首次安装 Setup 包

首次安装器（`dc-desktop-installer`）使用**单一固定文件名**的 Setup 制品，与更新包分离；不做版本化命名，新版本发布时原地覆盖同一文件。

- 文件名由管理台 `desktop` 设置域的 `installerFileName` 指定（默认 `DesignCloudDesktop-win-Setup.exe`）。
- Velopack 的默认输出就是 `<PackId>-<channel>-Setup.exe`：当前 `build.ps1` / `Makefile` 以 `-u DesignCloudDesktop` 打包、不传 `--channel`，Windows 默认 channel 为 `win`，所以构建产物就是 `DesignCloudDesktop-win-Setup.exe`，**不需要改名**。Velopack 的 Setup 名里**不包含架构**；只有在你自行重命名/复制产物时才需要同步改 `installerFileName`。
- 文件放在 `<data_dir>/desktop/`（与 `releases.*.json` 同级），只放一份；不要发布带版本号或多份 `*Setup*.exe` 副本。
- 安装器**不解析** `releases.*.json`：该清单只服务 Velopack 的 OTA 更新（nupkg），首次安装固定走 `/api/desktop/releases/installer` 返回的文件名 + SHA256。
- 元数据接口：`GET /api/desktop/releases/installer` 返回配置文件的 `fileName` / `sha256` / `size`；安装器据此下载并校验哈希后再执行。
- 配置的 Setup 文件不存在时接口返回 `404`。

### Windows 在线安装器（可选）

`cmd/dc-desktop-installer` 是**在线安装器（Online Installer）**，负责把 Portal CA 证书写入 Windows 受信任根存储、写入 `%APPDATA%\DesignCloud\config.json`，再从 `/api/desktop/releases/installer` 下载 Setup.exe 并校验 SHA256 后拉起安装。它适合新机器首次安装，避免用户手动处理证书。

门户下载页的命名对应关系：

- `DesignCloudDesktop-Installer.exe` → **在线安装器 / Online Installer**（需联网）
- `DesignCloudDesktop-win-Setup.exe` → **标准安装包 / Standard Installer**（含完整文件）
- `DesignCloudDesktop-win-Portable.zip` → **便携版 / Portable**（免安装）

- 构建并注入本集群的 CA 与访问地址：

```bash
make desktop-installer
./build/dc-engine cert inject-installer \
  --ca conf/certs/portal/portal.ca.crt \
  --input build/dc-desktop-installer.exe \
  --output build/DesignCloudDesktop-Installer.exe \
  --server-url https://dc.cloud.hpc:1234
```

  也可以直接执行 `make inject-installer SERVER_URL=https://dc.cloud.hpc:1234`，输出 `build/DesignCloudDesktop-Installer.exe`。

- 将 `DesignCloudDesktop-Installer.exe` 放到 `<data_dir>/desktop/`（与 Setup.exe 同级）。它是**本集群专属产物**：CA 或 Portal 地址变化后必须重新注入并覆盖。
- 元数据接口：`GET /api/desktop/releases/bootstrap-installer`，返回固定文件名的 `fileName` / `sha256` / `size`；文件未发布时返回 `404`。下载仍走 `GET /api/desktop/releases/download/DesignCloudDesktop-Installer.exe`。
- 未注入 CA/地址的模板 `dc-desktop-installer.exe` 不适合直接分发给最终用户；门户展示的是注入后的 `DesignCloudDesktop-Installer.exe`。

### Sidecar 插件（如 TurboVNC）

仅供 Design Cloud Desktop 消费，与主程序版本独立。

**目录布局**（默认 `<data_dir>/desktop/plugins`）：

```text
<data_dir>/desktop/plugins/
  turbovnc/
    win/
      latest.zip
      latest.version
    linux/
      latest.zip
      latest.version
```

**`latest.version`**：单行文本，格式为 `version|sha256|size`（管道分隔）。`#` 开头为注释。示例：

```text
3.1.4|a1b2c3...64位十六进制|12345678
```

**CLI 发布**（推荐，避免手算校验和）：

```bash
# 从目录打包并写入 latest.zip + latest.version
engine desktop plugin publish \
  --name turbovnc --platform win \
  --from-dir /path/to/turbovnc-staging \
  --version 3.1.4

# 已有 zip
engine desktop plugin publish \
  --name turbovnc --platform linux \
  --zip ./turbovnc-linux.zip --version 3.1.4

# 校验已投放目录
engine desktop plugin verify --name turbovnc --platform win
```

`--from-dir` 未指定 `--version` 时，读取目录内 `VERSION.txt` 首行。

**HTTP API**：公开下载 `GET /api/desktop/plugins/:name/latest?os=`、`.../download/latest`、`.../download/latest.zip`（详见 [用户 API · Version](../api/user.md#version)）；管理台在线上传与清理走 `GET/PUT/DELETE /api/admin/system/desktop/plugins*`（详见 [管理 API · 桌面客户端发布](../api/admin.md#桌面客户端发布desktop)）。

除管理台在线上传外，也可由运维拷贝或通过上述 CLI 写入磁盘（手工替换磁盘文件时，进程内清单/插件缓存最多按 **24h** `CacheTTL` 延迟生效；通过管理台 API 上传/删除会立即 `Invalidate` 本机缓存并通过 `cache_versions` 广播其它 Server 节点）。
