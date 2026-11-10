package api

import (
	"context"
	"log/slog"
	"slices"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/middleware"

	"github.com/labstack/echo/v5"
)

type Handler struct {
	policies             map[middleware.APIPolicy][]echo.MiddlewareFunc
	overviewHandler      *overviewHandler
	sessionHandler       *sessionHandler
	userHandler          *userHandler
	appHandler           *appHandler
	nodeHandler          *nodeHandler
	settingsHandler      *settingsHandler
	licenseHandler       *licenseHandler
	schedulerHandler     *schedulerHandler
	connectionHandler    *connectionHandler
	fileHandler          *fileHandler
	ticketHandler        *ticketHandler
	auditHandler         *auditHandler
	connProxyHandler     *connProxyHandler
	systemHandler        *systemHandler
	storageHandler       *storageHandler
	banHandler           *banHandler
	versionHandler       *versionHandler
	desktopClientHandler *desktopClientHandler
	alertHandler         *alertHandler
	eventsHandler        *eventsHandler
}

// defaultRestrictedSessionAllowedPaths 定义密码已过期受限会话允许放行的 API 路由白名单。
// 仅允许用户查看当前个人资料、修改自身密码以及退出/查询当前会话。
var defaultRestrictedSessionAllowedPaths = []string{
	"/api/users/me/password",
	"/api/users/me",
	"/api/sessions/current",
	"/api/sessions",
}

// NewHandler assembles all API handlers from an explicit dependency set.
// Sub-handlers MUST receive narrow dependencies via factory functions.
// DO NOT pass Dependencies into individual business handlers.
func NewHandler(deps Dependencies, cfg *config.Config, logger *slog.Logger) *Handler {
	desktopClientHandler := newDesktopClientHandler(deps.Desktop)
	var isOverviewEnabled func(ctx context.Context) bool
	var allowNormalUser func(ctx context.Context) bool
	if deps.Settings != nil {
		isOverviewEnabled = deps.Settings.IsOverviewEnabled
		allowNormalUser = deps.Settings.IsNormalUserEnabled
	}
	return &Handler{
		policies: middleware.BuildRoutePolicies(deps.Session, deps.Security, middleware.PolicyConfig{
			EnableThreeRoles:              cfg.Admin.EnableThreeRoles,
			IsOverviewEnabled:             isOverviewEnabled,
			RestrictedSessionAllowedPaths: defaultRestrictedSessionAllowedPaths,
			RateLimit:                     cfg.Portal.RateLimit,
			RateBurst:                     cfg.Portal.RateBurst,
			TransferRateLimit:             cfg.Portal.TransferRateLimit,
			TransferRateBurst:             cfg.Portal.TransferRateBurst,
			WSRateLimit:                   cfg.Portal.WSRateLimit,
			WSRateBurst:                   cfg.Portal.WSRateBurst,
		}),
		overviewHandler:      newOverviewHandler(deps.Overview, allowNormalUser),
		sessionHandler:       newSessionHandler(deps.Session, deps.File, cfg),
		userHandler:          newUserHandler(deps.User, deps.Role, deps.Identity, cfg, logger),
		appHandler:           newAppHandler(deps.App),
		nodeHandler:          newNodeHandler(deps.Node, cfg),
		settingsHandler:      newSettingsHandler(deps.Settings),
		licenseHandler:       newLicenseHandler(deps.License),
		schedulerHandler:     newSchedulerHandler(deps.Scheduler),
		connectionHandler:    newConnectionHandler(deps.Connection, logger),
		fileHandler:          newFileHandler(deps.File),
		ticketHandler:        newTicketHandler(deps.Ticket, deps.User),
		auditHandler:         newAuditHandler(deps.Audit),
		connProxyHandler:     newConnProxyHandler(deps.Proxy, deps.Connection, logger, cfg.Portal.WebSocketHost),
		systemHandler:        newSystemHandler(deps.System),
		storageHandler:       newStorageHandler(deps.Storage, deps.Settings),
		banHandler:           newBanHandler(deps.Security, deps.Settings),
		desktopClientHandler: desktopClientHandler,
		versionHandler:       newVersionHandler(deps.Settings),
		alertHandler:         newAlertHandler(deps.Alert),
		eventsHandler:        &eventsHandler{hub: deps.Push, logger: logger},
	}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// InitRouter initializes all Portal routes.
func (h *Handler) InitRouter(e *echo.Echo) {
	apiGroup := e.Group("/api")

	// --- Non-Auth APIs
	guestPolicy := h.policies[middleware.PolicyGuest]
	h.publicAPI(apiGroup.Group("", guestPolicy...))

	// --- Authenticated APIs（常规用户域）
	userPolicy := h.policies[middleware.PolicyUser]
	h.authRequiredAPI(apiGroup.Group("", userPolicy...))

	// Overview API: /me 与 /cluster 快照走常规 PolicyUser 链（仅登录会话）；
	// /cluster/stream 走 PolicyOverviewClusterStream 链（登录会话 + 开启时的网页大屏访客 Token）。
	// 裸 GET /api/overview 不注册。
	h.overviewHandler.overviewUserAPI(apiGroup.Group("/overview", userPolicy...))
	clusterStreamPolicy := h.policies[middleware.PolicyOverviewClusterStream]
	h.overviewHandler.overviewClusterStreamAPI(apiGroup.Group("/overview/cluster/stream", clusterStreamPolicy...))

	// 文件传输域： PolicyFileTransfer 链（大文件禁用读超时 + 传输限流）
	fileTransferPolicy := h.policies[middleware.PolicyFileTransfer]
	h.fileHandler.fileTransferAPI(apiGroup.Group("/files", fileTransferPolicy...))

	// 协议代理连接：长连接 + 登录 Session + WS 握手限流（无 60s 请求超时）
	proxyConnectionPolicy := h.policies[middleware.PolicyProxyConnection]
	h.connProxyHandler.proxyConnectionAPI(apiGroup.Group("/connections", proxyConnectionPolicy...))

	// SSE 全局通知流：专用 PolicySSE 链（无 60s 请求超时）
	ssePolicy := h.policies[middleware.PolicySSE]
	h.eventsHandler.eventsStreamAPI(apiGroup.Group("/events", ssePolicy...))

	// --- Admin APIs
	auditAdminPolicy := h.policies[middleware.PolicyAuditAdmin]
	h.auditHandler.auditAdminAPI(apiGroup.Group("/admin/audit", auditAdminPolicy...))

	sysAdminPolicy := h.policies[middleware.PolicySysAdmin]
	h.systemAdminAPI(apiGroup.Group("/admin/system", sysAdminPolicy...))

	// Desktop 制品流式上传：使用 PolicySysAdminFileTransfer 禁用读超时、跳过 1 MiB BodyLimit 并走传输限流
	sysAdminTransferPolicy := h.policies[middleware.PolicySysAdminFileTransfer]
	h.desktopClientHandler.adminDesktopTransferAPI(
		apiGroup.Group("/admin/system/desktop", slices.Concat(sysAdminTransferPolicy, []echo.MiddlewareFunc{middleware.RequireScope(model.ScopeDesktop)})...),
	)

	securityAdminPolicy := h.policies[middleware.PolicySecurityAdmin]
	h.securityAdminAPI(apiGroup.Group("/admin/security", securityAdminPolicy...))
}

// ---------------------------------------------------------------------------
// Top-level route groups
// ---------------------------------------------------------------------------
// publicAPI registers all routes that do not require authentication.
func (h *Handler) publicAPI(g *echo.Group) {
	subRoutes := []struct {
		path   string
		handle func(g *echo.Group)
	}{
		{"/sessions", h.sessionHandler.loginAPI},
		{"/version", h.versionHandler.versionAPI},
		{"/desktop", h.desktopClientHandler.desktopAPI},
		{"/nodes", h.nodeHandler.nodeRegistrationAPI},
	}

	for _, subRoute := range subRoutes {
		subRoute.handle(g.Group(subRoute.path))
	}
}

// authRequiredAPI registers all routes that require regular user authentication.
func (h *Handler) authRequiredAPI(g *echo.Group) {
	subRoutes := []struct {
		path   string
		handle func(g *echo.Group)
	}{
		{"/sessions", h.sessionHandler.sessionsAPI},
		{"/apps", h.appHandler.appsAPI},
		{"/scheduler", h.schedulerHandler.schedulerAPI},
		{"/connections", h.connectionHandler.connectionsAPI},
		{"/users", h.userHandler.usersAPI},
		{"/licenses", h.licenseHandler.licensesAPI},
		{"/settings", h.settingsHandler.settingsAPI},
		{"/tickets", h.ticketHandler.ticketsAPI},
		{"/files", h.fileHandler.filesAPI},
	}

	for _, subRoute := range subRoutes {
		subRoute.handle(g.Group(subRoute.path))
	}
}

// route: /api/admin/security
func (h *Handler) securityAdminAPI(g *echo.Group) {
	bansScope := middleware.RequireScope(model.ScopeBans)
	sessionsScope := middleware.RequireScope(model.ScopeSessions)
	filesScope := middleware.RequireScope(model.ScopeFiles)
	securityRoleMW := middleware.RequireRoleOrSuperAdmin(model.RoleSecurityAdmin)

	subRoutes := []struct {
		path        string
		middlewares []echo.MiddlewareFunc
		handle      func(g *echo.Group)
	}{
		{"/files", []echo.MiddlewareFunc{filesScope}, h.fileHandler.adminFilesAPI},
		{"/bans", []echo.MiddlewareFunc{bansScope}, h.banHandler.adminBansAPI},
		{"/sessions", []echo.MiddlewareFunc{sessionsScope}, h.sessionHandler.adminSessionsAPI},
		{"/roles", []echo.MiddlewareFunc{securityRoleMW}, h.userHandler.adminRolesAPI},
		{"/scopes", []echo.MiddlewareFunc{securityRoleMW}, h.userHandler.adminScopesAPI},
		// 角色分配只在三员模式成立：单管理员模式没有"可分配角色"的概念（超管仅内置 dceadmin 可登录），
		// 因此这里用 RequireRole(SecurityAdmin) 直接拒绝 SuperAdmin 与非三员会话，
		// handler 内不再做模式判断。
		{"/users", []echo.MiddlewareFunc{middleware.RequireRole(model.RoleSecurityAdmin)},
			func(g *echo.Group) { g.PUT("/:user/role", h.userHandler.setUserRole) }},
	}
	for _, sr := range subRoutes {
		sr.handle(g.Group(sr.path, sr.middlewares...))
	}
}

// systemAdminAPI registers all /admin/system routes.
// g carries auth + system-admin role + scope check for all routes.
func (h *Handler) systemAdminAPI(g *echo.Group) {
	nodeScope := middleware.RequireScope(model.ScopeNodes)
	connectionsScope := middleware.RequireScope(model.ScopeConnections)
	schedulerScope := middleware.RequireScope(model.ScopeSchedulers)
	appsScope := middleware.RequireScope(model.ScopeApps)
	licensesScope := middleware.RequireScope(model.ScopeLicenses)
	usersScope := middleware.RequireScope(model.ScopeUsers)
	settingsScope := middleware.RequireScope(model.ScopeSettings)
	settingsAccessScope := middleware.RequireScope(allSettingsAccessScopes...)
	desktopScope := middleware.RequireScope(model.ScopeDesktop)
	ticketsScope := middleware.RequireScope(model.ScopeTickets)
	storageScope := middleware.RequireScope(model.ScopeStorages)
	statusScope := middleware.RequireScope(model.ScopeStatuses)

	subRoutes := []struct {
		path        string
		middlewares []echo.MiddlewareFunc
		handle      func(g *echo.Group)
	}{
		{"/nodes", []echo.MiddlewareFunc{nodeScope}, h.nodeHandler.adminNodesAPI},
		{"/connections", []echo.MiddlewareFunc{connectionsScope}, h.connectionHandler.adminConnectionsAPI},
		{"/scheduler", []echo.MiddlewareFunc{schedulerScope}, h.schedulerHandler.adminSchedulerAPI},
		{"/apps", []echo.MiddlewareFunc{appsScope}, h.appHandler.adminAppsAPI},
		{"/licenses", []echo.MiddlewareFunc{licensesScope}, h.licenseHandler.adminLicensesAPI},
		{"/users", []echo.MiddlewareFunc{usersScope}, h.userHandler.adminSystemUsersAPI},
		{"/groups", []echo.MiddlewareFunc{usersScope}, h.userHandler.adminGroupsAPI},
		{"/id-mappings", []echo.MiddlewareFunc{usersScope}, h.userHandler.adminIDMappingsAPI},
		{"/settings", []echo.MiddlewareFunc{settingsAccessScope}, h.settingsHandler.adminSettingsAPI},
		{"/desktop", []echo.MiddlewareFunc{desktopScope}, h.desktopClientHandler.adminDesktopAPI},
		{"/tickets", []echo.MiddlewareFunc{ticketsScope}, h.ticketHandler.adminTicketsAPI},
		{"/storage", []echo.MiddlewareFunc{storageScope}, h.storageHandler.storageAPI},
		{"/status", []echo.MiddlewareFunc{statusScope}, h.systemHandler.statusAPI},
		{"/alerts", []echo.MiddlewareFunc{settingsScope}, h.alertHandler.adminAlertsAPI},
	}
	for _, sr := range subRoutes {
		sr.handle(g.Group(sr.path, sr.middlewares...))
	}
}
