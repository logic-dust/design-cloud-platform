package connection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
	"design-cloud-engine/internal/server/service/user"
)

var (
	ErrInvalidConnectionInfo   = errors.New("invalid connection info")
	ErrConnectionAlreadyExists = errors.New("connection already exists")

	ErrCleanupServiceUnavailable = errors.New("connection cleanup service is not configured")
	ErrCleanupLeaseHeld          = errors.New("connection cleanup lease is held by another node")
)

const (
	defaultUserRefreshCooldown      = 15 * time.Second
	maxUserRefreshCooldownEntries   = 1024
	minUserRefreshCooldownRetention = time.Hour
)

// Service provides connection management and monitoring logic.
type Service struct {
	connectionRepo *db.ConnectionRepo
	nodeRepo       *db.NodeRepo
	leaseRepo      *db.TaskLeaseRepo
	ownerID        string
	execSvc        execution.Executor
	userSvc        *user.Service
	logger         *slog.Logger

	publisher push.Publisher

	settingsSvc *settings.Service

	collectTrigger contract.CollectTrigger

	refreshMu            sync.Mutex
	lastUserRefreshAt    map[string]time.Time
	lastAdminCollectAt   time.Time
	userRefreshCooldown  time.Duration
	adminCollectCooldown time.Duration
	now                  func() time.Time

	// connListCache avoids repeated DB queries when the frontend polls GET /api/connections.
	// Key = username，由会话推导（非请求参数），键空间即真实用户集合，容量上限
	// 等于"用户数 × 各自连接列表"，本身就随真实数据量有界——因此刻意不设条目上限：
	// 设小了会让轮询在淘汰后回源 DB，设大了永不触发，两者都无收益。
	// TTL 跟随 connection 域 ConnectionListCacheTTL；写入路径（create/delete/collect）失效。
	connListCache *cache.TTLCache[string, []model.Connection]

	tunnelCloser TunnelCloser

	graceMu       sync.RWMutex
	lastControlAt map[uint]time.Time
}

// TunnelCloser defines the interface for terminating active proxy sessions by connection ID.
type TunnelCloser interface {
	CloseByConnectionID(connectionID uint) error
}

// Option configures a Service at construction time.
type Option func(*Service)

// WithNodeRepo injects the node repository for validating target node existence and resolving node host.
func WithNodeRepo(nodeRepo *db.NodeRepo) Option {
	return func(s *Service) {
		s.nodeRepo = nodeRepo
	}
}

// WithLease configures the distributed lease repository and node owner ID for connection cleanup.
func WithLease(leaseRepo *db.TaskLeaseRepo, ownerID string) Option {
	return func(s *Service) {
		s.leaseRepo = leaseRepo
		s.ownerID = ownerID
	}
}

// WithCollectTrigger wires the cluster collect trigger facade used to fan-out VNC probes to agents.
func WithCollectTrigger(t contract.CollectTrigger) Option {
	return func(s *Service) {
		s.collectTrigger = t
	}
}

// WithPublisher wires the SSE event publisher for connection status changes.
func WithPublisher(p push.Publisher) Option {
	return func(s *Service) {
		s.publisher = p
	}
}

// WithSettingsSvc 注入 settings 服务：用于动态读取 ConnectionListCacheTTL。
func WithSettingsSvc(settingsSvc *settings.Service) Option {
	return func(s *Service) {
		s.settingsSvc = settingsSvc
	}
}

// SetTunnelCloser sets the proxy tunnel closer dynamically after initialization.
func (s *Service) SetTunnelCloser(tc TunnelCloser) {
	s.tunnelCloser = tc
}

func (s *Service) markControlled(connID uint) {
	s.graceMu.Lock()
	defer s.graceMu.Unlock()
	if s.lastControlAt == nil {
		s.lastControlAt = make(map[uint]time.Time)
	}
	s.lastControlAt[connID] = s.clockNow()
}

func (s *Service) isWithinGracePeriod(connID uint, window time.Duration) bool {
	s.graceMu.RLock()
	defer s.graceMu.RUnlock()
	if s.lastControlAt == nil {
		return false
	}
	t, ok := s.lastControlAt[connID]
	if !ok {
		return false
	}
	return s.clockNow().Sub(t) < window
}

// NewService creates a new connection service.
func NewService(execSvc execution.Executor, userSvc *user.Service, connectionRepo *db.ConnectionRepo, logger *slog.Logger, opts ...Option) (*Service, error) {
	if execSvc == nil {
		return nil, fmt.Errorf("connection: execution service is required")
	}
	if userSvc == nil {
		return nil, fmt.Errorf("connection: user service is required")
	}
	if connectionRepo == nil {
		return nil, fmt.Errorf("connection: connection repo is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("connection: logger is required")
	}
	svc := &Service{
		execSvc:        execSvc,
		userSvc:        userSvc,
		connectionRepo: connectionRepo,
		logger:         logger,
		now:            time.Now,
		// 缓存级 TTL = 代码默认值，作为 settings 不可读/未配置时的唯一兜底来源
		// （调用方无需自备默认值，传 0 即走这里）。
		connListCache: cache.NewTTLCache[string, []model.Connection](
			time.Duration(schema.ConnectionListCacheTTLDefault) * time.Second).EnableJanitor(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// clockNow returns the current time, using the injectable clock for tests.
func (s *Service) clockNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// notifyOwnerRefresh 推送连接列表变更给归属用户（刷新列表）。
// connection 是 per-user 数据：私有连接只推给归属者，其他用户（含管理员）收到
// 无意义且泄露他人连接信息。共享连接（owner 为空）增删影响所有用户，广播给
// 全部在线用户。
//
// 寻址：hub 按 username 路由（与 Register 一致），connection.Username
// 即业务唯一标识，直接推送，零查询。
func (s *Service) notifyOwnerRefresh(ctx context.Context, owner string, connID uint) {
	if s.publisher == nil {
		return
	}
	msg := push.Message{
		Name: push.EventConnection,
		Payload: push.EventPayload{
			Action:   "refresh",
			TargetID: formatConnID(connID),
		},
	}
	if owner == "" {
		s.publisher.Broadcast(ctx, msg)
		return
	}
	s.publisher.PublishToUser(ctx, []string{owner}, msg)
}

// effectiveMaxConnections returns the max allowed non-shared connections for a user.
// Returns 0 if unlimited.
func (s *Service) effectiveMaxConnections(ctx context.Context, username string) (int, error) {
	if s.userSvc != nil && username != "" {
		if u, err := s.userSvc.GetDBUser(ctx, username); err == nil {
			if u.MaxConnections > 0 {
				return int(u.MaxConnections), nil
			}
		}
	}
	if s.settingsSvc != nil {
		return s.settingsSvc.MaxConnectionsPerUser(ctx)
	}
	return 0, nil
}
