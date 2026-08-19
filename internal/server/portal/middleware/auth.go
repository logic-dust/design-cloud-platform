package middleware

import (
	"cmp"
	"context"
	"net/http"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/session"

	"github.com/labstack/echo/v5"
)

const (
	errInvalidSessionToken    = "invalid session token"
	errSessionExpired         = "session expired"
	errMissingBearerToken     = "missing bearer token, format should be 'Bearer {token}' in Authorization header"
	errPasswordChangeRequired = "password change required"
)

// SecurityManager defines the subset of security.Manager needed by the auth middleware.
type SecurityManager interface {
	IsBannedByIP(ctx context.Context, IP string) (bool, security.BannedRecord)
	RecordIPFailure(ctx context.Context, IP string)
}

// checkIPBanned 检查客户端 IP 是否已被封禁。
func checkIPBanned(c *echo.Context, securityMgr SecurityManager, ip string) error {
	if banned, record := securityMgr.IsBannedByIP(c.Request().Context(), ip); banned {
		return echo.NewHTTPError(http.StatusForbidden, record.Reason)
	}
	return nil
}

// authenticateUserSession 验证 Token 对应 Session 的存在性与有效期，并在验证成功后注入 Context。
func authenticateUserSession(c *echo.Context, sm *session.Service, securityMgr SecurityManager, ip, token string) (*model.Session, error) {
	if token == "" {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, errMissingBearerToken)
	}

	userSession, err := sm.GetByToken(c.Request().Context(), token)
	if err != nil {
		go func(bannedIP string) {
			defer func() {
				_ = recover()
			}()
			securityMgr.RecordIPFailure(context.Background(), bannedIP)
		}(ip)
		return nil, echo.NewHTTPError(http.StatusUnauthorized, errInvalidSessionToken)
	}

	if time.Now().After(userSession.ExpiresAt) {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, errSessionExpired)
	}

	c.Set(httputil.CtxSessionKey, userSession)
	return userSession, nil
}

// sessionAuth 验证 Bearer Token 并维护 Session 状态。
// allowedPaths 可选接收当 Session 被标记为 PasswordMustChange 时允许放行的受限白名单路径。
func sessionAuth(sm *session.Service, securityMgr SecurityManager, allowedPaths ...string) echo.MiddlewareFunc {
	allowedMap := make(map[string]struct{}, len(allowedPaths))
	for _, p := range allowedPaths {
		allowedMap[p] = struct{}{}
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ip := c.RealIP()
			if err := checkIPBanned(c, securityMgr, ip); err != nil {
				return httputil.RespondError(c, err)
			}

			token := httputil.ExtractBearerToken(c.Request())
			userSession, err := authenticateUserSession(c, sm, securityMgr, ip, token)
			if err != nil {
				return httputil.RespondError(c, err)
			}

			// 密码需修改的 session 仅放行注入的受限白名单路径
			if userSession.PasswordMustChange && len(allowedMap) > 0 {
				if _, allowed := allowedMap[c.Path()]; !allowed {
					return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, errPasswordChangeRequired))
				}
			}

			return next(c)
		}
	}
}

// dashboardAuthMW 专门为 Dashboard 端点提供灵活鉴权：
// 1. 若携带 PublicDashboardToken（支持 Header 或 Query 参数，以适配浏览器原生 EventSource）且 isDashboardEnabled 动态判断为 true，作为免登只读请求直接放行（无状态直通）；
// 2. 若请求携带有效的用户 Bearer Token，以登录用户身份放行；
// 3. 若大屏被关闭或凭据无效，返回 401 Unauthorized / 403 Forbidden。
func dashboardAuthMW(sm *session.Service, securityMgr SecurityManager, isDashboardEnabled func(ctx context.Context) bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ip := c.RealIP()
			if err := checkIPBanned(c, securityMgr, ip); err != nil {
				return httputil.RespondError(c, err)
			}

			// 优先从 Header 提取 Bearer Token，无 Header 时从 Query 参数提取（适配浏览器 EventSource 免登场景）
			token := cmp.Or(httputil.ExtractBearerToken(c.Request()), c.QueryParam("token"))
			// 大屏 Guest Token 匹配验证（纯无状态直接放行，无需构造 Session）
			if token == httputil.PublicDashboardToken {
				if isDashboardEnabled != nil && !isDashboardEnabled(c.Request().Context()) {
					return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "dashboard guest access is disabled"))
				}
				return next(c)
			}

			if _, err := authenticateUserSession(c, sm, securityMgr, ip, token); err != nil {
				return httputil.RespondError(c, err)
			}

			return next(c)
		}
	}
}
