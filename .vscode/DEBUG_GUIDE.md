# VSCode 远程开发

两条流程都把产物安装到 `/opt/dce`，并以 **hpcadmin** 身份用 **dlv** 启动 engine。

| 流程 | 命令 | 产物来源 |
|------|------|---------|
| **push** | `remote.sh push` / 任务 `remote: push` | 本地 `make` 编译 dc-exec+engine → 上传 |
| **build** | `remote.sh build` / 任务 `remote: build` | rsync 源码 → 远程 `make` 编译 |

两者后续完全一致，只差“产物怎么来”：

```
[停 systemd 单元] → [取产物] → [安装 + 配置 + 密钥 + dcexec 权限] → [check-config 预检] → [dlv 启动 + 实测 /api/version]
```

- **push** 适合：改了大量代码，想在本地看编译错误、迭代更快。
- **build** 适合：本机不装构建环境，或要与远程工具链完全一致。

`launch.json` 中 `Remote Push (Debug & Attach)` / `Remote Build (Debug & Attach)` 已把对应流程设为
`preLaunchTask`，按 F5 即可一键「编译 → 安装 → 启动 → 附加」。

## 不开端口转发（直连）

两条流程本就需要 SSH/rsync 到同一台机，而 Portal 与 dlv 都监听 `0.0.0.0`，所以不需要隧道：

```bash
curl http://cent7.dev.vm:1234/api/version     # 本机直连 Portal API
```

VSCode 同样直连 `cent7.dev.vm:2345`（`remoteDebug.host` 配置项即该地址）。

> **dlv 监听地址**：`0.0.0.0`（`env.sh` 的 `DLV_BIND`）是从最初版本沿用至今的既有做法，内网调机一般没问题——
> 换来的是本机直连附加、无需隧道。dlv 无认证、不加密（日志原文 `Listening for remote connections
> (connections are not authenticated nor encrypted)`），所以它本质是一个“谁连上谁能执行”的入口。
>
> 日常在受信内网可以忽略；出现下面任一情况时值得收紧：
>
> - 机器换到不可信/共享网段（例如多人共用的集群网段，任何人都能访问 `10.1.1.0/24`）；
> - 以特权用户（如 `hpcadmin`，本机还有 root SSH）跑调试会话。
>
> 收紧只需一处开关：
>
> ```bash
> export DLV_BIND="127.0.0.1"     # dlv 只听回环
> ssh -N -L 2345:127.0.0.1:2345 hpcadmin@cent7.dev.vm   # VSCode 再经隧道附加
> ```

## 目录与身份（env.sh）

| 变量 | 值 | 说明 |
|------|-----|------|
| `REMOTE_USER` | `hpcadmin` | SSH / rsync / 文件属主 / **engine 运行用户**（同一身份） |
| `ROOT_USER` | `root` | 仅用于写 `/opt`、setcap、复制密钥、停 systemd 单元 |
| `REMOTE_PATH` | `/opt/dce` | 安装前缀，属主 hpcadmin |
| `DLV_BIND` | `0.0.0.0` | dlv 绑定地址（改 `127.0.0.1` 可收紧，见上） |

安装后的布局（数据全部落在同一棵树，避免 `/var/lib` 的属主问题）：

```
/opt/dce/
├── dc-engine                # engine（0755 hpcadmin）
├── dc-exec              # dc-exec（0700 hpcadmin + setcap）
├── dlv                      # 调试器
├── config.toml              # 运行配置（0600 hpcadmin）
├── dbpwd / ldapbindpwd      # 密钥（0600 hpcadmin，从 /etc/design-cloud 复制）
├── data/  logs/ certs/      # 运行期数据（hpcadmin 可写）
```

> **注意**：`/opt/dce` 也是 systemd 单元 `design-cloud-engine.service` 的安装路径。
> 两条流程会**覆盖该路径下的二进制**并**停止该单元**，即开发流程接管了这套部署。
> 该单元仍为开机自启，重启主机后需重跑流程（或手动 `systemctl stop`）才能释放 `1234/52255`。

## 为何这些细节是必须的（lab 实测坑）

1. **systemd 单元会抢端口**：`design-cloud-engine.service` 常驻占用 `1234/52255`，且被 kill 后会被自动拉起（表现为“刚起来就退出 / 端口仍被占用”）。流程启动前会 `systemctl stop` 它。
2. **dc-exec 需要 setcap，且属主/权限是硬约束**：缺 `cap_setuid,cap_setgid+ep` 时，引擎拉起 app 会因 L0 capability 校验失败。setcap 须在**本地盘**上，NFS 无效。
   dc-exec 的 L1 自检（[verifier/environment.go](../internal/dcexec/verifier/environment.go)）还要求：
   - **权限恰好 `0700`**（`mode & 077 != 0` 直接拒绝并报 `expected 700`）
   - **属主 UID == 调用者（父进程）realUID**，即**必须归 engine 的运行用户所有**
   - 父进程必须是 `design-cloud-engine` / `dc-engine` / `runner`，且禁止从 shell 启动

   > ⚠️ **不要“加固”成 root 所有或 0755**：那会让 dc-exec 直接拒绝运行（exit 126）。
   > 它的特权模型是「engine 的运行账号特权等价」——dc-exec 的职责就是以特权切换成任意用户。
   > 因此 dlv 一旦暴露，等同把该账号（进而这台机器的 root）交出去，加固只能从 dlv 侧做（见上一节）。
3. **dc-exec 与 engine 属主必须一致**（都是 hpcadmin）——这是 dc-exec L1 的 `checkCaller` 硬校验（详见第 2 条），不是可选的整理项。
4. **安装前必须先停进程**：覆盖正在运行的二进制会 `ETXTBSY`。
5. **密钥可读性**：`/etc/design-cloud/*` 属 `dce` 且 0600，hpcadmin 读不到；流程会把 `dbpwd`、`ldapbindpwd` 复制到 `/opt/dce`（已存在则不覆盖），对应配置里的相对路径 `password_file='dbpwd'`。
6. **data_dir 必须可写**：engine 启动时要在 `<data_dir>/certs/cluster` 建目录。配置用 `data_dir = ''`（回退 BaseDir = `/opt/dce`，已属 hpcadmin）。预检会拦截不可写的情况——否则会出现“dlv 正常监听、engine 秒退”的迷惑现象。
7. **就绪以 API 为准**：dlv 是独立进程，engine 崩溃后它仍监听 2345。流程实测 `/api/version`，失败即打印进程/端口/dlv 日志。

## 常用任务

| 任务 | 实际命令 | 作用 |
|------|---------|------|
| `remote: push` | `remote.sh push` | 本地编译 → 上传安装 → dlv 启动 |
| `remote: build` | `remote.sh build` | rsync + 远程编译 → 安装 → dlv 启动 |
| `cleanup: remote` | `remote.sh cleanup` | 停远程 dlv/engine |
| `check: remote status` | `remote.sh status` | 直连可达性 / 进程 / 端口 / API / dc-exec 权限 |
| `run: engine` | `make run` | 本地运行 |

命令也可直接在终端执行，例如 `bash .vscode/remote.sh build`。

## 文件职责

| 文件 | 职责 |
|------|------|
| `env.sh` | 唯一可改配置（主机 / 路径 / 端口 / 用户 / `DLV_BIND`）+ 打印函数 |
| `common.sh` | 全部实现：`run_flow` / `cleanup_all` / `show_status` |
| `remote.sh` | 唯一入口：把子命令分派到 `common.sh` |
| `remote-run-windows.sh` | Windows agent 旁路（独立平台，单独保留） |
| `tasks.json` / `launch.json` | VSCode 任务与调试配置 |

改配置只动 `env.sh`，改行为只动 `common.sh`，新增子命令只动 `remote.sh`。

## 排障

| 现象 | 原因与处理 |
|------|-----------|
| `engine 进程已退出（dlv 仍在监听）` | 脚本会直接打印 dlv 日志末尾。常见：`data_dir` 不可写、DB 连接/迁移失败、密钥路径错误 |
| `data_dir 不可写` | 以 hpcadmin 运行，`data_dir` 需该用户可写；用 `data_dir = ''`（回退 `/opt/dce`） |
| `config check failed` | 配置字段与当前二进制 schema 不符（配置是运行期文件，不随 rsync 同步） |
| `db migration failed`（如 `column ... contains null values`、`invalid input syntax for type bigint`） | 库里有旧格式数据，而 AutoMigrate 无法重命名列 / 给非空表加 NOT NULL 列。按新语义就地修数据（重命名列、规整取值），或在确认数据可弃后清空该表 |
| dc-exec 拉起 app 失败 | 看 `remote.sh status` 的属主与 `getcap`；属主须与 engine 一致，且需 `cap_setuid,cap_setgid+ep` |
| 本机 curl 1234 不通 | 远程 engine 未启动；或配置里 `[portal].host` 被设为 `127.0.0.1`（则只有远程本机能访问） |
| 启动后端口仍被占用 | systemd 单元又被拉起：`ssh root@cent7.dev.vm systemctl stop design-cloud-engine` |
| 上传二进制很慢 | `scp` 已启用 `-C` 压缩（调试二进制约 2x）；网络本身受限时属正常 |
