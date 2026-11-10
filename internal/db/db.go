package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"

	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Store holds shared database infrastructure used by domain repos.
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
		// 开启底层驱动错误向 GORM 标准错误转义（跨 Postgres 与 SQLite 统一为 gorm.ErrDuplicatedKey 等）
		TranslateError: true,
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
		// 连接级开启 _txlock=immediate：ncruces/go-sqlite3 将事务以 BEGIN IMMEDIATE 开启，
		// 避免 GORM Transaction() 默认 deferred 模式在高并发（如连接配额计数与写入）下发生写偏斜与升级锁失败。
		// 这是连接级开关，影响该库上所有事务，属有意接受（单机部署更安全）。
		sqliteDSN := c.Path
		if strings.Contains(sqliteDSN, "?") {
			sqliteDSN += "&_txlock=immediate"
		} else {
			sqliteDSN += "?_txlock=immediate"
		}
		gdb, dbConnErr = gorm.Open(gormlite.Open(sqliteDSN), gormCfg)

	case config.DatabaseTypePostgres:
		log.Info("Connecting to Postgres database", "host", c.Host, "port", c.Port, "user", c.User, "dbname", c.Name)

		pgDSN := postgresDSN(c)

		// PreferSimpleProtocol：避免扩展协议预处理语句与连接池复用冲突（尤其 PgBouncer
		// transaction 模式）。GORM PrepareStmt 仍缓存 SQL 字符串，但驱动侧不会走 binary
		// prepared statement；改回扩展协议需一并评估连接池/中间件兼容性。
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

func postgresDSN(c config.Database) string {
	host := c.Host
	if c.Port != "" {
		host = net.JoinHostPort(c.Host, c.Port)
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   host,
		Path:   "/" + c.Name,
	}
	query := url.Values{}
	query.Set("sslmode", c.SSLMode)
	if c.SSLCAFile != "" {
		query.Set("sslrootcert", c.SSLCAFile)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

type Repos struct {
	Setting            *SettingRepo
	User               *UserRepo
	Session            *SessionRepo
	Ban                *BanRepo
	DownloadRequest    *DownloadRequestRepo
	ConnectionResource *ConnectionResourceRepo
	Connection         *ConnectionRepo
	Node               *NodeRepo
	App                *AppRepo
	License            *LicenseRepo
	TaskLease          *TaskLeaseRepo
	Health             *HealthRepo
	Execution          *ExecutionRepo
	Storage            *StorageRepo
	Ticket             *TicketRepo
	TicketProject      *TicketProjectRepo
	CacheVersion       *CacheVersionRepo
	AlertHistory       *AlertHistoryRepo
}

func NewRepos(store *Store) *Repos {
	return &Repos{
		Setting:            &SettingRepo{store: store},
		User:               &UserRepo{store: store},
		Session:            &SessionRepo{store: store},
		Ban:                &BanRepo{store: store},
		DownloadRequest:    &DownloadRequestRepo{store: store},
		ConnectionResource: &ConnectionResourceRepo{store: store},
		Connection:         &ConnectionRepo{store: store},
		Node:               &NodeRepo{store: store},
		App:                &AppRepo{store: store},
		License:            &LicenseRepo{store: store},
		TaskLease:          &TaskLeaseRepo{store: store},
		Health:             &HealthRepo{store: store},
		Execution:          &ExecutionRepo{store: store},
		Storage:            &StorageRepo{store: store},
		Ticket:             &TicketRepo{store: store},
		TicketProject:      &TicketProjectRepo{store: store},
		CacheVersion:       &CacheVersionRepo{store: store},
		AlertHistory:       &AlertHistoryRepo{store: store},
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
		&model.ConnectionResource{},
		&model.UserConnectionResource{},
		&model.Connection{},
		&model.Node{},
		&model.NodeRuntime{},
		&model.DownloadRequest{},
		&model.Ban{},
		&model.Setting{},
		&model.LicenseService{},
		&model.TaskLease{},
		&model.ExecutionRecord{},
		&model.StorageReport{},
		&model.Ticket{},
		&model.TicketProject{},
		&model.TicketComment{},
		&model.TicketEvent{},
		&model.CacheVersion{},
		&model.AlertHistory{},
	); err != nil {
		return err
	}
	m.ensureUsernameTrigramIndex()
	return m.initCacheVersions()
}

// ensureUsernameTrigramIndex 在 PostgreSQL 已安装 pg_trgm 扩展时为 LOWER(username)
// 建立 GIN trigram 索引，加速工单指派人的包含匹配。扩展由 DBA 按需安装；
// 未安装、非 PostgreSQL 或建索引失败时仅记录日志，不影响启动。
func (m *Store) ensureUsernameTrigramIndex() {
	if m.db.Dialector.Name() != "postgres" {
		return
	}
	var installed bool
	if err := m.db.Raw("SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')").Scan(&installed).Error; err != nil || !installed {
		return
	}
	if err := m.db.Exec("CREATE INDEX IF NOT EXISTS idx_users_username_trgm ON users USING gin (LOWER(username) gin_trgm_ops)").Error; err != nil {
		m.log.Warn("failed to create username trigram index", "error", err)
	}
}

func (m *Store) initCacheVersions() error {
	for _, n := range AllCacheVersionTopics {
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

// --- Portal 列表分页（与 httputil.ParsePage 一致；硬编码，非 settings）---

const (
	DefaultListLimit = 500
	MaxListLimit     = 2000
)

// ErrInvalidLimit is returned when a paginated repository method receives limit <= 0.
// Callers must pass an explicit positive page size; full scans use dedicated ListAll*
// methods, and batch maintenance methods require the caller's batch size.
var ErrInvalidLimit = errors.New("limit must be greater than 0")

func validateLimit(limit int) error {
	if limit <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidLimit, limit)
	}
	return nil
}

// ResolveListLimit applies DefaultListLimit when limit <= 0, then clamps to [1, MaxListLimit].
func ResolveListLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	return min(MaxListLimit, max(1, limit))
}

// CapListLimit caps a positive page size to MaxListLimit.
func CapListLimit(limit int) int {
	return min(limit, MaxListLimit)
}

// PageSlice returns a page from an in-memory list (e.g. after post-SQL filters such as node groups).
func PageSlice[T any](items []T, limit, offset int) (page []T, total int) {
	total = len(items)
	if offset >= total {
		return []T{}, total
	}
	end := min(offset+limit, total)
	return items[offset:end], total
}
