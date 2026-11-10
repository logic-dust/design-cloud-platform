package middleware

import (
	"cmp"
	"context"
	"net/http"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/security"

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

// sessionLookup is the subset of session.Service used by the auth middleware.
type sessionLookup interface {
	GetByToken(ctx context.Context, token string) (*model.Session, error)
}

// checkIPBanned 检查客户端 IP 是否已被封禁。
func checkIPBanned(c *echo.Context, securityMgr SecurityManager, ip string) error {
	if banned, record := securityMgr.IsBannedByIP(c.Request().Context(), ip); banned {
		return echo.NewHTTPError(http.StatusForbidden, record.Reason)
	}
	return nil
}

// authenticateUserSession 验证 Token 对应 Session 的存在性与有效期，并在验证成功后注入 Context。
func authenticateUserSession(c *echo.Context, sm sessionLookup, securityMgr SecurityManager, ip, token string) (*model.Session, error) {
	if token == "" {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, errMissingBearerToken)
	}

	userSession, err := sm.GetByToken(c.Request().Context(), token)
	if err != nil {
		if banErr := checkIPBanned(c, securityMgr, ip); banErr != nil {
			return nil, banErr
		}
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
//
// 检查顺序：先验证会话，再考虑 IP 封禁——
//  1. 有效会话直接放行，不受 IP 封禁影响（避免共享出口 IP 误伤已登录用户）；
//  2. 无凭据或无效凭据且 IP 已封禁 → 403，不累计失败；
//  3. 无效凭据且未封禁 → 计入失败并返回 401。
func sessionAuth(sm sessionLookup, securityMgr SecurityManager, allowedPaths ...string) echo.MiddlewareFunc {
	allowedMap := make(map[string]struct{}, len(allowedPaths))
	for _, p := range allowedPaths {
		allowedMap[p] = struct{}{}
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ip := c.RealIP()
			token := httputil.ExtractBearerToken(c.Request())
			if token == "" {
				if err := checkIPBanned(c, securityMgr, ip); err != nil {
					return httputil.RespondError(c, err)
				}
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusUnauthorized, errMissingBearerToken))
			}

			userSession, err := authenticateUserSession(c, sm, securityMgr, ip, token)
			if err != nil {
				return httputil.RespondError(c, err)
			}

			// 密码需修改的 session 仅放行注入的受限白名单路径
			if userSession.PasswordMustChange && len(allowedMap) > 0 {
				if _, allowed := allowedMap[c.Path()]; !allowed {
					return httputil.RespondError(c, echo.NewHTTPError(http.StatusPreconditionRequired, errPasswordChangeRequired))
				}
			}

			return next(c)
		}
	}
}

// overviewClusterStreamAuthMW 为集群概览实时流 GET /api/overview/cluster/stream 鉴权（网页大屏唯一使用的数据接口）：
// 1. Token 优先取 Authorization 请求头，无请求头时取 ?token=（浏览器原生 EventSource 无法设置请求头）；
// 2. 无凭据或携带 PublicOverviewToken 时先检查 IP 封禁；
// 3. PublicOverviewToken：大屏开关关闭返回 404，开启则无状态放行；
// 4. 其余 Token 按用户会话校验：无效且已封禁 → 403；无效未封禁 → 401；有效会话跳过 IP 封禁检查。
func overviewClusterStreamAuthMW(sm sessionLookup, securityMgr SecurityManager, isOverviewEnabled func(ctx context.Context) bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ip := c.RealIP()
			token := cmp.Or(httputil.ExtractBearerToken(c.Request()), c.QueryParam("token"))

			// 免登访问（无凭据或 Guest Token）仍受 IP 封禁约束；有效用户会话则跳过封禁检查
			if token == "" || token == httputil.PublicOverviewToken {
				if err := checkIPBanned(c, securityMgr, ip); err != nil {
					return httputil.RespondError(c, err)
				}
			}

			// 大屏 Guest Token：开关关闭返回 404；开启则无状态直接放行（无需构造 Session）
			if token == httputil.PublicOverviewToken && isOverviewEnabled != nil && !isOverviewEnabled(c.Request().Context()) {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusNotFound, "overview guest access is disabled"))
			}
			if token == httputil.PublicOverviewToken {
				return next(c)
			}

			if _, err := authenticateUserSession(c, sm, securityMgr, ip, token); err != nil {
				return httputil.RespondError(c, err)
			}

			return next(c)
		}
	}
}
