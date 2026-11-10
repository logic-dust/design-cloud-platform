package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

const bridgeWriteDeadline = 20 * time.Second

var (
	ErrConnectionNotFound             = errors.New("connection not found")
	ErrProxyConnectionNotFound        = errors.New("proxy connection not found")
	ErrProxyServiceDisabled           = errors.New("proxy service is disabled")
	ErrProxyProtocolNotAllowed        = errors.New("proxy protocol is not allowed")
	ErrProxyMaxSessionsReached        = errors.New("proxy max sessions reached")
	ErrProxyMaxSessionsPerUserReached = errors.New("proxy max sessions per user reached")
	ErrNotProxyable                   = errors.New("connection is not proxyable")
	ErrProxyConnectionForbidden       = errors.New("proxy connection access denied")
	ErrNodeConnectionsDisallowed      = errors.New("target node does not allow connections")
	ErrNodeInMaintenance              = errors.New("target node is in maintenance")
	ErrSSHUsesResourceProxy           = errors.New("SSH proxy uses GET /api/connections/ssh?resourceId=")
	ErrSSHDialResolverUnavailable     = errors.New("SSH dial resolver is not configured")
)

// SSHDialTargetResolver selects a live dial target for an SSH resource without
// creating a connections row. Implemented by connection.Service.
type SSHDialTargetResolver interface {
	ResolveSSHDialTarget(ctx context.Context, username string, resourceID uint) (host string, port uint, err error)
}

// Status represents the lifecycle state of a ProxyConnection.
type Status string

const (
	StatusActive Status = "active"
)

// ProxyConnection is a runtime-only mediated access session (代理访问会话).
// Connection-backed tunnels set ConnectionID; SSH resource tunnels set ResourceID
// only (no connections row). The two anchors are mutually exclusive.
type ProxyConnection struct {
	ID           uint              `json:"id"`
	ConnectionID uint              `json:"connectionId,omitempty"`
	ResourceID   uint              `json:"resourceId,omitempty"`
	Requester    string            `json:"requester"`
	Status       Status            `json:"status"`
	StartedAt    time.Time         `json:"startedAt"`
	Protocol     contract.Protocol `json:"-"`
	Host         string            `json:"-"`
	Port         uint              `json:"-"`
	Username     string            `json:"-"`
}

// ProtocolHandler defines the interface for protocol-specific proxy behavior.
type ProtocolHandler interface {
	Name() string
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
	return dialTCPWithRefusedRetry(ctx, target, h.dialTimeout)
}

// ProtocolRegistry manages available protocol handlers
type ProtocolRegistry struct {
	handlers map[contract.Protocol]ProtocolHandler
}

func newProtocolRegistry() *ProtocolRegistry {
	r := &ProtocolRegistry{
		handlers: make(map[contract.Protocol]ProtocolHandler),
	}
	r.Register(contract.VNCProtocol, newBaseProtocolHandler(contract.VNCProtocol.String()))
	r.Register(contract.RDPProtocol, newBaseProtocolHandler(contract.RDPProtocol.String()))
	r.Register(contract.SSHProtocol, newBaseProtocolHandler(contract.SSHProtocol.String()))
	// XRDP speaks RDP on the wire; reuse the RDP byte-tunnel handler.
	r.Register(contract.XRDPProtocol, newBaseProtocolHandler(contract.XRDPProtocol.String()))
	return r
}

func (r *ProtocolRegistry) Register(protocol contract.Protocol, handler ProtocolHandler) {
	r.handlers[protocol] = handler
}

func (r *ProtocolRegistry) Get(protocol contract.Protocol) (ProtocolHandler, error) {
	handler, ok := r.handlers[protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
	return handler, nil
}

// OpenDataPlaneRequest carries caller inputs for opening a proxy data plane.
// Set either ConnectionID (VNC/RDP/XRDP) or ResourceID (SSH resource proxy).
type OpenDataPlaneRequest struct {
	ConnectionID uint
	ResourceID   uint
	Requester    string
}

// PreparedDataPlaneOpen is a validated open plan: session template plus quota
// captured from settings at prepare time (single GetConnectionSettings for the open path).
type PreparedDataPlaneOpen struct {
	Session ProxyConnection
	quota   sessionQuota
}

type Service struct {
	connectionRepo   *db.ConnectionRepo
	nodeRepo         *db.NodeRepo
	store            *proxySessionRegistry
	protocolRegistry *ProtocolRegistry
	settingsSvc      SettingsReader
	sshDial          SSHDialTargetResolver
	logger           *slog.Logger

	touchMu     sync.Mutex
	lastTouchAt map[uint]time.Time
}

func NewService(settingsSvc SettingsReader, connectionRepo *db.ConnectionRepo, nodeRepo *db.NodeRepo, logger *slog.Logger) (*Service, error) {
	return &Service{
		connectionRepo:   connectionRepo,
		nodeRepo:         nodeRepo,
		store:            newProxySessionRegistry(),
		settingsSvc:      settingsSvc,
		logger:           logger,
		protocolRegistry: newProtocolRegistry(),
		lastTouchAt:      make(map[uint]time.Time),
	}, nil
}

// SetSSHDialResolver wires SSH resource placement (called after connection.Service exists).
func (s *Service) SetSSHDialResolver(r SSHDialTargetResolver) {
	if s != nil {
		s.sshDial = r
	}
}

func (s *Service) Start() error {
	s.logger.Info("Proxy service started", "mode", "ws")
	return nil
}

func (s *Service) Stop() error {
	handles := s.store.RemoveAll()
	for _, handle := range handles {
		tcp, ws := handle.Close()
		CloseDetachedSockets(tcp, ws)
	}
	s.logger.Info("Proxy service stopped")
	return nil
}

// BridgeWriteDeadline is the per-write deadline for proxy data-plane copies.
func (s *Service) BridgeWriteDeadline() time.Duration {
	return bridgeWriteDeadline
}

// PrepareDataPlaneOpen validates settings, authorization, and quota without registering a session.
// Quota limits are snapshotted here so RegisterDataPlaneSession does not re-read settings.
func (s *Service) PrepareDataPlaneOpen(ctx context.Context, req *OpenDataPlaneRequest) (*PreparedDataPlaneOpen, error) {
	if req == nil || req.Requester == "" {
		return nil, contract.NewInvalidParamError("requester is required")
	}
	if req.ConnectionID != 0 && req.ResourceID != 0 {
		return nil, contract.NewInvalidParamError("connectionId and resourceId are mutually exclusive")
	}
	if req.ResourceID != 0 {
		return s.prepareResourceDataPlaneOpen(ctx, req)
	}
	if req.ConnectionID == 0 {
		return nil, contract.NewInvalidParamError("connectionId or resourceId is required")
	}

	protocol, targetHost, targetPort, conn, err := s.resolveAuthorizedConnectionTarget(ctx, req.Requester, req.ConnectionID)
	if err != nil {
		return nil, err
	}

	connectionSettings, err := s.settingsSvc.GetConnectionSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection settings error: %w", err)
	}
	proxySettings := connectionSettings.Proxy
	if err := ensureProtocolAllowed(proxySettings, protocol); err != nil {
		return nil, err
	}

	quota := sessionQuota{
		MaxGlobal:    proxySettings.MaxProxyConnections,
		MaxPerUser:   proxySettings.MaxProxyConnectionsPerUser,
		ConnectionID: req.ConnectionID,
		Requester:    req.Requester,
	}
	if err := s.store.CheckQuota(quota); err != nil {
		return nil, err
	}

	now := time.Now()
	return &PreparedDataPlaneOpen{
		Session: ProxyConnection{
			ConnectionID: req.ConnectionID,
			Requester:    req.Requester,
			Status:       StatusActive,
			StartedAt:    now,
			Protocol:     protocol,
			Host:         targetHost,
			Port:         targetPort,
			Username:     conn.Username,
		},
		quota: quota,
	}, nil
}

func (s *Service) prepareResourceDataPlaneOpen(ctx context.Context, req *OpenDataPlaneRequest) (*PreparedDataPlaneOpen, error) {
	if s.sshDial == nil {
		return nil, ErrSSHDialResolverUnavailable
	}
	host, port, err := s.sshDial.ResolveSSHDialTarget(ctx, req.Requester, req.ResourceID)
	if err != nil {
		return nil, err
	}

	connectionSettings, err := s.settingsSvc.GetConnectionSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection settings error: %w", err)
	}
	proxySettings := connectionSettings.Proxy
	if err := ensureProtocolAllowed(proxySettings, contract.SSHProtocol); err != nil {
		return nil, err
	}

	quota := sessionQuota{
		MaxGlobal:  proxySettings.MaxProxyConnections,
		MaxPerUser: proxySettings.MaxProxyConnectionsPerUser,
		ResourceID: req.ResourceID,
		Requester:  req.Requester,
	}
	if err := s.store.CheckQuota(quota); err != nil {
		return nil, err
	}

	now := time.Now()
	return &PreparedDataPlaneOpen{
		Session: ProxyConnection{
			ResourceID: req.ResourceID,
			Requester:  req.Requester,
			Status:     StatusActive,
			StartedAt:  now,
			Protocol:   contract.SSHProtocol,
			Host:       host,
			Port:       port,
			Username:   req.Requester,
		},
		quota: quota,
	}, nil
}

// RegisterDataPlaneSession records an active session after the target TCP dial succeeds.
// Uses the quota snapshot from PrepareDataPlaneOpen (no second settings read).
func (s *Service) RegisterDataPlaneSession(ctx context.Context, prepared *PreparedDataPlaneOpen, handle *ProxyConnectionLifecycle) (*ProxyConnection, error) {
	if prepared == nil || handle == nil {
		return nil, contract.NewInvalidParamError("session template is required")
	}

	template := &prepared.Session
	stored, replacedHandle, err := s.store.RegisterActive(prepared.quota, template, handle)
	if err != nil {
		tcp, ws := handle.Close()
		CloseDetachedSockets(tcp, ws)
		return nil, err
	}
	if replacedHandle != nil {
		tcp, ws := replacedHandle.Close()
		CloseDetachedSockets(tcp, ws)
	}

	if template.ConnectionID != 0 {
		s.touchConnectionAccess(ctx, template.ConnectionID, template.StartedAt)
	}

	s.logger.Info("Proxy session active",
		"connection", template.ConnectionID,
		"resource", template.ResourceID,
		"requester", template.Requester,
		"protocol", template.Protocol,
		"host", template.Host,
		"port", template.Port,
	)

	return cloneSession(&stored.session), nil
}

func (s *Service) touchConnectionAccess(ctx context.Context, connectionID uint, now time.Time) {
	s.touchMu.Lock()
	if s.lastTouchAt == nil {
		s.lastTouchAt = make(map[uint]time.Time)
	}
	for id, touched := range s.lastTouchAt {
		if now.Sub(touched) >= 60*time.Second {
			delete(s.lastTouchAt, id)
		}
	}
	if last, ok := s.lastTouchAt[connectionID]; ok && now.Sub(last) < 60*time.Second {
		s.touchMu.Unlock()
		return
	}
	s.lastTouchAt[connectionID] = now
	s.touchMu.Unlock()

	if err := s.connectionRepo.UpdateFields(ctx, connectionID, map[string]any{"last_accessed_at": now}); err != nil && s.logger != nil {
		s.logger.Warn("touch connection access time failed", "connection", connectionID, "error", err)
	}
}

func (s *Service) resolveAuthorizedConnectionTarget(ctx context.Context, requester string, connectionID uint) (contract.Protocol, string, uint, model.Connection, error) {
	conn, err := s.connectionRepo.GetByID(ctx, connectionID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return contract.ProtocolUnknown, "", 0, model.Connection{}, ErrConnectionNotFound
		}
		return contract.ProtocolUnknown, "", 0, model.Connection{}, fmt.Errorf("get connection error: %w", err)
	}
	if err := authorizeConnectionForProxy(requester, conn); err != nil {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, err
	}
	return s.resolveConnectionTargetFromConn(ctx, conn)
}

func (s *Service) resolveConnectionTargetFromConn(ctx context.Context, conn model.Connection) (contract.Protocol, string, uint, model.Connection, error) {
	if conn.Status == model.ConnectionPending {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, fmt.Errorf("%w: connection is still starting", ErrNotProxyable)
	}
	if conn.Status == model.ConnectionStopped {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, fmt.Errorf("%w: connection is stopped", ErrNotProxyable)
	}

	protocol := conn.Protocol
	if protocol == contract.SSHProtocol {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, ErrSSHUsesResourceProxy
	}
	if !protocol.Valid() {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewInvalidParamError("proxy protocol not defined")
	}
	if _, err := s.protocolRegistry.Get(protocol); err != nil {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewValidationError("proxy protocol " + protocol.String() + " is not supported")
	}

	if conn.NodeID == "" {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewInvalidParamError("proxy target host not defined")
	}
	if s.nodeRepo == nil {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewInvalidParamError("proxy target host not defined")
	}
	node, err := s.nodeRepo.GetByNodeID(ctx, conn.NodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewInvalidParamError("proxy target host not defined")
		}
		return contract.ProtocolUnknown, "", 0, model.Connection{}, fmt.Errorf("get target node: %w", err)
	}
	if node.Status == model.NodeStatusMaintenance {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, fmt.Errorf("%w: node %s", ErrNodeInMaintenance, node.NodeID)
	}
	if !node.AllowConnections {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, fmt.Errorf("%w: node %s", ErrNodeConnectionsDisallowed, node.NodeID)
	}
	targetHost := model.DialHostFor(node.DialHost, node.NodeID)
	if targetHost == "" {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewInvalidParamError("proxy target host not defined")
	}

	targetPort := conn.Port
	if targetPort <= 0 || targetPort > 65535 {
		return contract.ProtocolUnknown, "", 0, model.Connection{}, contract.NewValidationError(fmt.Sprintf("proxy target port is invalid: %d", targetPort))
	}

	return protocol, targetHost, targetPort, conn, nil
}

func authorizeConnectionForProxy(requester string, conn model.Connection) error {
	if requester == "" {
		return contract.NewInvalidParamError("requester is required")
	}
	if !canAccessConnection(requester, conn) {
		return ErrProxyConnectionForbidden
	}
	return nil
}

func canAccessConnection(username string, conn model.Connection) bool {
	return conn.Username == username
}

// CloseByID terminates exactly one ProxyConnection by identity (data-plane exit / error paths).
func (s *Service) CloseByID(sessionID uint) error {
	handle, err := s.store.RemoveByID(sessionID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return ErrProxyConnectionNotFound
		}
		return err
	}
	if handle != nil {
		tcp, ws := handle.Close()
		CloseDetachedSockets(tcp, ws)
	}
	return nil
}

// CloseAllByConnectionID terminates every ProxyConnection on the connection (admin delete/stop).
func (s *Service) CloseAllByConnectionID(connectionID uint) error {
	handles := s.store.RemoveAllByConnectionID(connectionID)
	for _, handle := range handles {
		tcp, ws := handle.Close()
		CloseDetachedSockets(tcp, ws)
	}
	return nil
}

// CloseAllByRequester terminates every ProxyConnection owned by the requester (logout / ban).
func (s *Service) CloseAllByRequester(requester string) error {
	handles := s.store.RemoveAllByRequester(requester)
	for _, handle := range handles {
		tcp, ws := handle.Close()
		CloseDetachedSockets(tcp, ws)
	}
	return nil
}

func (s *Service) GetProtocolHandler(protocol contract.Protocol) (ProtocolHandler, error) {
	return s.protocolRegistry.Get(protocol)
}

func (s *Service) GetActiveByConnectionIDAndRequester(connID uint, requester string) (*ProxyConnection, error) {
	session, err := s.store.GetByConnectionIDAndRequester(connID, requester)
	if errors.Is(err, contract.ErrNotFound) {
		return nil, ErrProxyConnectionNotFound
	}
	return session, err
}

func ensureProtocolAllowed(proxySettings schema.ProxySettings, protocol contract.Protocol) error {
	if !proxySettings.Enabled {
		return ErrProxyServiceDisabled
	}
	if !proxySettings.IsProtocolAllowed(protocol) {
		return fmt.Errorf("%w: %s", ErrProxyProtocolNotAllowed, protocol)
	}
	return nil
}
