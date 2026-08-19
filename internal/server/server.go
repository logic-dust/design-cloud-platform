package server

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/host"
	"design-cloud-engine/internal/config"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/control"
	"design-cloud-engine/internal/server/control/leader"
	"design-cloud-engine/internal/server/portal"
	"design-cloud-engine/internal/server/service"
	nodeService "design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/push"

	"github.com/labstack/echo/v5"
)

// Server manages both Http and gRPC servers
type Server struct {
	ctx             context.Context
	portalServer    *echo.Echo
	portalLogCloser io.Closer // closes the access log writer on shutdown
	cfg             *config.Config
	controlServer   *control.Server
	services        *service.Container
	leaseRepo       *db.TaskLeaseRepo
	logger          *slog.Logger
	cancelHTTP      context.CancelFunc
	leader          *leader.Elector
	auditSvc        *audit.Service
}

// PrepareCrypto ensures the cluster CA and node certificate exist before NewServer.
// This is separated from NewServer so that construction-time IO does not interfere
// with assembly, and Server remains testable without on-disk certificates.
//
// Fail-fast: a reused CA (ErrCAExists) is the only tolerated error; incomplete CA
// material, unwritable directories or a failed node certificate issuance abort
// startup with a diagnosable error instead of degrading to a late TLS failure.
func PrepareCrypto(logger *slog.Logger, cfg *config.Config) error {
	nodeID := cfg.Runtime.NodeID

	// --- Ensure the cluster CA exists (generated on first start, reused afterwards).
	if _, err := nodeService.InitializeCA(cfg.ClusterCACertPath(), cfg.ClusterCAKeyPath(), false); err != nil {
		if !errors.Is(err, nodeService.ErrCAExists) {
			return fmt.Errorf("initialize cluster CA: %w", err)
		}
		logger.Debug("Cluster CA already exists, reusing")
	} else {
		logger.Info("Cluster CA generated automatically")
	}

	// --- Ensure the current node's gRPC certificate exists (issued by the CA if missing).
	return ensureNodeCert(logger, cfg, nodeID)
}

func NewServer(ctx context.Context, logger *slog.Logger, cfg *config.Config, store *db.Store) (*Server, error) {
	nodeID := cfg.Runtime.NodeID

	repos := db.NewRepos(store)
	leaderElector := leader.NewElector(nodeID, logger.With("component", "leader"), repos.TaskLease)

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  cfg.AuditLogPath(),
		SecurityLogPath: cfg.SecurityLogPath(),
		NodeID:          nodeID,
		Version:         config.VERSION,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init audit service: %w", err)
	}
	// Attach the execution repository so the audit API can list/get execution
	// records under /api/admin/audit/executions. Safe to do before NewContainer
	// because auditService is only used through its public methods.
	auditSvc.WithExecutionRepo(repos.Execution)
	defer func() {
		if err != nil {
			if closeErr := auditSvc.Close(); closeErr != nil {
				logger.Warn("Failed to close audit service during error cleanup", "error", closeErr)
			}
		}
	}()

	certs := control.Certs{
		CertPath: cfg.ClusterNodeCertPath(nodeID),
		KeyPath:  cfg.ClusterNodeKeyPath(nodeID),
		CAPath:   cfg.ClusterCACertPath(),
	}

	controlServer, err := control.NewControlPlane(
		cfg.Cluster,
		certs,
		nodeID,
		leaderElector,
		logger,
		repos,
	)
	if err != nil {
		return nil, err
	}

	services, err := service.NewContainer(
		controlServer.Executor(), controlServer, cfg, repos, logger, auditSvc,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init services: %w", err)
	}
	controlServer.SetSettingsProvider(services.Settings)
	controlServer.SetAlertEvaluator(services.Alert)
	controlServer.SetPublisher(services.Push)
	controlServer.SetControlEventHandler(services.Connection)
	services.Settings.SetOnUpdated(controlServer.BroadcastSettingsUpdate)
	services.Node.SetSettingsPusher(controlServer)

	// Configure cross-control push notification relaying
	services.Push.SetClusterForwarder(func(ctx context.Context, targetType string, userIDs []string, role model.Role, msg push.Message) {
		if len(cfg.Cluster.Servers) <= 1 {
			return
		}
		var payloadJSON string
		if msg.Payload != nil {
			if b, err := json.Marshal(msg.Payload); err == nil {
				payloadJSON = string(b)
			}
		}
		req := &pb.ForwardClusterEventRequest{
			SourceNodeId: cfg.Runtime.NodeID,
			TargetType:   targetType,
			UserIds:      userIDs,
			Role:         int32(role),
			EventName:    string(msg.Name),
			PayloadJson:  payloadJSON,
		}

		if leaderElector != nil && !leaderElector.IsLeader() {
			leaderID := leaderElector.LeaderID()
			if leaderID != "" && leaderID != cfg.Runtime.NodeID && controlServer.Forwarder() != nil {
				common.SafeGo(logger, "forward-cluster-event-standby", func() {
					fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = controlServer.Forwarder().ForwardClusterEvent(fCtx, leaderID, req)
				})
			}
			return
		}

		if controlServer.Forwarder() != nil {
			for _, srv := range cfg.Cluster.Servers {
				nodeIDs, err := host.ExpandHostPattern(srv)
				if err != nil {
					continue
				}
				for _, nid := range nodeIDs {
					if nid == cfg.Runtime.NodeID {
						continue
					}
					targetID := nid
					common.SafeGo(logger, "forward-cluster-event-leader", func() {
						fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						_ = controlServer.Forwarder().ForwardClusterEvent(fCtx, targetID, req)
					})
				}
			}
		}
	})

	portalServer, portalLogCloser, err := portal.CreateEchoServer(logger, cfg, services)
	if err != nil {
		return nil, fmt.Errorf("failed to create portal server: %w", err)
	}

	server := &Server{
		ctx:             ctx,
		portalServer:    portalServer,
		portalLogCloser: portalLogCloser,
		cfg:             cfg,
		controlServer:   controlServer,
		services:        services,
		leaseRepo:       repos.TaskLease,
		logger:          logger.With("component", "server"),
		leader:          leaderElector,
		auditSvc:        auditSvc,
	}
	// Certificate lifecycle engine: inspection, expiry alerts and CSR-based
	// auto-renewal (agent trigger + control node self-renewal with hot reload).
	controlServer.SetCertRenewalHooks(newCertRenewalAdapter(cfg, services, server))
	return server, nil
}

// Start starts both gRPC and Http servers
// gRPC starts synchronously (blocks until listening or error)
// Http starts asynchronously (errors sent to errCh)
func (sm *Server) Start(grpcPort string, errCh chan<- error) error {
	// started records cleanup functions for components that were successfully
	// started; on failure the deferred closure runs them in reverse order.
	var started []func()
	success := false
	defer func() {
		if success {
			return
		}
		for _, s := range slices.Backward(started) {
			s()
		}
	}()

	// --- 初始化并确保管理员账号一致性 (动态启停控制)
	if err := sm.services.User.InitAdminAccounts(sm.ctx, sm.cfg.Admin.EnableThreeRoles); err != nil {
		return fmt.Errorf("failed to init admin accounts: %w", err)
	}

	if err := sm.services.Proxy.Start(); err != nil {
		return fmt.Errorf("failed to start proxy service: %w", err)
	}
	started = append(started, func() {
		if err := sm.services.Proxy.Stop(); err != nil {
			sm.logger.Warn("Failed to stop Proxy after start failure", "error", err)
		}
	})

	// Start control gRPC server (synchronous listen check)
	if err := sm.controlServer.Start(":" + grpcPort); err != nil {
		return fmt.Errorf("failed to start control gRPC server: %w", err)
	}
	started = append(started, sm.controlServer.Stop)
	sm.logger.Info("Control gRPC Server started", "port", grpcPort)

	// Persist the control node's own certificate metadata (issuance report). Best
	// effort: a failure must never block startup, and the handshake self-heal
	// backfills subject/expiry on the built-in agent's first handshake.
	if err := sm.persistOwnCertMetadata(); err != nil {
		sm.logger.Warn("Failed to persist own certificate metadata", "error", err)
	}

	// Start Http Server (async)
	httpReady := make(chan struct{})
	httpStartErr := make(chan error, 1)
	go func() {
		addr := sm.cfg.Portal.Host + ":" + sm.cfg.Portal.Port
		sm.logger.Info("Starting Portal Server", "addr", addr, "tls_enabled", sm.cfg.Portal.TLS)
		httpCtx, cancel := context.WithCancel(sm.ctx)
		sm.cancelHTTP = cancel
		if err := portal.StartServer(httpCtx, sm.portalServer, sm.cfg.Portal, httpReady); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case httpStartErr <- err:
			default:
			}
			// Trigger Fail-Fast
			select {
			case errCh <- fmt.Errorf("http server failed: %w", err):
			default:
				sm.logger.Error("Failed to send http server error to errCh (buffer full)", "error", err)
			}
		}
	}()

	select {
	case <-httpReady:
		sm.logger.Info("Portal Server listener ready", "port", sm.cfg.Portal.Port, "addr", net.JoinHostPort(sm.cfg.Portal.Host, sm.cfg.Portal.Port), "tls", sm.cfg.Portal.TLS)
	case err := <-httpStartErr:
		return fmt.Errorf("failed to start portal server: %w", err)
	}

	if sm.leader != nil {
		sm.leader.Start(sm.ctx)
		started = append(started, sm.leader.Stop)
	}

	sm.services.Session.StartCleanupLoop(sm.ctx, sm.leaseRepo, sm.cfg.Runtime.NodeID)
	sm.services.User.StartSyncScheduler(sm.ctx, sm.logger.With("task", "user_sync"))
	if sm.services.Invalidator != nil {
		sm.services.Invalidator.Start(sm.ctx, len(sm.cfg.Cluster.Servers) <= 1)
	}

	// 统一启动所有已注册 TTL 缓存的后台过期清理
	cache.StartJanitors(cache.JanitorInterval)

	success = true
	return nil
}

// Stop gracefully shuts down both Http and gRPC servers
// Shutdown order: Http -> gRPC (reverse of startup)
func (sm *Server) Stop() {
	sm.logger.Info("Stopping server services...")

	// Stop Http Server (graceful shutdown via context cancellation, 10s default timeout)
	if sm.cancelHTTP != nil {
		sm.cancelHTTP()
	}

	if err := sm.services.Proxy.Stop(); err != nil {
		sm.logger.Warn("Failed to stop Proxy gracefully", "error", err)
	}

	// Flush in-memory auth failure records to DB before shutdown.
	if sm.services.BanCache != nil {
		sm.services.BanCache.Stop()
	}

	if sm.services.Session != nil {
		sm.services.Session.Close()
	}

	if sm.services.Invalidator != nil {
		sm.services.Invalidator.Stop()
	}

	// Stop control gRPC server
	sm.controlServer.Stop()

	if sm.leader != nil {
		sm.leader.Stop()
	}
	if sm.auditSvc != nil {
		if err := sm.auditSvc.Close(); err != nil {
			sm.logger.Warn("Failed to close audit service", "error", err)
		}
	}
	if sm.portalLogCloser != nil {
		if err := sm.portalLogCloser.Close(); err != nil {
			sm.logger.Warn("Failed to close portal access log writer", "error", err)
		}
	}

	sm.logger.Info("Server services stopped")
}

// ensureNodeCert auto-generates a node certificate if one does not exist at
// the configured paths. This runs unconditionally at server start — if the
// cert already exists the function is a no-op.
func ensureNodeCert(logger *slog.Logger, cfg *config.Config, nodeID string) error {
	certPath := cfg.ClusterNodeCertPath(nodeID)
	if _, err := os.Stat(certPath); err == nil {
		return nil // already exists
	}

	hosts := []string{nodeID, "localhost", "127.0.0.1"}
	if ips, err := localIPv4s(); err == nil {
		hosts = append(hosts, ips...)
	}

	if _, err := nodeService.IssueNodeCert(nodeService.CertOptions{
		CommonName: nodeID,
		Hosts:      hosts,
		ValidDays:  nodeService.ControlNodeCertValidDays,
		CACertFile: cfg.ClusterCACertPath(),
		CAKeyFile:  cfg.ClusterCAKeyPath(),
		CertFile:   cfg.ClusterNodeCertPath(nodeID),
		KeyFile:    cfg.ClusterNodeKeyPath(nodeID),
	}); err != nil {
		return fmt.Errorf("issue node certificate: %w", err)
	}
	logger.Info("Node certificate auto-generated", "node_id", nodeID)
	return nil
}

// persistOwnCertMetadata writes the local node certificate's fingerprint, subject
// and expiry to the nodes table right after the control server created/updated its
// own row (UpsertSelf). Best-effort by design: it must never block startup, and
// the handshake self-heal corrects the metadata once the built-in agent connects.
func (sm *Server) persistOwnCertMetadata() error {
	certPath := sm.cfg.ClusterNodeCertPath(sm.cfg.Runtime.NodeID)
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("read node certificate: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("invalid node certificate PEM at %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse node certificate: %w", err)
	}
	sum := sha256.Sum256(cert.Raw)
	fingerprint := hex.EncodeToString(sum[:])

	ctx, cancel := context.WithTimeout(sm.ctx, 5*time.Second)
	defer cancel()
	if _, err := sm.services.Node.UpdateCert(ctx, sm.cfg.Runtime.NodeID, fingerprint, cert.Subject.String(), cert.NotAfter); err != nil {
		return fmt.Errorf("update cert metadata: %w", err)
	}
	return nil
}

// renewOwnControlCert re-signs this control node's certificate in place, keeping
// .prev backups for rollback. The listener picks the new certificate up on the
// next handshake via GetCertificate hot reload (no restart).
func (sm *Server) renewOwnControlCert(_ context.Context) error {
	nodeID := sm.cfg.Runtime.NodeID
	certPath := sm.cfg.ClusterNodeCertPath(nodeID)
	keyPath := sm.cfg.ClusterNodeKeyPath(nodeID)

	if err := backupForRenewal(certPath); err != nil {
		return fmt.Errorf("backup current certificate: %w", err)
	}
	if err := backupForRenewal(keyPath); err != nil {
		return fmt.Errorf("backup current key: %w", err)
	}

	hosts := []string{nodeID, "localhost", "127.0.0.1"}
	if ips, err := localIPv4s(); err == nil {
		hosts = append(hosts, ips...)
	}
	if _, err := nodeService.IssueNodeCert(nodeService.CertOptions{
		CommonName: nodeID,
		Hosts:      hosts,
		ValidDays:  nodeService.ControlNodeCertValidDays,
		CACertFile: sm.cfg.ClusterCACertPath(),
		CAKeyFile:  sm.cfg.ClusterCAKeyPath(),
		CertFile:   certPath,
		KeyFile:    keyPath,
	}); err != nil {
		return fmt.Errorf("re-sign control node certificate: %w", err)
	}

	// Rotate the fingerprint in DB (prev=old, current=new) with the same
	// best-effort semantics as issuance reporting: a failure must not block the
	// new certificate, and the dual fingerprint keeps both authenticatable.
	if err := sm.persistOwnCertMetadata(); err != nil {
		sm.logger.Warn("Failed to persist renewed control certificate metadata", "error", err)
	}
	return nil
}

// backupForRenewal moves path to <path>.prev (replacing any older backup).
func backupForRenewal(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	prev := path + ".prev"
	_ = os.Remove(prev)
	return os.Rename(path, prev)
}

func localIPv4s() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var ips []string
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				ips = append(ips, ip4.String())
			}
		}
	}
	return ips, nil
}
