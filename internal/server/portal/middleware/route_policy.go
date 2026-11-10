package middleware

import (
	"context"
	"net/http"
	"slices"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/session"

	"github.com/labstack/echo/v5"
	echoMW "github.com/labstack/echo/v5/middleware"
)

const (
	DefaultJSONBodyLimitBytes    int64 = 1 << 20
	DefaultRequestContextTimeout       = 60 * time.Second
)

type APIPolicy int

const (
	PolicyGuest APIPolicy = iota
	PolicyUser
	PolicyOverviewClusterStream
	PolicyFileTransfer
	PolicyAuditAdmin
	PolicySysAdmin
	PolicySecurityAdmin
	PolicyProxyTunnel
	PolicySSE
)

// PolicyConfig 汇集构建路由策略链所需的全部标量配置（速率限制 + 管理员模式开关 + 受限会话白名单）。
// 作为 api → middleware 的 DTO，保持 middleware 与 config 包及具体路由路径解耦。
type PolicyConfig struct {
	EnableThreeRoles              bool                           // 三员管理员模式（false = 单管理员）
	IsOverviewEnabled             func(ctx context.Context) bool // 是否开启嵌入式数据大屏（动态判定函数）
	RestrictedSessionAllowedPaths []string                       // 密码需修改的受限会话允许放行的 API 路由白名单（由 api 层注入）
	RateLimit                     float64                        // 公共 API IP 级速率限制 (req/s)
	RateBurst                     int                            // 公共 API 突发上限
	TransferRateLimit             float64                        // 文件传输速率限制 (req/s)
	TransferRateBurst             int                            // 文件传输突发上限
	WSRateLimit                   float64                        // WebSocket 握手速率限制 (req/s)
	WSRateBurst                   int                            // WebSocket 握手突发上限
}

// BuildRoutePolicies constructs the middleware chains for all API scopes.
// All rate limits and bursts are validated at config load and passed in directly.
func BuildRoutePolicies(
	sm *session.Service,
	securityMgr SecurityManager,
	cfg PolicyConfig,
) map[APIPolicy][]echo.MiddlewareFunc {
	// --- 请求超时中间件：仅加入需要有限响应时间的策略链。
	requestTimeoutMW := echoMW.ContextTimeoutWithConfig(echoMW.ContextTimeoutConfig{
		Timeout: DefaultRequestContextTimeout,
	})

	// --- 请求 Body 大小限制中间件：适用于所有需要处理 JSON 请求体的 API，防止过大的请求导致资源耗尽
	bodyLimitMW := echoMW.BodyLimitWithConfig(echoMW.BodyLimitConfig{
		LimitBytes: DefaultJSONBodyLimitBytes,
	})

	// --- 公共 API 频率限制中间件：基于客户端 IP 地址进行限流（速率和突发容量均来自 config 校验）
	publicRateLimitMW := echoMW.RateLimiterWithConfig(echoMW.RateLimiterConfig{
		Store: echoMW.NewRateLimiterMemoryStoreWithConfig(
			echoMW.RateLimiterMemoryStoreConfig{Rate: cfg.RateLimit, Burst: cfg.RateBurst, ExpiresIn: 3 * time.Minute},
		),
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return c.RealIP(), nil
		},
	})

	// --- 认证用户频率限制中间件：直接使用 session 内置的 rate.Limiter性能高效
	userRateLimitMW := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			val := c.Get(httputil.CtxSessionKey)
			if val == nil {
				return next(c) // no session yet (shouldn't happen, but guard)
			}
			s, ok := val.(*model.Session)
			if !ok || s == nil {
				return next(c)
			}
			if s.RateLimiter != nil && !s.RateLimiter.Allow() {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusTooManyRequests, "too many requests"))
			}
			return next(c)
		}
	}

	// --- 文件传输频率限制中间件：基于认证用户进行限流，来自 config 校验
	fileTransferRateLimitMW := echoMW.RateLimiterWithConfig(echoMW.RateLimiterConfig{
		Store: echoMW.NewRateLimiterMemoryStoreWithConfig(
			echoMW.RateLimiterMemoryStoreConfig{Rate: cfg.TransferRateLimit, Burst: cfg.TransferRateBurst, ExpiresIn: 3 * time.Minute},
		),
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return httputil.CurrentUsername(c), nil // 确保已有 sessionAuthMW 提取了 Username
		},
	})

	// --- WebSocket 握手频率限制中间件：基于真实 IP 进行限流，来自 config 校验
	wsRateLimitMW := echoMW.RateLimiterWithConfig(echoMW.RateLimiterConfig{
		Store: echoMW.NewRateLimiterMemoryStoreWithConfig(
			echoMW.RateLimiterMemoryStoreConfig{Rate: cfg.WSRateLimit, Burst: cfg.WSRateBurst, ExpiresIn: 3 * time.Minute},
		),
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return c.RealIP(), nil // 纯 IP 兜底，免去无谓的 if username == "" 判断
		},
	})

	// --- 会话认证中间件：检查请求是否携带有效的 session token，并将 session 信息存入上下文
	sessionAuthMW := sessionAuth(sm, securityMgr, cfg.RestrictedSessionAllowedPaths...)
	// --- 管理域守卫中间件：根据管理员模式阻止不属于该模式的用户登录使用
	// （三员模式拒 SuperAdmin；单管理员模式拒三员角色）
	adminModeMW := requireSingleAdminGate()
	if cfg.EnableThreeRoles {
		adminModeMW = requireThreeRoleGate()
	}

	// --- 关闭 ReadTimeout 中间件：用于文件传输路由，避免读取大文件时触发超时
	disableReadTimeoutMW := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			rc := http.NewResponseController(c.Response())
			_ = rc.SetReadDeadline(time.Time{}) // zero value = disable deadline
			return next(c)
		}
	}

	// ---
	// 1. 基础构件 (Atomic Building Blocks)
	// - bodyLimitBase: [bodyLimitMW]
	// - publicBase: [requestTimeoutMW, bodyLimitMW]
	// - authBase:   [sessionAuthMW, adminModeMW]
	bodyLimitBase := []echo.MiddlewareFunc{bodyLimitMW}
	publicBase := slices.Concat([]echo.MiddlewareFunc{requestTimeoutMW}, bodyLimitBase)
	authBase := []echo.MiddlewareFunc{sessionAuthMW, adminModeMW}

	// 2. 派生基础链 (Derived Base Chains)
	// - publicWithRateLimit: IP 限流 + 公共基础（超时 + Body 限制）
	// - authenticatedChain:  公共基础 + 认证基础 + 用户级限流
	// - overviewClusterStreamChain: Body 限制 + 大屏实时流鉴权 + 用户级限流（无请求超时）
	// - fileTransferChain:   关闭读超时 + 认证基础 + 文件传输限流
	// - sseChain:            认证基础（无超时 + 无 Body 限制）
	publicWithRateLimit := slices.Concat([]echo.MiddlewareFunc{publicRateLimitMW}, publicBase)
	authenticatedChain := slices.Concat(publicBase, authBase, []echo.MiddlewareFunc{userRateLimitMW})
	// EventSource 长连接显式不加请求超时；不要依赖路由名后缀隐式豁免。
	overviewClusterStreamChain := slices.Concat(bodyLimitBase, []echo.MiddlewareFunc{
		overviewClusterStreamAuthMW(sm, securityMgr, cfg.IsOverviewEnabled),
		userRateLimitMW,
	})
	fileTransferChain := slices.Concat([]echo.MiddlewareFunc{disableReadTimeoutMW}, authBase, []echo.MiddlewareFunc{fileTransferRateLimitMW})
	proxyTunnelChain := slices.Concat([]echo.MiddlewareFunc{disableReadTimeoutMW}, authBase, []echo.MiddlewareFunc{wsRateLimitMW, userRateLimitMW})
	sseChain := authBase

	// 辅助函数：基于认证基础链添加特定的授权中间件
	extendAuthChain := func(mws ...echo.MiddlewareFunc) []echo.MiddlewareFunc {
		return slices.Concat(authenticatedChain, mws)
	}

	// 管理域中间件链 — 根据管理员模式选择不同的授权策略
	//
	// 单管理员模式: parent 组用 requireRoleOrAdminScope(role, scopes...)
	//               — Normal 用户可通过 scope 访问
	//               — 子路由用 RequireScope(bitmask) 做精细化检查
	//
	// 三员模式:     parent 组用 requireRoleOrAdminScope(role) 无 scopes
	//               — 只检查 role 匹配（scope 忽略）
	//               — 子路由 RequireScope 对 role 用户自动放行
	var (
		auditAdminChain    []echo.MiddlewareFunc
		sysAdminChain      []echo.MiddlewareFunc
		securityAdminChain []echo.MiddlewareFunc
	)
	if cfg.EnableThreeRoles {
		// 三员模式：父组只检查 role，不传 scopes
		auditAdminChain = extendAuthChain(RequireRole(model.RoleAuditAdmin), requireReadOnly())
		sysAdminChain = extendAuthChain(RequireRole(model.RoleSystemAdmin))
		securityAdminChain = extendAuthChain(RequireRole(model.RoleSecurityAdmin))
	} else {
		// 单管理员模式：父组检查 role + scope
		auditAdminChain = extendAuthChain(requireRoleOrAdminScope(model.RoleAuditAdmin))
		sysAdminChain = extendAuthChain(requireRoleOrAdminScope(
			model.RoleSystemAdmin,
			model.ScopeNodes, model.ScopeConnections, model.ScopeSchedulers,
			model.ScopeApps, model.ScopeLicenses,
			model.ScopeSettings, model.ScopeTickets,
			model.ScopeStorages, model.ScopeStatuses,
			model.ScopeUsers,
		))
		securityAdminChain = extendAuthChain(requireRoleOrAdminScope(
			model.RoleSecurityAdmin, model.ScopeBans, model.ScopeSessions, model.ScopeFiles,
		))
	}

	return map[APIPolicy][]echo.MiddlewareFunc{
		PolicyGuest:                 publicWithRateLimit,
		PolicyUser:                  authenticatedChain,
		PolicyOverviewClusterStream: overviewClusterStreamChain,
		PolicyFileTransfer:          fileTransferChain,
		PolicyAuditAdmin:            auditAdminChain,
		PolicySysAdmin:              sysAdminChain,
		PolicySecurityAdmin:         securityAdminChain,
		PolicyProxyTunnel:           proxyTunnelChain,
		PolicySSE:                   sseChain,
	}
}
