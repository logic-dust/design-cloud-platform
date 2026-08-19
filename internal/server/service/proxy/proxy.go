package proxy

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
)

var (
	ErrConnectionNotFound            = errors.New("proxy connection not found")
	ErrProxyTunnelNotFound           = errors.New("proxy tunnel not found")
	ErrProxyServiceDisabled          = errors.New("proxy service is disabled")
	ErrProxyProtocolNotAllowed       = errors.New("proxy protocol is not allowed")
	ErrProxyMaxTunnelsReached        = errors.New("proxy max tunnels reached")
	ErrProxyMaxTunnelsPerUserReached = errors.New("proxy max tunnels per user reached")
	ErrProxyTunnelActivateFailed     = errors.New("proxy tunnel activation failed: OTP invalid, expired, or already used")
)

// Status represents the lifecycle state of a proxy tunnel.
type Status string

const (
	StatusPending Status = "pending"
	StatusActive  Status = "active"
)

// Tunnel is a runtime-only proxy tunnel channel.
// Protocol, Host, Port, and Username are snapshotted from the Connection
// at creation time so the OTP phase can proceed without a DB round-trip.
type Tunnel struct {
	ConnectionID uint              `json:"connectionId"`
	OTP          string            `json:"otp,omitempty"`
	Status       Status            `json:"status"`
	ExpiresAt    time.Time         `json:"expiresAt"`
	Protocol     contract.Protocol `json:"-"`
	Host         string            `json:"-"`
	Port         uint              `json:"-"`
	Username     string            `json:"-"`
}

func (t *Tunnel) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// ProtocolHandler defines the interface for protocol-specific proxy behavior.
type ProtocolHandler interface {
	// Name returns the protocol name (e.g., "VNC", "RDP", "SSH")
	Name() string

	// Connect establishes a TCP connection to the backend target
	Connect(ctx context.Context, target string) (net.Conn, error)
}

// BaseProtocolHandler provides common functionality for all protocols
type BaseProtocolHandler struct {
	name        string
	dialTimeout time.Duration
}

func newBaseProtocolHandler(name string) *BaseProtocolHandler {
	return &BaseProtocolHandler{
		name:        name,
		dialTimeout: 10 * time.Second,
	}
}

func (h *BaseProtocolHandler) Name() string {
	return h.name
}

func (h *BaseProtocolHandler) Connect(ctx context.Context, target string) (net.Conn, error) {
	var d net.Dialer
	d.Timeout = h.dialTimeout
	return d.DialContext(ctx, "tcp", target)
}

// ProtocolRegistry manages available protocol handlers
type ProtocolRegistry struct {
	handlers map[contract.Protocol]ProtocolHandler
}

func newProtocolRegistry() *ProtocolRegistry {
	r := &ProtocolRegistry{
		handlers: make(map[contract.Protocol]ProtocolHandler),
	}

	// Register built-in protocols
	r.Register(newBaseProtocolHandler(string(contract.VNCProtocol)))
	r.Register(newBaseProtocolHandler(string(contract.RDPProtocol)))
	r.Register(newBaseProtocolHandler(string(contract.SSHProtocol)))

	return r
}

func (r *ProtocolRegistry) Register(handler ProtocolHandler) {
	r.handlers[contract.Protocol(handler.Name())] = handler
}

func (r *ProtocolRegistry) Get(protocol contract.Protocol) (ProtocolHandler, error) {
	handler, ok := r.handlers[protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
	return handler, nil
}

// CreateTunnelRequest carries the caller-supplied inputs for opening a new proxy tunnel.
// Target host and protocol are resolved internally from the Connection record.
type CreateTunnelRequest struct {
	ConnectionID uint
}

type Service struct {
	connectionRepo   *db.ConnectionRepo
	store            *tunnelStore
	protocolRegistry *ProtocolRegistry
	settingsSvc      *settings.Service
	logger           *slog.Logger
}

// NewService initializes the proxy service in WebSocket-only mode.
func NewService(settingsSvc *settings.Service, connectionRepo *db.ConnectionRepo, logger *slog.Logger) (*Service, error) {
	s := &Service{
		connectionRepo:   connectionRepo,
		store:            newTunnelStore(),
		settingsSvc:      settingsSvc,
		logger:           logger,
		protocolRegistry: newProtocolRegistry(),
	}

	return s, nil
}

func (s *Service) Start() error {
	s.logger.Info("Proxy service started", "mode", "ws")
	return nil
}

func (s *Service) Stop() error {
	s.logger.Info("Proxy service stopped")
	return nil
}

// Create provisions a new proxy tunnel for WebSocket proxying.
// Target host and protocol are derived from the Connection record to avoid caller redundancy.
func (s *Service) Create(ctx context.Context, req *CreateTunnelRequest) (*Tunnel, error) {
	protocol, targetHost, targetPort, conn, err := s.resolveConnectionTarget(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}

	tunnelSettings, err := s.settingsSvc.GetTunnelSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tunnel settings error: %w", err)
	}
	if err := ensureProtocolAllowed(tunnelSettings, protocol); err != nil {
		return nil, err
	}

	existingTunnel, existingErr := s.store.GetByConnectionID(req.ConnectionID)
	hasCurrentTunnel := existingErr == nil &&
		(existingTunnel.Status == StatusPending || existingTunnel.Status == StatusActive)
	if err := s.checkTunnelQuota(tunnelSettings, conn, hasCurrentTunnel); err != nil {
		return nil, err
	}

	// Replace any existing active/pending tunnel for this connection (silent replacement)
	if err := s.store.CloseByConnectionID(req.ConnectionID); err != nil {
		if !errors.Is(err, common.ErrNotFound) {
			return nil, err
		}
	}

	return s.newProxyTunnel(ctx, req.ConnectionID, conn.Username, protocol, targetHost, targetPort, tunnelSettings.OtpTTL)
}

// resolveConnectionTarget loads the connection and validates the protocol, host,
// and port needed for proxying.
func (s *Service) resolveConnectionTarget(ctx context.Context, connectionID uint) (contract.Protocol, string, uint, model.Connection, error) {
	conn, err := s.connectionRepo.GetByID(ctx, connectionID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return "", "", 0, model.Connection{}, ErrConnectionNotFound
		}
		return "", "", 0, model.Connection{}, fmt.Errorf("get connection error: %w", err)
	}

	protocol := conn.Protocol
	if protocol == "" {
		return "", "", 0, model.Connection{}, fmt.Errorf("%w: proxy protocol not defined", fs.ErrInvalid)
	}
	if _, err := s.protocolRegistry.Get(protocol); err != nil {
		return "", "", 0, model.Connection{}, fmt.Errorf("%w: proxy protocol %s is not supported", fs.ErrInvalid, protocol)
	}

	targetHost := conn.Host
	if targetHost == "" || targetHost == "127.0.0.1" || targetHost == "localhost" {
		s.logger.Debug("Proxy target host falls back to target node", "connection", conn.ID, "original_host", conn.Host, "target_node", conn.TargetNodeID)
		targetHost = conn.TargetNodeID
	}
	if targetHost == "" {
		return "", "", 0, model.Connection{}, fmt.Errorf("%w: proxy target host not defined", fs.ErrInvalid)
	}

	targetPort := conn.Port
	if targetPort <= 0 || targetPort > 65535 {
		return "", "", 0, model.Connection{}, fmt.Errorf("%w: proxy target port is invalid: %d", fs.ErrInvalid, targetPort)
	}

	return protocol, targetHost, targetPort, conn, nil
}

// checkTunnelQuota enforces global MaxTunnels and per-user MaxTunnelsPerUser limits.
func (s *Service) checkTunnelQuota(tunnelSettings schema.TunnelSettings, conn model.Connection, hasCurrentTunnel bool) error {
	if tunnelSettings.MaxTunnels > 0 {
		activeCount := s.store.CountActive()
		if hasCurrentTunnel && activeCount > 0 {
			activeCount--
		}
		if activeCount >= int64(tunnelSettings.MaxTunnels) {
			return ErrProxyMaxTunnelsReached
		}
	}

	if tunnelSettings.MaxTunnelsPerUser > 0 {
		userCount := s.countActiveByUser(conn.Username)
		if hasCurrentTunnel && userCount > 0 {
			userCount--
		}
		if userCount >= int64(tunnelSettings.MaxTunnelsPerUser) {
			return ErrProxyMaxTunnelsPerUserReached
		}
	}
	return nil
}

// newProxyTunnel creates the in-memory tunnel, persists it, and updates the
// connection's last access time.
func (s *Service) newProxyTunnel(ctx context.Context, connectionID uint, username string, protocol contract.Protocol, targetHost string, targetPort uint, otpTTL int) (*Tunnel, error) {
	otpTTL = cmp.Or(otpTTL, 60)

	now := time.Now()
	tunnel := &Tunnel{
		ConnectionID: connectionID,
		OTP:          crypto.GenSecret(24), // 256-bit cryptographically secure random OTP
		Status:       StatusPending,
		ExpiresAt:    now.Add(time.Duration(otpTTL) * time.Second),
		Protocol:     protocol,
		Host:         targetHost,
		Port:         targetPort,
		Username:     username,
	}

	s.store.Create(tunnel)
	if err := s.connectionRepo.UpdateFields(ctx, connectionID, map[string]any{"last_accessed_at": now}); err != nil {
		return nil, fmt.Errorf("touch connection access time: %w", err)
	}

	s.logger.Info("Proxy tunnel created", "connection", connectionID, "protocol", protocol, "host", targetHost, "port", targetPort)

	return tunnel, nil
}

func (s *Service) CloseByConnectionID(connectionID uint) error {
	if err := s.store.CloseByConnectionID(connectionID); err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return ErrProxyTunnelNotFound
		}
		return err
	}
	return nil
}

// GetByOTP retrieves a tunnel by its one-time password.
func (s *Service) GetByOTP(otp string) (*Tunnel, error) {
	tunnel, err := s.store.GetByOTP(otp)
	if errors.Is(err, common.ErrNotFound) {
		return nil, ErrProxyTunnelNotFound
	}
	return tunnel, err
}

// Activate transitions a pending tunnel to active status.
func (s *Service) Activate(otp string) (*Tunnel, error) {
	now := time.Now()
	tunnel, err := s.store.Activate(otp, now)
	if err != nil {
		if errors.Is(err, ErrProxyTunnelActivateFailed) {
			return nil, fmt.Errorf("%w: proxy tunnel is no longer valid", fs.ErrPermission)
		}
		if errors.Is(err, common.ErrNotFound) {
			return nil, ErrProxyTunnelNotFound
		}
		return nil, err
	}
	return tunnel, nil
}

// GetProtocolHandler returns the handler for a specific proxy protocol.
func (s *Service) GetProtocolHandler(protocol contract.Protocol) (ProtocolHandler, error) {
	return s.protocolRegistry.Get(protocol)
}

func (s *Service) EnsureProtocolAllowed(ctx context.Context, protocol contract.Protocol) error {
	tunnelSettings, err := s.settingsSvc.GetTunnelSettings(ctx)
	if err != nil {
		return fmt.Errorf("get tunnel settings error: %w", err)
	}
	return ensureProtocolAllowed(tunnelSettings, protocol)
}

// GetActiveByConnectionID returns the current tunnel for a connection.
func (s *Service) GetActiveByConnectionID(connID uint) (*Tunnel, error) {
	tunnel, err := s.store.GetByConnectionID(connID)
	if errors.Is(err, common.ErrNotFound) {
		return nil, ErrProxyTunnelNotFound
	}
	return tunnel, err
}

// PruneExpiredPending removes expired pending tunnels from the in-memory store.
// Intended to be called periodically by the cleanup service on every node.
func (s *Service) PruneExpiredPending(now time.Time) int {
	return s.store.PruneExpiredPending(now)
}

// countActiveByUser counts active/pending proxy tunnels for a user.
// Username is snapshotted onto Tunnel at creation time, so no DB
// round-trip is needed.
func (s *Service) countActiveByUser(username string) int64 {
	tunnels := s.store.List()
	var count int64
	for _, tunnel := range tunnels {
		if tunnel.Status != StatusPending && tunnel.Status != StatusActive {
			continue
		}
		if tunnel.Username == username {
			count++
		}
	}
	return count
}

func ensureProtocolAllowed(tunnelSettings schema.TunnelSettings, protocol contract.Protocol) error {
	if !tunnelSettings.Enabled {
		return ErrProxyServiceDisabled
	}
	if !tunnelSettings.IsProtocolAllowed(protocol) {
		return fmt.Errorf("%w: %s", ErrProxyProtocolNotAllowed, protocol)
	}
	return nil
}
