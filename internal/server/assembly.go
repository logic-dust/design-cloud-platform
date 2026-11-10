package server

// 本文件是 Server 子系统的组合根：依次装配 Infra（基础设施）、Domain（业务服务）
// 与 Application（跨域用例和读模型）。
//
// 它是全仓唯一允许知道「具体类型如何互相喂」的地方。业务包只声明自己需要的
// 依赖，由这里注入；任何业务包都不得反向 import 本包（编译期由导入环强制）。

import (
	"context"
	"log/slog"
	"path/filepath"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/application/identity"
	"design-cloud-engine/internal/server/application/overview"
	"design-cloud-engine/internal/server/application/system"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/controlplane"
	"design-cloud-engine/internal/server/push"
	"design-cloud-engine/internal/server/service/alert"
	"design-cloud-engine/internal/server/service/app"
	"design-cloud-engine/internal/server/service/connection"
	"design-cloud-engine/internal/server/service/desktop"
	"design-cloud-engine/internal/server/service/file"
	"design-cloud-engine/internal/server/service/license"
	"design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/proxy"
	"design-cloud-engine/internal/server/service/role"
	"design-cloud-engine/internal/server/service/scheduler"
	securitysvc "design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/session"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/storage"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"
	"design-cloud-engine/internal/server/service/user"
)

// infra holds infrastructure dependencies: repos / invalidator / push.Hub /
// audit / dispatcher / settings / banCache. Lifecycle (Start/Stop) is managed
// by the server assembly layer, NOT by business handlers.
type infra struct {
	Repos       *db.Repos
	Dispatcher  execution.Executor
	Audit       *audit.Service
	Invalidator *cache.Invalidator    // lifecycle: started/stopped by Server
	BanCache    *securitysvc.BanCache // lifecycle: started/stopped by Server
	Push        *push.Hub
	Settings    *settings.Service
}

// domain holds the business services, assembled once by newDomain and read-only afterwards.
type domain struct {
	User       *user.Service
	Role       *role.Service
	File       *file.Service
	Scheduler  *scheduler.Service
	Node       *node.Service
	Proxy      *proxy.Service
	Session    *session.Service
	Security   *securitysvc.Service
	App        *app.Service
	License    *license.Service
	Connection *connection.Service
	Storage    *storage.Service
	Ticket     *ticketsvc.Service
	Desktop    *desktop.Service
	Alert      *alert.Evaluator
}

// applicationSet groups cross-service use cases and read models. These are
// assembled after the business services because they coordinate or aggregate them.
type applicationSet struct {
	Identity *identity.Service
	Overview *overview.Service
	System   *system.Service
}

// assembly is the Server subsystem's composition root: infrastructure,
// business services, and application use cases/read models.
//
// Unexported on purpose: outside this package nothing may depend on the shape
// of the object graph. Consumers receive what they need (portal takes
// api.Dependencies), never the graph itself.
type assembly struct {
	Infra       *infra
	Domain      *domain
	Application *applicationSet
	// SettingsPusher 是配置下发的写侧，node 服务与 controlplane.Server 共用同一实例。
	SettingsPusher *controlplane.SettingsPusher
}

// newAssembly initializes all services via three-phase assembly.
// repos must be created once by the caller (NewServer) and passed in; do NOT
// construct db.Repos inside this function.
//
// rt 是已构造好的集群控制运行时：service 只从它取派发器、采集触发与配置推送
// 能力，不依赖控制面的入站 gRPC 适配（controlplane.Server）。这是 control↔service
// 构造环被消除的关键——装配顺序为 Runtime → service → Server。
//
// connChecker 可选 — nil 表示维护模式无法检查实时连接。
func newAssembly(rt *controlplane.Runtime, cfg *config.Config, repos *db.Repos, log *slog.Logger, auditSvc *audit.Service, connChecker node.ConnectionChecker) (*assembly, error) {
	inf, err := newInfra(rt, cfg, repos, log, auditSvc)
	if err != nil {
		return nil, err
	}
	// 配置推送的写侧由 Runtime + settings 读能力组成，因此在 settings 就绪后即可
	// 构造，供 node 服务注入——无需等待 controlplane.Server。
	settingsPusher := controlplane.NewSettingsPusher(rt, inf.Settings)
	dom, err := newDomain(inf, rt, cfg, log, settingsPusher, connChecker)
	if err != nil {
		return nil, err
	}
	appSet := newApplication(inf, dom, cfg, log)
	// 应用用例与读模型在业务服务之后装配，依赖全部已就绪。
	return &assembly{
		Infra:          inf,
		Domain:         dom,
		Application:    appSet,
		SettingsPusher: settingsPusher,
	}, nil
}

func newApplication(inf *infra, dom *domain, cfg *config.Config, log *slog.Logger) *applicationSet {
	return &applicationSet{
		Identity: identity.New(dom.User, dom.Role, dom.Connection, dom.Session, log.With("component", "identity")),
		Overview: overview.NewService(log.With("component", "overview"), inf.Repos.Node, inf.Repos.Storage, inf.Settings, dom.Scheduler),
		System:   system.NewService(inf.Repos.Health, dom.Node, dom.User, inf.Settings, cfg),
	}
}

// newInfra constructs infrastructure in fixed order (each step only depends on
// previously constructed items):
//  1. repos / dispatcher / audit —— 调用方传入
//  2. cache.Invalidator          —— repos.CacheVersion
//  3. push.Hub                   —— 无依赖，供后续所有服务注入 publisher
//  4. settings.Service           —— repos + audit + Invalidator（L0 配置中心）
//  5. security.BanCache          —— repos.Ban + Invalidator
func newInfra(rt *controlplane.Runtime, cfg *config.Config, repos *db.Repos, log *slog.Logger, auditSvc *audit.Service) (*infra, error) {
	// Push Hub 在构造时就拿到集群转发回调：Leader 广播、Standby 单播、单机短路。
	// 回调实现留在 controlplane，Hub 本身不 import 控制面。
	hub := push.NewHub(push.WithClusterForwarder(controlplane.NewClusterEventForwarder(
		controlplane.ClusterEventForwarderConfig{
			NodeID:         cfg.Runtime.NodeID,
			Members:        cfg.Runtime.Servers,
			ServerPatterns: cfg.Cluster.Servers,
		},
		rt.Leader(),
		rt.Forwarder(),
		log.With("component", "push_forward"),
	)))

	i := &infra{
		Repos:       repos,
		Dispatcher:  rt.Executor(),
		Audit:       auditSvc,
		Invalidator: cache.NewInvalidator(repos.CacheVersion, log),
		Push:        hub,
	}

	var err error
	// Settings：核心配置服务，需要 cacheVersionRepo + nodeRepo 来支持
	// InvalidateCache() 和 node-specific feature flags。
	if i.Settings, err = settings.NewService(repos.Setting, log, auditSvc, i.Invalidator, repos.CacheVersion, repos.Node); err != nil {
		return nil, err
	}

	if i.BanCache, err = securitysvc.NewBanCache(repos.Ban, log.With("component", "auth_failure"), i.Invalidator); err != nil {
		return nil, err
	}
	return i, nil
}

// newDomain constructs the 16 business services in fixed order (each step only
// depends on previously constructed items):
//  1. proxy      —— settings + repos
//  2. session    —— session.Evictor 共享的跨域失效组件（只依赖 repos/cache/proxy，
//     不依赖 user，从而切断 user↔session 构造环）
//  3. user       —— settings + repos + dispatcher + Evictor（密码/权限/同步失效）
//  4. security   —— settings + BanCache + WithUserBanHook(→proxy)
//  5. session    —— user + security + settings + repos + Evictor + proxyRevoker(→proxy)
//  6. role       —— repos.User + audit
//  7. connection —— user + settings + repos + WithProxyConnectionCloser(→proxy)
//  8. node       —— repos + settings + push + settingsPusher + connChecker
//  9. storage    —— repos + collectTrigger
//
// 10. license    —— repos + settings + nodeRepo
// 11. scheduler  —— dispatcher + settings + push
// 12. app        —— repos + scheduler
// 13. desktop    —— settings（提前于 file：file 的 GetDesktopReleaseDir 闭包引用它）
// 14. file       —— user + settings + desktop
// 15. ticket     —— repos + audit + push + WithAttachmentResolver(→file)
// 16. alert      —— push + settings + repos.AlertHistory
func newDomain(inf *infra, collectTrigger contract.CollectTrigger, cfg *config.Config, log *slog.Logger, settingsPusher contract.NodeSettingsPusher, connChecker node.ConnectionChecker) (*domain, error) {
	d := &domain{}
	repos := inf.Repos
	var err error

	// 1. Proxy 必须在 Connection/Security/Session 之前构造（三者经闭包/Option 引用它）
	if d.Proxy, err = proxy.NewService(inf.Settings, repos.Connection, repos.Node, log.With("component", "proxy")); err != nil {
		return nil, err
	}

	// 2. Session Evictor：user 与 session 共享的跨域失效实现（删除行 + 清缓存 + 断代理连接）。
	// 它不依赖 *user.Service，因此 user 可以先于 session 构造。
	sessionEvictor, err := session.NewEvictor(log.With("component", "session_evictor"), repos.Session, inf.Invalidator, func(requester string) {
		_ = d.Proxy.CloseAllByRequester(requester)
	})
	if err != nil {
		return nil, err
	}

	// 3. User：密码/权限/LDAP 同步路径直接使用共享 Evictor，无需构造后回填
	if d.User, err = user.NewService(inf.Dispatcher, user.Config{
		AuthMode:         cfg.User.AuthMode,
		SyncLeaseOwnerID: cfg.Runtime.NodeID,
		LDAP:             cfg.User.Ldap,
		EnableThreeRoles: cfg.Admin.EnableThreeRoles,
	}, inf.Settings, repos.User, repos.TaskLease, repos.CacheVersion, inf.Invalidator, log.With("component", "user"), inf.Audit, sessionEvictor); err != nil {
		return nil, err
	}

	// 4. Security：构造期注入封禁断连 hook（唯一触发路径，禁止双触发）
	if d.Security, err = securitysvc.NewService(inf.Settings, inf.BanCache, log.With("component", "security"),
		securitysvc.WithUserBanHook(func(username string) {
			_ = d.Proxy.CloseAllByRequester(username)
		})); err != nil {
		return nil, err
	}
	// 5. Session：user + security + settings + repos + Evictor（session→proxy 单向，无循环）
	ldapAuth := cfg.User.AuthMode == config.UserAuthModeLDAP
	authSvc, err := session.NewAuthManager(repos.User, d.User, d.Security, log.With("component", "session_auth"), inf.Audit, ldapAuth)
	if err != nil {
		return nil, err
	}
	if d.Session, err = session.NewService(log.With("component", "session"), authSvc, d.User, d.Security, inf.Settings, repos.Session, sessionEvictor, inf.Push); err != nil {
		return nil, err
	}

	// 6. Role（three-role mode role management）
	d.Role = role.NewService(repos.User, inf.Audit)

	// 7. Connection
	connOpts := []connection.Option{
		connection.WithCollectTrigger(collectTrigger),
		connection.WithLease(repos.TaskLease, cfg.Runtime.NodeID),
		connection.WithResourceRepo(repos.ConnectionResource),
		connection.WithProxyConnectionCloser(d.Proxy),
		connection.WithPublisher(inf.Push),
		connection.WithSettingsSvc(inf.Settings),
		connection.WithNodeRepo(repos.Node),
	}
	if d.Connection, err = connection.NewService(inf.Dispatcher, d.User, repos.Connection, log.With("component", "connection"), connOpts...); err != nil {
		return nil, err
	}
	d.Proxy.SetSSHDialResolver(d.Connection)

	// 8. Node
	d.Node = node.NewService(repos.Node, log.With("component", "node"), node.Config{
		NodeID:  cfg.Runtime.NodeID,
		CertDir: filepath.Join(cfg.Runtime.DataDir, "certs", "cluster"),
	}, inf.Audit, inf.Push, collectTrigger, inf.Settings, settingsPusher, connChecker)

	// 9. Storage
	d.Storage = storage.NewService(repos.Storage, collectTrigger)

	// 10. License
	d.License = license.NewService(inf.Dispatcher, repos.License, inf.Settings, log.With("component", "license"), license.WithNodeRepo(repos.Node))

	// 11. Scheduler
	if d.Scheduler, err = scheduler.NewService(inf.Dispatcher, inf.Settings, log.With("component", "scheduler"), inf.Push); err != nil {
		return nil, err
	}

	// 12. App
	d.App = app.NewService(repos.App, repos.User, d.Scheduler, log.With("component", "app"))

	// 13. Desktop（提前于 file，见顺序注释）
	d.Desktop = desktop.NewService("", inf.Settings, log.With("component", "desktop"))

	// 14. File
	localNodeID := cfg.Runtime.NodeID
	d.File = file.NewService(d.User, inf.Settings, file.Config{
		GetExecBinPath: func(ctx context.Context) string {
			agentSettings, err := inf.Settings.GetEffectiveAgentSettingsForNode(ctx, localNodeID)
			if err != nil {
				return ""
			}
			return agentSettings.ExecBinPath
		},
		GetDesktopReleaseDir: func(ctx context.Context) string {
			return d.Desktop.GetBaseDir()
		},
	}, repos.DownloadRequest, log.With("component", "file"))

	// 15. Ticket
	d.Ticket = ticketsvc.NewService(repos.Ticket, repos.TicketProject, repos.User, inf.Audit, log, inf.Push,
		ticketsvc.WithAttachmentResolver(func(ctx context.Context, location, relPath, username string) error {
			_, err := d.File.ResolveLocationPath(ctx, location, relPath, username)
			return err
		}))

	// 16. 集中式告警引擎：阈值来自 DomainAlert 配置，推送走 SSE hub，告警历史存库（最多 1000 条）
	d.Alert = alert.NewEvaluator(inf.Push, inf.Settings, repos.AlertHistory, log.With("component", "alert"))

	return d, nil
}
