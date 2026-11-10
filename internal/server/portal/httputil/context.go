package httputil

import (
	"net/http"
	"strings"

	"design-cloud-engine/internal/model"

	"github.com/labstack/echo/v5"
)

// CtxSessionKey stores the authenticated *model.Session on the Echo context.
// Session is the portal session (token, user, expiry). It is not a Connection or a ProxyConnection.
const CtxSessionKey = "session"

const (
	// PublicOverviewToken 专用于只读大屏页面的匿名鉴权（绕过强制登录和定期改密）
	PublicOverviewToken = "__dc_public_overview_guest_token__"
)

// ExtractBearerToken 从 HTTP 请求头 Authorization 中提取 Bearer Token。
// 支持不区分大小写的 "Bearer " 前缀，若未提供或格式不符则返回空字符串。
func ExtractBearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return auth[len(prefix):]
	}
	return ""
}

// CurrentUsername 从 Context 中获取当前登录用户名
func CurrentUsername(c *echo.Context) string {
	val := c.Get(CtxSessionKey)
	if val == nil {
		return ""
	}
	s, ok := val.(*model.Session)
	if !ok || s == nil {
		return ""
	}
	return s.Username
}

// CurrentToken 从 Context 中获取当前 Session 的 Token
func CurrentToken(c *echo.Context) string {
	val := c.Get(CtxSessionKey)
	if val == nil {
		return ""
	}
	s, ok := val.(*model.Session)
	if !ok || s == nil {
		return ""
	}
	return s.Token
}

// RequireSession extracts the current session from context.
// Returns 401 Unauthorized if no session is present, or if the stored value is not a *model.Session.
func RequireSession(c *echo.Context) (*model.Session, error) {
	val := c.Get(CtxSessionKey)
	if val == nil {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "no active session found")
	}
	s, ok := val.(*model.Session)
	if !ok || s == nil {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "session type mismatch")
	}
	return s, nil
}

// SessionOrForbidden returns the authenticated session from context, or a 403
// Forbidden error when no session is present or the stored value is not a
// *model.Session. Unlike RequireSession (401), it is used by middleware and
// handlers that expect a session to already exist, so a missing session is
// treated as forbidden rather than unauthenticated.
func SessionOrForbidden(c *echo.Context) (*model.Session, error) {
	val := c.Get(CtxSessionKey)
	if val == nil {
		return nil, echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
	}
	s, ok := val.(*model.Session)
	if !ok {
		return nil, echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
	}
	return s, nil
}
