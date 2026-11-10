# PostgreSQL 阶梯式 SQL 迁移 (`deployment/db/migrations/`)

因 Go 源码与 `Store.Migrate()`（GORM `AutoMigrate`）已全面移除历史兼容别名与自动数据搬迁逻辑，生产 PostgreSQL 存量库升级前需执行本目录下的 SQL 迁移脚本（开发测试用的 SQLite 不提供存量迁移脚本，直接重建即可）。

## 目录与命名约定

采用 **`<序号>_from_<源版本>_to_<目标版本>.sql`** 链式命名（对齐主次版本线）：

```text
deployment/db/migrations/
├── migrate.sh                          # 按 --from 串联执行后续步骤
└── 001_from_0.1.x_to_0.2.x.sql         # 0.1.x -> 0.2.x 全量架构收敛（现行 model）
```

> 当前 **仅一份** 迁移脚本：从 0.1.x 一次对齐到现行 0.2.x model（含登录 IP 删除、连接 `last_active_at`、授权表 `username` 主键等）。后续大/次版本再追加下一序号；**不修改已发布的历史步骤**。脚本均为纯 SQL 事务 + `information_schema` 幂等检查，可安全重跑。

## 使用方法

1. **停止全部 `design-cloud-engine` 进程**。
2. **备份数据库**（`pg_dump`）。
3. **使用 `migrate.sh` 从当前版本起执行**（`--from` 支持 `0.1.x` 或具体版本号如 `0.1.21`；可用 `--dry-run` 先预览）：

```bash
# 方式 1：使用 ~/.pgpass（建议 chmod 600 ~/.pgpass）自动免密认证
bash deployment/db/migrations/migrate.sh \
  --dsn "postgresql://dce@localhost:5432/dce" \
  --from 0.1.x

# 方式 2：交互式输入密码（-W/--password，或未配 ~/.pgpass 时自动提示一次）
bash deployment/db/migrations/migrate.sh \
  --dsn "postgresql://dce@localhost:5432/dce" \
  --from 0.1.x -W

# 方式 3：指定自定义 pgpass 文件并禁用交互提示
bash deployment/db/migrations/migrate.sh \
  --dsn "postgresql://dce@localhost:5432/dce" \
  --from 0.1.x --pgpass /path/to/.pgpass -w
```

4. **启动新版 `dc-engine`**：空库由 `Store.Migrate()`（`AutoMigrate`）按 `internal/model` 建全表结构；**存量** PostgreSQL 须先完成本目录 SQL，勿指望 AutoMigrate 替代重命名/删列/数据搬迁。
