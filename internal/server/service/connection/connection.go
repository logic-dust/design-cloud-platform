package connection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/server/push"
	"design-cloud-engine/internal/server/service/user"
)

// SettingsReader is the settings subset connection.Service needs.
type SettingsReader interface {
	MaxPerUserCap(ctx context.Context) (int, error)
	GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error)
	GetEffectiveAgentSettingsForNode(ctx context.Context, nodeID string) (schema.AgentSettings, error)
}

var (
	ErrConnectionAlreadyExists   = errors.New("connection already exists")
	ErrCleanupServiceUnavailable = errors.New("connection cleanup service is not configured")
	ErrCleanupLeaseHeld          = errors.New("connection cleanup lease is held by another node")
	ErrResourceInUse             = errors.New("connection resource in use")
	ErrResourceNameConflict      = errors.New("connection resource name already exists")
	ErrPlacerNoNodeAvailable     = errors.New("no available node for connection")
	ErrCandidateSetEmpty         = errors.New("candidate node set is empty")
	ErrNoHealthyNode             = errors.New("no healthy online nodes")
	ErrAllNodesOccupied          = errors.New("all candidate nodes are already occupied")
	ErrNodeConnectionLimit       = errors.New("node connection limit reached")
	ErrTargetNodeUnavailable     = errors.New("target node is not available")
	ErrDisplayOccupied           = errors.New("display is already occupied")
	ErrDisplayExhausted          = errors.New("display_exhausted")
	ErrDisplayConflict           = errors.New("display_conflict")
	ErrConnectionStillStarting   = errors.New("connection is still starting")
	ErrConnectionStateUnknown    = errors.New("connection state unknown")
)

const AllUsers = "all"

// Service provides connection management and monitoring logic.
type Service struct {
	connectionRepo         *db.ConnectionRepo
	connectionResourceRepo *db.ConnectionResourceRepo
	nodeRepo               *db.NodeRepo
	leaseRepo              *db.TaskLeaseRepo
	ownerID                string
	execSvc                execution.Executor
	userSvc                *user.Service
	logger                 *slog.Logger

	placer *Placer

	publisher push.Publisher

	settingsSvc SettingsReader

	collectTrigger contract.CollectTrigger

	proxyConnectionCloser ProxyConnectionCloser

	resolvePortalUser func(ctx context.Context, osUser string) (string, bool)

	refreshMu            sync.Mutex
	lastAdminCollectAt   time.Time
	adminCollectCooldown time.Duration
	lastUserRefreshAt    map[string]time.Time
	userRefreshCooldown  time.Duration

	touchMu     sync.Mutex
	lastTouchAt map[uint]time.Time

	tiedWarnMu sync.Mutex
	tiedWarned map[string]string // "nodeID:username:display" -> sorted comma-separated candidate resource IDs

	xrdpReapMu         sync.Mutex
	lastXRDPReapKillAt map[string]time.Time
}

type ProxyConnectionCloser interface {
	CloseAllByConnectionID(connectionID uint) error
}

type Option func(*Service)

func WithNodeRepo(nodeRepo *db.NodeRepo) Option {
	return func(s *Service) {
		s.nodeRepo = nodeRepo
	}
}

func WithResourceRepo(resourceRepo *db.ConnectionResourceRepo) Option {
	return func(s *Service) {
		s.connectionResourceRepo = resourceRepo
	}
}

func WithLease(leaseRepo *db.TaskLeaseRepo, ownerID string) Option {
	return func(s *Service) {
		s.leaseRepo = leaseRepo
		s.ownerID = ownerID
	}
}

func WithCollectTrigger(t contract.CollectTrigger) Option {
	return func(s *Service) {
		s.collectTrigger = t
	}
}

func WithPublisher(p push.Publisher) Option {
	return func(s *Service) {
		s.publisher = p
	}
}

func WithSettingsSvc(settingsSvc SettingsReader) Option {
	return func(s *Service) {
		s.settingsSvc = settingsSvc
	}
}

func WithProxyConnectionCloser(closer ProxyConnectionCloser) Option {
	return func(s *Service) {
		s.proxyConnectionCloser = closer
	}
}

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
		lastTouchAt:    make(map[uint]time.Time),
		resolvePortalUser: func(ctx context.Context, osUser string) (string, bool) {
			osUser = strings.TrimSpace(osUser)
			if osUser == "" {
				return "", false
			}
			user, err := userSvc.GetDBUser(ctx, osUser)
			if err != nil || user.Username == "" {
				return "", false
			}
			return user.Username, true
		},
	}

	for _, opt := range opts {
		opt(svc)
	}

	svc.placer = NewPlacer(svc.nodeRepo, svc.connectionRepo).WithSettings(svc.settingsSvc)

	return svc, nil
}

func (s *Service) portalUsername(ctx context.Context, osUser string) (string, bool) {
	osUser = strings.TrimSpace(osUser)
	if osUser == "" || s == nil {
		return "", false
	}
	if s.resolvePortalUser != nil {
		return s.resolvePortalUser(ctx, osUser)
	}
	if s.userSvc == nil {
		return "", false
	}
	user, err := s.userSvc.GetDBUser(ctx, osUser)
	if err != nil || user.Username == "" {
		return "", false
	}
	return user.Username, true
}
