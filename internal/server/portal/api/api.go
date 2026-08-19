package api

import (
	"context"
	"log/slog"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/middleware"
	"design-cloud-engine/internal/server/service"

	"github.com/labstack/echo/v5"
)

type Handler struct {
	policies          map[middleware.APIPolicy][]echo.MiddlewareFunc
	dashboard         *dashboardHandler
	sessionHandler    *sessionHandler
	userHandler       *userHandler
	appHandler        *appHandler
	nodeHandler       *nodeHandler
	settingsHandler   *settingsHandler
	licenseHandler    *licenseHandler
	schedulerHandler  *schedulerHandler
	connectionHandler *connectionHandler
	fileHandler       *fileHandler
	ticketHandler     *ticketHandler
	auditHandler      *auditHandler
	connProxyHandler  *connProxyHandler
	systemHandler     *systemHandler
	storageHandler    *storageHandler
	banHandler        *banHandler
	versionHandler    *versionHandler
	alertHandler      *alertHandler
	eventsHandler     *eventsHandler
}

// defaultRestrictedSessionAllowedPaths 定义密码已过期受限会话允许放行的 API 路由白名单。
// 仅允许用户查看当前个人资料、修改自身密码以及退出/查询当前会话。
var defaultRestrictedSessionAllowedPaths = []string{
	"/api/users/me/password",
	"/api/users/me",
	"/api/sessions/current",
	"/api/sessions",
}

// NewHandler receives the full service container ONLY for top-level assembly.
// Sub-handlers MUST receive narrow dependencies via factory functions.
// DO NOT pass *service.Container into individual business handlers.
func NewHandler(svc *service.Container, cfg *config.Config, logger *slog.Logger) *Handler {
	var isDashboardEnabled func(ctx context.Context) bool
	if svc.Settings != nil {
		isDashboardEnabled = svc.Settings.IsDashboardEnabled
	}
	return &Handler{
		policies: middleware.BuildRoutePolicies(svc.Session, svc.Security, middleware.PolicyConfig{
			EnableThreeRoles:              cfg.Admin.EnableThreeRoles,
			IsDashboardEnabled:            isDashboardEnabled,
			RestrictedSessionAllowedPaths: defaultRestrictedSessionAllowedPaths,
			RateLimit:                     cfg.Portal.RateLimit,
			RateBurst:                     cfg.Portal.RateBurst,
			TransferRateLimit:             cfg.Portal.TransferRateLimit,
			TransferRateBurst:             cfg.Portal.TransferRateBurst,
			WSRateLimit:                   cfg.Portal.WSRateLimit,
			WSRateBurst:                   cfg.Portal.WSRateBurst,
		}),
		dashboard:         newDashboardHandler(svc.Dashboard, isDashboardEnabled),
		sessionHandler:    newSessionHandler(svc, cfg),
		userHandler:       newUserHandler(svc, cfg, logger),
		appHandler:        newAppHandler(svc.App),
		nodeHandler:       newNodeHandler(svc.Node, cfg),
		settingsHandler:   newSettingsHandler(svc),
		licenseHandler:    newLicenseHandler(svc),
		schedulerHandler:  newSchedulerHandler(svc),
		connectionHandler: newConnectionHandler(svc, logger),
		fileHandler:       newFileHandler(svc),
		ticketHandler:     newTicketHandler(svc.Ticket, svc.User),
		auditHandler:      newAuditHandler(svc.Audit),
		connProxyHandler:  newConnProxyHandler(svc, logger, cfg.Portal.WebSocketHost),
		systemHandler:     newSystemHandler(svc),
		storageHandler:    newStorageHandler(svc),
		banHandler:        newBanHandler(svc.Security),
		versionHandler:    newVersionHandler(svc.Desktop),
		alertHandler:      newAlertHandler(svc.Alert),
		eventsHandler:     &eventsHandler{hub: svc.Push, logger: logger},
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

	// Dashboard API: PolicyDashboard 链（支持登录用户及开启时的免登大屏）
	dashboardPolicy := h.policies[middleware.PolicyDashboard]
	h.dashboard.dashboardAPI(apiGroup.Group("/dashboard", dashboardPolicy...))

	// 文件传输域： PolicyFileTransfer 链（大文件禁用读超时 + 传输限流）
	fileTransferPolicy := h.policies[middleware.PolicyFileTransfer]
	h.fileHandler.fileTransferAPI(apiGroup.Group("/files", fileTransferPolicy...))

	// WebSocket 代理域： PolicyWebSocket 链（OTP 鉴权，无 session cookie）
	proxyWsPolicy := h.policies[middleware.PolicyWebSocket]
	h.connProxyHandler.proxyAccessAPI(apiGroup.Group("/connection-proxy", proxyWsPolicy...))

	// SSE 全局通知流：专用 PolicySSE 链（无 60s 请求超时）
	ssePolicy := h.policies[middleware.PolicySSE]
	h.eventsHandler.eventsStreamAPI(apiGroup.Group("/events", ssePolicy...))

	// --- Admin APIs
	auditAdminPolicy := h.policies[middleware.PolicyAuditAdmin]
	h.auditHandler.auditAdminAPI(apiGroup.Group("/admin/audit", auditAdminPolicy...))

	sysAdminPolicy := h.policies[middleware.PolicySysAdmin]
	h.systemAdminAPI(apiGroup.Group("/admin/system", sysAdminPolicy...))

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
		{"/connections", h.connProxyHandler.proxySessionAPI},
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
		{"/users", []echo.MiddlewareFunc{securityRoleMW},
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
		{"/settings", []echo.MiddlewareFunc{settingsScope}, h.settingsHandler.adminSettingsAPI},
		{"/tickets", []echo.MiddlewareFunc{ticketsScope}, h.ticketHandler.adminTicketsAPI},
		{"/storage", []echo.MiddlewareFunc{storageScope}, h.storageHandler.storageAPI},
		{"/status", []echo.MiddlewareFunc{statusScope}, h.systemHandler.statusAPI},
		{"/alerts", []echo.MiddlewareFunc{settingsScope}, h.alertHandler.adminAlertsAPI},
	}
	for _, sr := range subRoutes {
		sr.handle(g.Group(sr.path, sr.middlewares...))
	}
}
