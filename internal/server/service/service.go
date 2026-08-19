// Package service provides the service container for the HTTP server subsystem.
// The container manages dependencies and provides a unified access point for all services.
package service

import (
	"context"
	"log/slog"
	"path/filepath"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/alert"
	"design-cloud-engine/internal/server/service/app"
	"design-cloud-engine/internal/server/service/connection"
	"design-cloud-engine/internal/server/service/dashboard"
	"design-cloud-engine/internal/server/service/desktop"
	"design-cloud-engine/internal/server/service/file"
	"design-cloud-engine/internal/server/service/license"
	"design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/proxy"
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/role"
	"design-cloud-engine/internal/server/service/scheduler"
	securitysvc "design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/session"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/storage"
	"design-cloud-engine/internal/server/service/system"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"
	"design-cloud-engine/internal/server/service/user"
)

// Container holds all service dependencies.
type Container struct {
	User       *user.Service
	Role       *role.Service
	File       *file.Service
	Scheduler  *scheduler.Service
	Node       *node.Service
	Proxy      *proxy.Service
	Session    *session.Service
	Security   *securitysvc.Service
	Settings   *settings.Service
	App        *app.Service
	License    *license.Service
	Connection *connection.Service
	System     *system.Service
	Storage    *storage.Service
	Dashboard  *dashboard.Service
	Audit      *audit.Service
	Ticket     *ticketsvc.Service
	Desktop    *desktop.Service
	Push       *push.Hub
	Alert      *alert.Evaluator

	// --- Infrastructure bridges (not for business use) ---
	// These fields are exposed so that Server can manage their lifecycle
	// (Start/Stop). They are NOT intended for use by business handlers.
	Invalidator *cache.Invalidator    // lifecycle: started/stopped by Server
	BanCache    *securitysvc.BanCache // lifecycle: started/stopped by Server

	Dispatcher execution.Executor
}

// NewContainer initializes all services and returns a populated Container.
// repos must be created once by the caller (typically server.NewServer) and
// passed in; do NOT construct db.Repos inside this function.
func NewContainer(dispatcher execution.Executor, collectTrigger contract.CollectTrigger, cfg *config.Config, repos *db.Repos, log *slog.Logger, auditSvc *audit.Service, connOpts ...connection.Option) (*Container, error) {
	r := &Container{
		Dispatcher:  dispatcher,
		Audit:       auditSvc,
		Invalidator: cache.NewInvalidator(repos.CacheVersion, log),
	}

	if err := r.initComponents(collectTrigger, cfg, repos, log, auditSvc, connOpts); err != nil {
		return nil, err
	}

	if err := r.initSession(cfg, repos, log, auditSvc); err != nil {
		return nil, err
	}
	r.System = system.NewService(repos.Health, r.Node, r.User, cfg)

	return r, nil
}

func (r *Container) initComponents(collectTrigger contract.CollectTrigger, cfg *config.Config, repos *db.Repos, log *slog.Logger, auditSvc *audit.Service, connOpts []connection.Option) error {
	var err error

	// SSE 事件 Hub：先创建，供后续所有服务注入 publisher
	r.Push = push.NewHub()

	r.Settings, err = settings.NewService(repos.Setting, auditSvc, r.Invalidator)
	if err != nil {
		return err
	}
	r.Settings.SetNodeRepo(repos.Node)

	// 集中式告警引擎：阈值来自 DomainAlert 配置，推送走 SSE hub，告警历史存库（最多 1000 条）
	r.Alert = alert.NewEvaluator(r.Push, r.Settings, log.With("component", "alert"))
	if repos.AlertHistory != nil {
		r.Alert.SetHistoryRepo(repos.AlertHistory)
	}

	r.Storage = storage.NewService(repos.Storage, collectTrigger)

	r.User, err = user.NewService(r.Dispatcher, user.Config{
		AuthMode:         cfg.User.AuthMode,
		SyncLeaseOwnerID: cfg.Runtime.NodeID,
		LDAP:             cfg.User.Ldap,
		EnableThreeRoles: cfg.Admin.EnableThreeRoles,
	}, r.Settings, repos.User, repos.TaskLease, repos.CacheVersion, r.Invalidator, log.With("component", "user"), auditSvc)
	if err != nil {
		return err
	}

	// --- role service (three-role mode role management)
	r.Role = role.NewService(repos.User, auditSvc)

	r.Scheduler, err = scheduler.NewService(r.Dispatcher, r.Settings, scheduler.Config{
		Name:      cfg.Scheduler.Type,
		EnvScript: cfg.Scheduler.EnvScript,
	}, log.With("component", "scheduler"), r.Push)
	if err != nil {
		return err
	}

	r.Dashboard = dashboard.NewService(log.With("component", "dashboard"), repos.Node, repos.Storage, r.Settings, r.Scheduler)

	r.Desktop = desktop.NewService("", r.Settings, log.With("component", "desktop"))

	localNodeID := cfg.Runtime.NodeID
	r.File = file.NewService(nil, r.User, r.Settings, file.Config{
		GetLauncherPath: func(ctx context.Context) string {
			agentSettings, err := r.Settings.GetEffectiveAgentSettingsForNode(ctx, localNodeID)
			if err != nil {
				return ""
			}
			return agentSettings.LauncherPath
		},
		GetDesktopReleaseDir: func(ctx context.Context) string {
			return r.Desktop.GetBaseDir()
		},
	}, repos.File, log.With("component", "file"))

	r.License = license.NewService(r.Dispatcher, repos.License, r.Settings, log.With("component", "license"), license.WithNodeRepo(repos.Node))
	connOpts = append([]connection.Option{
		connection.WithCollectTrigger(collectTrigger),
		connection.WithLease(repos.TaskLease, cfg.Runtime.NodeID),
	}, connOpts...)
	r.Connection, err = connection.NewService(r.Dispatcher, r.User, repos.Connection, log.With("component", "connection"), append(connOpts, connection.WithPublisher(r.Push), connection.WithSettingsSvc(r.Settings), connection.WithNodeRepo(repos.Node))...)
	if err != nil {
		return err
	}
	r.Node = node.NewService(repos.Node, log.With("component", "node"), node.Config{
		NodeID:  cfg.Runtime.NodeID,
		CertDir: filepath.Join(cfg.Runtime.DataDir, "certs", "cluster"),
	}, auditSvc, r.Push, collectTrigger, r.Settings)
	r.App = app.NewService(repos.App, repos.User, r.Scheduler, log.With("component", "app"))

	r.Proxy, err = proxy.NewService(r.Settings, repos.Connection, log.With("component", "proxy"))
	if err != nil {
		return err
	}
	r.Connection.SetTunnelCloser(r.Proxy)

	r.Ticket = ticketsvc.NewService(repos.Ticket, repos.TicketProject, repos.User, auditSvc, log, r.Push)
	return nil
}

func (r *Container) initSession(cfg *config.Config, repos *db.Repos, log *slog.Logger, auditSvc *audit.Service) error {
	var err error
	r.BanCache, err = securitysvc.NewBanCache(repos.Ban, log.With("component", "auth_failure"), r.Invalidator)
	if err != nil {
		return err
	}
	r.Security, err = securitysvc.NewService(r.Settings, r.BanCache, log.With("component", "security"))
	if err != nil {
		return err
	}
	// authSettingsCache 是 settings 的派生缓存，需随 settings 变更联动失效。
	// 与 settings.Service 自身的 Clear 回调共存于同一主题（Invalidator 支持多回调）。
	if r.Invalidator != nil {
		r.Invalidator.Register("settings", func() {
			r.Security.InvalidateAuthSettingsCache()
		})
	}

	ldapAuth := cfg.User.AuthMode == config.UserAuthModeLDAP
	authSvc, err := session.NewAuthManager(repos.User, r.User, r.Security, log.With("component", "session_auth"), auditSvc, ldapAuth)
	if err != nil {
		return err
	}

	r.Session, err = session.NewService(log.With("component", "session"), authSvc, r.User, r.Security, r.Settings, repos.Session, r.Invalidator, r.Push)
	if err != nil {
		return err
	}

	r.Session.SetProxyPruner(r.Proxy.PruneExpiredPending)
	r.User.SetSessionInvalidator(r.Session)
	r.Role.SetSessionInvalidator(r.Session)
	return nil
}
