package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Store holds shared database infrastructure used by domain repos.
// Domain CRUD/query methods should gradually move onto the relevant repo types.
type Store struct {
	db  *gorm.DB
	log *slog.Logger
}

// DB returns the underlying gorm.DB instance (primarily for testing or raw operations).
func (m *Store) DB() *gorm.DB {
	return m.db
}

// NewStore establishes a database connection, configures the connection pool, and returns a ready-to-use Store.
func NewStore(c config.Database, logger *slog.Logger) (*Store, error) {
	log := logger.With("component", "database")
	var gdb *gorm.DB
	var dbConnErr error

	// GORM 基础配置（SQLite 和 PostgreSQL 共用）
	gormCfg := &gorm.Config{
		// 禁用默认事务：写操作不再自动包裹事务，写性能提升约 30%
		// 需要事务的场景请显式使用 db.Transaction()
		SkipDefaultTransaction: true,
		// 开启预编译缓存：高并发场景下 SQL 编译结果复用，减少数据库 CPU 开销
		PrepareStmt: true,
		// 禁止 AutoMigrate 创建物理外键：本项目统一使用逻辑外键（索引 + 应用层约束）
		DisableForeignKeyConstraintWhenMigrating: true,
		// 慢查询监控：超过 200ms 的 SQL 通过 slog 输出 Warn 日志
		Logger: gormlogger.NewSlogLogger(
			logger.With("scope", "gorm"),
			gormlogger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  gormlogger.Warn,
				IgnoreRecordNotFoundError: true,
			},
		),
	}

	switch c.Type {
	case config.DatabaseTypeSQLite:
		// 连接级开启 _txlock=immediate：modernc.org/sqlite 驱动会将所有事务以 BEGIN IMMEDIATE 开启，
		// 避免 GORM Transaction() 默认 deferred 模式在高并发（如连接配额计数与写入）下发生写偏斜与升级锁失败。
		// 这是连接级开关，影响该库上所有事务，属有意接受（单机部署更安全）。
		sqliteDSN := c.Path
		if strings.Contains(sqliteDSN, "?") {
			sqliteDSN += "&_txlock=immediate"
		} else {
			sqliteDSN += "?_txlock=immediate"
		}
		gdb, dbConnErr = gorm.Open(sqlite.Open(sqliteDSN), gormCfg)

	case config.DatabaseTypePostgres:
		log.Info("Connecting to Postgres database", "host", c.Host, "port", c.Port, "user", c.User, "dbname", c.Name)

		pgDSN := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
			c.Host, c.User, c.Password, c.Name, c.Port, c.SSLMode)
		if c.SSLCAFile != "" {
			pgDSN += " sslrootcert=" + c.SSLCAFile
		}

		gdb, dbConnErr = gorm.Open(postgres.New(postgres.Config{
			DSN:                  pgDSN,
			PreferSimpleProtocol: true,
		}), gormCfg)
		if dbConnErr != nil {
			log.Error("failed to connect to postgres", "addr", c.Host+":"+c.Port, "error", dbConnErr)
		}
	default:
		return nil, fmt.Errorf("unsupported database type: %s", c.Type)
	}

	if dbConnErr != nil || gdb == nil {
		return nil, fmt.Errorf("failed to connect to database: %w", dbConnErr)
	}

	sqlDB, err := gdb.DB()
	if err != nil || sqlDB == nil {
		return nil, err
	}

	if c.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(c.MaxIdleConns)
	}
	if c.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(c.MaxOpenConns)
	}
	if c.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(c.ConnMaxLifetime) * time.Second)
	}
	// 空闲连接超过此时间会被回收，防止 PG 端断开后连接池仍持有无效连接
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	store := &Store{db: gdb, log: log}
	log.Info("Successfully connected to database", "type", c.Type)
	return store, nil
}

type Repos struct {
	Setting       *SettingRepo
	User          *UserRepo
	Session       *SessionRepo
	Ban           *BanRepo
	File          *FileRepo
	Connection    *ConnectionRepo
	Node          *NodeRepo
	App           *AppRepo
	License       *LicenseRepo
	TaskLease     *TaskLeaseRepo
	Health        *HealthRepo
	Execution     *ExecutionRepo
	Storage       *StorageRepo
	Ticket        *TicketRepo
	TicketProject *TicketProjectRepo
	CacheVersion  *CacheVersionRepo
	AlertHistory  *AlertHistoryRepo
}

func NewRepos(store *Store) *Repos {
	return &Repos{
		Setting:       &SettingRepo{store: store},
		User:          &UserRepo{store: store},
		Session:       &SessionRepo{store: store},
		Ban:           &BanRepo{store: store},
		File:          &FileRepo{store: store},
		Connection:    &ConnectionRepo{store: store},
		Node:          &NodeRepo{store: store},
		App:           &AppRepo{store: store},
		License:       &LicenseRepo{store: store},
		TaskLease:     &TaskLeaseRepo{store: store},
		Health:        &HealthRepo{store: store},
		Execution:     &ExecutionRepo{store: store},
		Storage:       &StorageRepo{store: store},
		Ticket:        &TicketRepo{store: store},
		TicketProject: &TicketProjectRepo{store: store},
		CacheVersion:  &CacheVersionRepo{store: store},
		AlertHistory:  &AlertHistoryRepo{store: store},
	}
}

type HealthRepo struct {
	store *Store
}

// Close closes the database connection.
func (m *Store) Close() error {
	if m == nil {
		return nil
	}
	sqlDB, err := m.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Migrate runs schema migration and seeds initial data.
func (m *Store) Migrate() error {
	if err := m.db.AutoMigrate(
		&model.App{},
		&model.Session{},
		&model.User{},
		&model.Connection{},
		&model.Node{},
		&model.NodeResourceReport{},
		&model.Download{},
		&model.BanRecord{},
		&model.Setting{},
		&model.LicenseService{},
		&model.TaskLease{},
		&model.ExecutionRecord{},
		&model.StorageReport{},
		&model.Ticket{},
		&model.TicketProject{},
		&model.CacheVersion{},
		&model.AlertHistory{},
	); err != nil {
		return err
	}
	if err := m.initCacheVersions(); err != nil {
		return err
	}
	return nil
}

func (m *Store) initCacheVersions() error {
	names := []string{"settings", "session", "ban", "id_mappings"}
	for _, n := range names {
		err := m.db.FirstOrCreate(&model.CacheVersion{Name: n, Version: 0}, model.CacheVersion{Name: n}).Error
		if err != nil {
			return fmt.Errorf("init cache_versions for %s: %w", n, err)
		}
	}
	return nil
}

// --- 数据库健康检查相关 ---

func (r *HealthRepo) Check(ctx context.Context) (latencyMs int64, err error) {
	m := r.store
	sqlDB, err := m.db.DB()
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if err = sqlDB.PingContext(ctx); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}

// PoolStats returns a snapshot of the connection pool metrics.
// This reads from in-memory counters inside database/sql and requires no
// network round-trip.
func (r *HealthRepo) PoolStats() sql.DBStats {
	m := r.store
	sqlDB, err := m.db.DB()
	if err != nil {
		return sql.DBStats{}
	}
	return sqlDB.Stats()
}
