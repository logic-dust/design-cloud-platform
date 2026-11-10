package server

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/controlplane"
	"design-cloud-engine/internal/server/controlplane/leader"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"
	"design-cloud-engine/internal/server/portal"
	"design-cloud-engine/internal/server/portal/api"
	"design-cloud-engine/internal/server/push"
	nodesvc "design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/settings"

	"github.com/labstack/echo/v5"
)

// Server manages both Http and gRPC servers
type Server struct {
	ctx               context.Context
	portalServer      *echo.Echo
	portalLogCloser   io.Closer // closes the access log writer on shutdown
	cfg               *config.Config
	workerCtx         context.Context
	cancelWorkers     context.CancelFunc
	backgroundWorkers []<-chan struct{}
	controlServer     *controlplane.Server
	services          *assembly
	leaseRepo         *db.TaskLeaseRepo
	logger            *slog.Logger
	cancelHTTP        context.CancelFunc
	httpDone          chan struct{}
	stopJanitors      func()
	resourceCloseOnce sync.Once
	leader            *leader.Elector
	auditSvc          *audit.Service
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
	if _, err := nodesvc.InitializeCA(cfg.ClusterCACertPath(), cfg.ClusterCAKeyPath(), false); err != nil {
		if !errors.Is(err, nodesvc.ErrCAExists) {
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
	leaderElector := leader.NewElector(nodeID, logger.With("component", "leader"), repos.TaskLease,
		leader.WithLeaseTTL(time.Duration(cfg.Cluster.LeaseTTL)*time.Second),
		leader.WithRenewInterval(time.Duration(cfg.Cluster.RenewInterval)*time.Second))

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
	// records under /api/admin/audit/executions. Safe to do before newAssembly
	// because auditService is only used through its public methods.
	auditSvc.WithExecutionRepo(repos.Execution)
	defer func() {
		if err != nil {
			if closeErr := auditSvc.Close(); closeErr != nil {
				logger.Warn("Failed to close audit service during error cleanup", "error", closeErr)
			}
		}
	}()

	certs := controlplane.Certs{
		CertPath: cfg.ClusterNodeCertPath(nodeID),
		KeyPath:  cfg.ClusterNodeKeyPath(nodeID),
		CAPath:   cfg.ClusterCACertPath(),
	}

	// 步骤 1：先构造控制面运行时（注册表/派发器/转发客户端），它不依赖任何业务服务。
	controlRT, err := controlplane.NewRuntime(
		cfg.Cluster,
		certs,
		nodeID,
		leaderElector,
		logger,
		repos,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create control runtime: %w", err)
	}

	// 降级即自隔离：失去租约时由 Elector 回调控制面，主动断全部 Agent 流并失败在途派发。
	leaderElector.SetDemotionHook(controlRT.HandleLeaderDemotion)

	// 步骤 2：构造业务服务，只消费运行时能力（派发器 / 采集触发 / 配置推送）。
	services, err := newAssembly(
		controlRT, cfg, repos, logger, auditSvc,
		agentRegistryConnectionChecker{registry: controlRT.Registry()}, // node.ConnectionChecker
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init services: %w", err)
	}

	// 步骤 3：业务端口就绪后再构造入站 gRPC 适配，所有依赖在此时固定。
	controlServer, err := controlplane.NewServer(controlRT, controlplane.ServiceDeps{
		SettingsProvider:        services.Infra.Settings,
		AlertEvaluator:          services.Domain.Alert,
		Publisher:               services.Infra.Push,
		ConnectionReportHandler: services.Domain.Connection,
		SettingsPusher:          services.SettingsPusher,
		CertHooks:               newCertRenewalAdapter(cfg, services, logger),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create control server: %w", err)
	}
	subscribeAgentSettingsPush(services.Infra.Settings, services.SettingsPusher.Broadcast)
	subscribeDesktopSettingsRefresh(services.Infra.Settings, services.Infra.Push)

	// 集群事件转发由 newInfra 在构造 push.Hub 注入 controlplane.NewClusterEventForwarder，
	// 此处不再内联闭包，也不存在运行期 SetClusterForwarder。

	portalServer, portalLogCloser, err := portal.CreateEchoServer(logger, cfg, api.Dependencies{
		Settings: services.Infra.Settings,
		Audit:    services.Infra.Audit,
		Push:     services.Infra.Push,

		User:       services.Domain.User,
		Role:       services.Domain.Role,
		File:       services.Domain.File,
		Scheduler:  services.Domain.Scheduler,
		Node:       services.Domain.Node,
		Proxy:      services.Domain.Proxy,
		Session:    services.Domain.Session,
		Security:   services.Domain.Security,
		App:        services.Domain.App,
		License:    services.Domain.License,
		Connection: services.Domain.Connection,
		System:     services.Application.System,
		Storage:    services.Domain.Storage,
		Overview:   services.Application.Overview,
		Ticket:     services.Domain.Ticket,
		Desktop:    services.Domain.Desktop,
		Alert:      services.Domain.Alert,

		Identity: services.Application.Identity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create portal server: %w", err)
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	server := &Server{
		ctx:             ctx,
		workerCtx:       workerCtx,
		cancelWorkers:   cancelWorkers,
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
		if sm.cancelHTTP != nil {
			sm.cancelHTTP()
		}
		if sm.httpDone != nil {
			<-sm.httpDone
		}
		if sm.services != nil && sm.services.Domain.User != nil {
			sm.services.Domain.User.StopAsyncSyncs()
		}
		sm.stopBackgroundWorkers()
		for _, s := range slices.Backward(started) {
			s()
		}
		sm.closeLoggingResources()
	}()

	// --- 初始化并确保管理员账号一致性 (动态启停控制)
	if err := sm.services.Domain.User.InitAdminAccounts(sm.ctx, sm.cfg.Admin.EnableThreeRoles); err != nil {
		return fmt.Errorf("failed to init admin accounts: %w", err)
	}

	if err := sm.services.Domain.Proxy.Start(); err != nil {
		return fmt.Errorf("failed to start proxy service: %w", err)
	}
	started = append(started, func() {
		if err := sm.services.Domain.Proxy.Stop(); err != nil {
			sm.logger.Warn("Failed to stop Proxy after start failure", "error", err)
		}
	})

	// Start control gRPC server (synchronous listen check)
	controlErrors, err := sm.controlServer.Start(":" + grpcPort)
	if err != nil {
		return fmt.Errorf("failed to start control gRPC server: %w", err)
	}
	started = append(started, sm.controlServer.Stop)
	sm.logger.Info("Control gRPC Server started", "port", grpcPort)
	go func() {
		select {
		case serveErr, ok := <-controlErrors:
			if !ok || serveErr == nil {
				return
			}
			select {
			case errCh <- fmt.Errorf("control gRPC server failed: %w", serveErr):
			case <-sm.ctx.Done():
			default:
				sm.logger.Error("Failed to send control gRPC server error to errCh (buffer full)", "error", serveErr)
			}
		case <-sm.ctx.Done():
		}
	}()

	// Persist the control node's own certificate metadata (issuance report). Best
	// effort: a failure must never block startup, and the handshake self-heal
	// backfills subject/expiry on the built-in agent's first handshake.
	if err := persistOwnCertMetadata(sm.ctx, sm.cfg, sm.services); err != nil {
		sm.logger.Warn("Failed to persist own certificate metadata", "error", err)
	}

	// Start Http Server (async)
	httpReady := make(chan struct{})
	httpStartErr := make(chan error, 1)
	sm.httpDone = make(chan struct{})
	go func() {
		defer close(sm.httpDone)
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

	sm.backgroundWorkers = append(sm.backgroundWorkers,
		sm.services.Domain.Session.StartCleanupLoop(sm.workerCtx, sm.leaseRepo, sm.cfg.Runtime.NodeID),
		sm.services.Domain.Connection.StartStoppedPruneLoop(sm.workerCtx, sm.leaseRepo, sm.cfg.Runtime.NodeID),
		sm.services.Domain.User.StartSyncScheduler(sm.workerCtx, sm.logger.With("task", "user_sync")),
	)
	if sm.services.Infra.Invalidator != nil {
		sm.services.Infra.Invalidator.Start(sm.ctx, len(sm.cfg.Cluster.Servers) <= 1)
	}

	// 统一启动所有已注册 TTL 缓存的后台过期清理
	sm.stopJanitors = cache.StartJanitors(cache.JanitorInterval)

	success = true
	return nil
}

// Stop gracefully shuts down both Http and gRPC servers.
func (sm *Server) Stop() {
	sm.logger.Info("Stopping server services...")
	if sm.cancelWorkers != nil {
		sm.cancelWorkers()
	}

	// Stop Http Server (graceful shutdown via context cancellation, 10s default timeout)
	if sm.cancelHTTP != nil {
		sm.cancelHTTP()
	}
	if sm.httpDone != nil {
		<-sm.httpDone
	}
	if sm.services.Domain.User != nil {
		sm.services.Domain.User.StopAsyncSyncs()
	}

	if err := sm.services.Domain.Proxy.Stop(); err != nil {
		sm.logger.Warn("Failed to stop Proxy gracefully", "error", err)
	}

	// 先释放选主租约并触发降级钩子：此时 gRPC 仍可用，Agent 能收到重定向并立即迁移。
	if sm.leader != nil {
		sm.leader.Stop()
	}

	// Stop accepting and drain Agent RPCs before closing the services they call.
	sm.controlServer.Stop()
	sm.waitBackgroundWorkers()

	if sm.stopJanitors != nil {
		sm.stopJanitors()
	}
	if sm.services.Infra.Invalidator != nil {
		sm.services.Infra.Invalidator.Stop()
	}

	// Flush in-memory auth failure records to DB before shutdown.
	if sm.services.Infra.BanCache != nil {
		sm.services.Infra.BanCache.Stop()
	}

	if sm.services.Domain.Session != nil {
		sm.services.Domain.Session.Close()
	}

	sm.closeLoggingResources()

	sm.logger.Info("Server services stopped")
}

func (sm *Server) stopBackgroundWorkers() {
	if sm.cancelWorkers != nil {
		sm.cancelWorkers()
	}
	sm.waitBackgroundWorkers()
}

func (sm *Server) waitBackgroundWorkers() {
	for _, done := range sm.backgroundWorkers {
		if done != nil {
			<-done
		}
	}
	sm.backgroundWorkers = nil
}

func (sm *Server) closeLoggingResources() {
	sm.resourceCloseOnce.Do(func() {
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
	})
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

	res, err := nodesvc.IssueNodeCert(nodesvc.CertOptions{
		CommonName: nodeID,
		Hosts:      hosts,
		ValidDays:  nodesvc.ControlNodeCertValidDays,
		CACertFile: cfg.ClusterCACertPath(),
		CAKeyFile:  cfg.ClusterCAKeyPath(),
		CertFile:   cfg.ClusterNodeCertPath(nodeID),
		KeyFile:    cfg.ClusterNodeKeyPath(nodeID),
	})
	if err != nil {
		return fmt.Errorf("issue node certificate: %w", err)
	}
	nodesvc.CommitCertPair(res.CertPath, res.KeyPath)
	logger.Info("Node certificate auto-generated", "node_id", nodeID)
	return nil
}

// persistOwnCertMetadata writes the local node certificate's fingerprint, subject
// and expiry to the nodes table right after the control server created/updated its
// own row (UpsertSelf). Best-effort by design: it must never block startup, and
// the handshake self-heal corrects the metadata once the built-in agent connects.
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

// agentRegistryConnectionChecker adapts the control-plane agent registry to
// node.Service's NodeConnectionChecker (maintenance exit uses live connections).
type agentRegistryConnectionChecker struct {
	registry *agentregistry.Registry
}

func (c agentRegistryConnectionChecker) IsConnected(nodeID string) bool {
	if c.registry == nil {
		return false
	}
	return c.registry.IsConnected(nodeID)
}

// certRenewalAdapter implements controlplane.CertRenewalHooks, bridging the control
// plane's certificate lifecycle engine to the settings policy, the node service
// (CSR signing + metadata persistence + audit) and this server's own cert paths.
type certRenewalAdapter struct {
	cfg      *config.Config
	services *assembly
	logger   *slog.Logger
}

func newCertRenewalAdapter(cfg *config.Config, services *assembly, logger *slog.Logger) *certRenewalAdapter {
	return &certRenewalAdapter{cfg: cfg, services: services, logger: logger}
}

// persistOwnCertMetadata 把本机证书指纹写入 DB（prev=old，current=new）。
// 启动期与续期后共用同一实现，避免两处指纹口径漂移。
func persistOwnCertMetadata(ctx context.Context, cfg *config.Config, services *assembly) error {
	certPath := cfg.ClusterNodeCertPath(cfg.Runtime.NodeID)
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

	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := services.Domain.Node.UpdateCert(dbCtx, cfg.Runtime.NodeID, fingerprint, cert.Subject.String(), cert.NotAfter); err != nil {
		return fmt.Errorf("update cert metadata: %w", err)
	}
	return nil
}

// renewOwnControlCert re-signs this control node's certificate in place, keeping
// .prev backups for rollback. The listener picks the new certificate up on the
// next handshake via GetCertificate hot reload (no restart).
func (a *certRenewalAdapter) renewOwnControlCert(ctx context.Context) error {
	nodeID := a.cfg.Runtime.NodeID
	certPath := a.cfg.ClusterNodeCertPath(nodeID)
	keyPath := a.cfg.ClusterNodeKeyPath(nodeID)

	// IssueNodeCert publishes cert+key as a pair and keeps <path>.prev for
	// rollback if the second write (or the DB binding step) fails. Snapshots are
	// removed after the binding is committed.
	hosts := []string{nodeID, "localhost", "127.0.0.1"}
	if ips, err := localIPv4s(); err == nil {
		hosts = append(hosts, ips...)
	}
	res, err := nodesvc.IssueNodeCert(nodesvc.CertOptions{
		CommonName: nodeID,
		Hosts:      hosts,
		ValidDays:  nodesvc.ControlNodeCertValidDays,
		CACertFile: a.cfg.ClusterCACertPath(),
		CAKeyFile:  a.cfg.ClusterCAKeyPath(),
		CertFile:   certPath,
		KeyFile:    keyPath,
	})
	if err != nil {
		return fmt.Errorf("re-sign control node certificate: %w", err)
	}

	// Rotate the fingerprint in DB (prev=old, current=new). If the DB step fails,
	// restore the previous pair so this control node keeps a cert/DB-consistent
	// identity and the renewal loop can retry on the next tick.
	if err := persistOwnCertMetadata(ctx, a.cfg, a.services); err != nil {
		if rbErr := nodesvc.RollbackCertPair(res.CertPath, res.KeyPath); rbErr != nil {
			a.logger.Error("Failed to roll back control certificate after DB failure", "error", rbErr)
		}
		a.logger.Warn("Failed to persist renewed control certificate metadata; restored previous pair", "error", err)
		return nil
	}
	nodesvc.CommitCertPair(res.CertPath, res.KeyPath)
	return nil
}

func (a *certRenewalAdapter) Policy(ctx context.Context) (controlplane.CertPolicy, error) {
	settings, err := a.services.Infra.Settings.GetCertSettings(ctx)
	if err != nil {
		return controlplane.CertPolicy{}, err
	}
	return controlplane.CertPolicy{
		AutoRenewMode:   settings.AutoRenewMode,
		RenewBeforeDays: settings.RenewBeforeDays,
		MaxAttempts:     settings.MaxAttempts,
		BackoffHours:    settings.BackoffScheduleHours,
	}, nil
}

func (a *certRenewalAdapter) SignCSR(csrPEM []byte, commonName string, hosts []string, validDays int) ([]byte, string, string, time.Time, error) {
	certPEM, result, err := nodesvc.SignNodeCSR(nodesvc.CSRSignOptions{
		CSRPEM:     csrPEM,
		CommonName: commonName,
		Hosts:      hosts,
		ValidDays:  validDays,
		CACertFile: a.cfg.ClusterCACertPath(),
		CAKeyFile:  a.cfg.ClusterCAKeyPath(),
	})
	if err != nil {
		return nil, "", "", time.Time{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		return nil, "", "", time.Time{}, fmt.Errorf("parse certificate expiry: %w", err)
	}
	return certPEM, result.Fingerprint, result.Subject, expiresAt, nil
}

func (a *certRenewalAdapter) CAPEM() ([]byte, error) {
	return os.ReadFile(a.cfg.ClusterCACertPath())
}

func (a *certRenewalAdapter) CAKeyPEM() ([]byte, error) {
	return os.ReadFile(a.cfg.ClusterCAKeyPath())
}

func (a *certRenewalAdapter) UpdateCert(ctx context.Context, nodeID, fingerprint, subject string, expiresAt time.Time) error {
	_, err := a.services.Domain.Node.UpdateCert(ctx, nodeID, fingerprint, subject, expiresAt)
	return err
}

func (a *certRenewalAdapter) LogRenew(ctx context.Context, nodeID, phase, message string) {
	a.services.Domain.Node.LogCertRenew(ctx, nodeID, phase, message)
}

func (a *certRenewalAdapter) RenewControlNodeCert(ctx context.Context) error {
	return a.renewOwnControlCert(ctx)
}

// agentSettingsPushDomains lists global settings domains that affect pb.AgentSettings.
var agentSettingsPushDomains = []schema.Domain{
	schema.DomainAgent,
	schema.DomainStorage,
	schema.DomainLicense,
	schema.DomainScheduler,
}

func agentSettingsDomainsChanged(domains []schema.Domain) bool {
	for _, d := range domains {
		if slices.Contains(agentSettingsPushDomains, d) {
			return true
		}
	}
	return false
}

// subscribeAgentSettingsPush pushes effective agent settings only when a domain
// that contributes to GetAgentSettingsForNode / UpdateSettingsCommand changes.
func subscribeAgentSettingsPush(svc *settings.Service, broadcast func(ctx context.Context) error) {
	if svc == nil || broadcast == nil {
		return
	}
	svc.SubscribeDomains(func(ctx context.Context, domains []schema.Domain) error {
		if !agentSettingsDomainsChanged(domains) {
			return nil
		}
		return broadcast(ctx)
	})
}

// subscribeDesktopSettingsRefresh notifies Portal clients when the desktop domain changes.
func subscribeDesktopSettingsRefresh(svc *settings.Service, pub push.Publisher) {
	if svc == nil || pub == nil {
		return
	}
	svc.SubscribeDomains(func(ctx context.Context, domains []schema.Domain) error {
		if !slices.Contains(domains, schema.DomainDesktop) {
			return nil
		}
		pub.Broadcast(ctx, desktopSettingsRefreshMessage())
		return nil
	})
}

func desktopSettingsRefreshMessage() push.Message {
	return push.Message{
		Name: push.EventSystem,
		Payload: push.EventPayload{
			Action: "refresh",
			Extra:  map[string]string{"domain": schema.DomainDesktop.String()},
		},
	}
}
