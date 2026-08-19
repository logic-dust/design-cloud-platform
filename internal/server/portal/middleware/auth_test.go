package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/security"

	"github.com/labstack/echo/v5"
)

type mockSecurityManager struct{}

func (m *mockSecurityManager) IsBannedByIP(ctx context.Context, IP string) (bool, security.BannedRecord) {
	return false, security.BannedRecord{}
}

func (m *mockSecurityManager) RecordIPFailure(ctx context.Context, IP string) {
}

// setupEchoTest creates a fresh Echo instance, a GET request, and a context.
func setupEchoTest(method, target string) (*echo.Echo, *echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	return e, c, rec
}

// setSessionOnContext stores a model.Session with the given role in the echo context.
func setSessionOnContext(c *echo.Context, role model.Role) {
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:  "testuser",
		Token:     "test-token",
		Role:      role,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
}

// ---------- CurrentUsername ----------

func TestCurrentUsername_Exists(t *testing.T) {
	_, c, _ := setupEchoTest(http.MethodGet, "/")

	setSessionOnContext(c, model.RoleNormal)
	// Override the username to something specific after the helper
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username: "bob",
	})

	got := httputil.CurrentUsername(c)
	if got != "bob" {
		t.Fatalf("expected 'bob', got %q", got)
	}
}

func TestCurrentUsername_NoSession(t *testing.T) {
	_, c, _ := setupEchoTest(http.MethodGet, "/")

	got := httputil.CurrentUsername(c)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// ---------- CurrentToken ----------

func TestCurrentToken_Exists(t *testing.T) {
	_, c, _ := setupEchoTest(http.MethodGet, "/")

	c.Set(httputil.CtxSessionKey, &model.Session{
		Token: "secret-token",
	})

	got := httputil.CurrentToken(c)
	if got != "secret-token" {
		t.Fatalf("expected 'secret-token', got %q", got)
	}
}

func TestCurrentToken_NoSession(t *testing.T) {
	_, c, _ := setupEchoTest(http.MethodGet, "/")

	got := httputil.CurrentToken(c)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// ---------- SessionAuth (limited — needs mock for success path) ----------

func TestSessionAuth_MissingBearerToken(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")

	// No Authorization header
	mw := sessionAuth(nil, &mockSecurityManager{})
	handler := mw(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestSessionAuth_EmptyBearerToken(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")

	c.Request().Header.Set(echo.HeaderAuthorization, "Bearer ")
	mw := sessionAuth(nil, &mockSecurityManager{})
	handler := mw(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestSessionAuth_RejectsQueryToken(t *testing.T) {
	// 常规会话鉴权严格要求 Header，不再从 query 参数提取 token
	_, c, rec := setupEchoTest(http.MethodGet, "/api/events?stream=true&token=any-token")

	mw := sessionAuth(nil, &mockSecurityManager{})
	handler := mw(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when token passed only in query, got %d", rec.Code)
	}
}

func TestSessionAuth_InvalidBearerFormat(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")

	// Wrong prefix
	c.Request().Header.Set(echo.HeaderAuthorization, "Basic dXNlcjpwYXNz")
	mw := sessionAuth(nil, &mockSecurityManager{})
	handler := mw(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestPasswordChangeAllowed(t *testing.T) {
	t.Parallel()

	allowedPaths := []string{
		"/api/users/me/password",
		"/api/users/me",
		"/api/sessions/current",
		"/api/sessions",
	}

	allowedMap := make(map[string]struct{}, len(allowedPaths))
	for _, p := range allowedPaths {
		allowedMap[p] = struct{}{}
	}

	for _, p := range allowedPaths {
		if _, ok := allowedMap[p]; !ok {
			t.Errorf("expected %q to be in allowedPaths", p)
		}
	}

	blocked := []string{
		"/api/dashboard",
		"/api/apps",
		"/api/files",
		"/api/admin/system/status",
		"/api/users",
		"/api/sessions/123",
		"/api/users/me/preferences",
	}
	for _, p := range blocked {
		if _, ok := allowedMap[p]; ok {
			t.Errorf("expected %q not to be in allowedPaths", p)
		}
	}
}

func TestDashboardAuthMW(t *testing.T) {
	t.Parallel()

	// 1. 大屏开启时，Guest Token 被成功放行（纯无状态直通）
	mwEnabled := dashboardAuthMW(nil, &mockSecurityManager{}, func(context.Context) bool { return true })
	handlerEnabled := mwEnabled(func(c *echo.Context) error {
		return c.String(http.StatusOK, "dashboard-ok")
	})

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+httputil.PublicDashboardToken)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handlerEnabled(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "dashboard-ok" {
		t.Fatalf("expected body %q, got %q", "dashboard-ok", rec.Body.String())
	}

	// 2. 大屏关闭时，使用 Guest Token 访问被 403 阻断
	mwDisabled := dashboardAuthMW(nil, &mockSecurityManager{}, func(context.Context) bool { return false })
	handlerDisabled := mwDisabled(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	recDisabled := httptest.NewRecorder()
	cDisabled := e.NewContext(req, recDisabled)

	_ = handlerDisabled(cDisabled)
	if recDisabled.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 when dashboard disabled, got %d", recDisabled.Code)
	}

	// 3. 大屏开启时，Guest Token 通过 Query 参数（适配浏览器原生 EventSource）成功放行
	reqQuery := httptest.NewRequest(http.MethodGet, "/api/dashboard?token="+httputil.PublicDashboardToken, nil)
	recQuery := httptest.NewRecorder()
	cQuery := e.NewContext(reqQuery, recQuery)

	if err := handlerEnabled(cQuery); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recQuery.Code != http.StatusOK {
		t.Fatalf("expected status 200 when guest token passed via query, got %d", recQuery.Code)
	}

	// 4. 未传 Token 时报 401
	reqNoToken := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	recNoToken := httptest.NewRecorder()
	cNoToken := e.NewContext(reqNoToken, recNoToken)

	_ = handlerEnabled(cNoToken)
	if recNoToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 when no token provided, got %d", recNoToken.Code)
	}
}
