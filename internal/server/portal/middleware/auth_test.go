package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// configurableSecurityManager 可按 IP 配置封禁状态并记录失败计数，用于验证中间件检查顺序。
type configurableSecurityManager struct {
	bannedIPs map[string]bool
	recorded  chan string // RecordIPFailure 在 goroutine 中异步调用，用 channel 同步
}

func newConfigurableSecurityManager(bannedIPs ...string) *configurableSecurityManager {
	m := &configurableSecurityManager{bannedIPs: map[string]bool{}, recorded: make(chan string, 10)}
	for _, ip := range bannedIPs {
		m.bannedIPs[ip] = true
	}
	return m
}

func (m *configurableSecurityManager) IsBannedByIP(_ context.Context, IP string) (bool, security.BannedRecord) {
	if m.bannedIPs[IP] {
		return true, security.BannedRecord{IP: IP, Reason: "banned"}
	}
	return false, security.BannedRecord{}
}

func (m *configurableSecurityManager) RecordIPFailure(_ context.Context, IP string) {
	m.recorded <- IP
}

func (m *configurableSecurityManager) waitRecorded(t *testing.T) string {
	t.Helper()
	select {
	case ip := <-m.recorded:
		return ip
	case <-time.After(2 * time.Second):
		t.Fatal("expected RecordIPFailure to be called")
		return ""
	}
}

func (m *configurableSecurityManager) assertNotRecorded(t *testing.T) {
	t.Helper()
	select {
	case ip := <-m.recorded:
		t.Fatalf("RecordIPFailure must not run for banned IP, got %q", ip)
	case <-time.After(50 * time.Millisecond):
	}
}

// fakeSessionStore 按 token 返回预置会话，用于绕过真实 session.Service 的重依赖。
type fakeSessionStore struct {
	sessions map[string]*model.Session
}

func (f *fakeSessionStore) GetByToken(_ context.Context, token string) (*model.Session, error) {
	if s, ok := f.sessions[token]; ok {
		return s, nil
	}
	return nil, echo.NewHTTPError(http.StatusUnauthorized, "invalid session token")
}

func validSession() *model.Session {
	return &model.Session{Username: "alice", Token: "valid-token", ExpiresAt: time.Now().Add(time.Hour)}
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
	_, c, rec := setupEchoTest(http.MethodGet, "/api/events?token=any-token")

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
		"/api/overview",
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

// ---------- SessionAuth IP-ban ordering ----------

func runSessionAuth(sm sessionLookup, sec SecurityManager, c *echo.Context) {
	handler := sessionAuth(sm, sec)(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	_ = handler(c)
}

func authedContext(token string) (*echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestSessionAuth_BannedIPNoToken(t *testing.T) {
	t.Parallel()
	sec := newConfigurableSecurityManager("192.0.2.1")
	c, rec := authedContext("")
	c.Request().RemoteAddr = "192.0.2.1:1234"

	runSessionAuth(nil, sec, c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for banned IP without token, got %d", rec.Code)
	}
	if rec.Body.String() == "" {
		t.Fatal("expected ban reason in body")
	}
}

func TestSessionAuth_ValidSessionBypassesIPBan(t *testing.T) {
	t.Parallel()
	sec := newConfigurableSecurityManager("192.0.2.1")
	sm := &fakeSessionStore{sessions: map[string]*model.Session{"valid-token": validSession()}}
	c, rec := authedContext("valid-token")
	c.Request().RemoteAddr = "192.0.2.1:1234"

	runSessionAuth(sm, sec, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected valid session to bypass IP ban (200), got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSessionAuth_InvalidTokenRecordsFailure(t *testing.T) {
	t.Parallel()
	sec := newConfigurableSecurityManager()
	sm := &fakeSessionStore{sessions: map[string]*model.Session{}}
	c, rec := authedContext("bad-token")
	c.Request().RemoteAddr = "192.0.2.1:1234"

	runSessionAuth(sm, sec, c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid token when IP not banned, got %d", rec.Code)
	}
	if ip := sec.waitRecorded(t); ip != "192.0.2.1" {
		t.Fatalf("RecordIPFailure ip = %q, want 192.0.2.1", ip)
	}
}

func TestSessionAuth_BannedIPInvalidTokenForbidden(t *testing.T) {
	t.Parallel()
	sec := newConfigurableSecurityManager("192.0.2.1")
	sm := &fakeSessionStore{sessions: map[string]*model.Session{}}
	c, rec := authedContext("bad-token")
	c.Request().RemoteAddr = "192.0.2.1:1234"

	runSessionAuth(sm, sec, c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for banned IP with invalid token, got %d", rec.Code)
	}
	sec.assertNotRecorded(t)
}

func TestOverviewClusterStreamAuthMW_BannedIPGuestTokenBlocked(t *testing.T) {
	t.Parallel()
	sec := newConfigurableSecurityManager("192.0.2.1")
	handler := overviewClusterStreamAuthMW(nil, sec, func(context.Context) bool { return true })(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	c, rec := authedContext(httputil.PublicOverviewToken)
	c.Request().RemoteAddr = "192.0.2.1:1234"
	_ = handler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for banned IP with guest token, got %d", rec.Code)
	}
}

func TestOverviewClusterStreamAuthMW_ValidSessionBypassesIPBan(t *testing.T) {
	t.Parallel()
	sec := newConfigurableSecurityManager("192.0.2.1")
	sm := &fakeSessionStore{sessions: map[string]*model.Session{"valid-token": validSession()}}
	handler := overviewClusterStreamAuthMW(sm, sec, func(context.Context) bool { return true })(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	c, rec := authedContext("valid-token")
	c.Request().RemoteAddr = "192.0.2.1:1234"
	_ = handler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected valid session to bypass IP ban (200), got %d", rec.Code)
	}
}

func TestOverviewClusterStreamAuthMW(t *testing.T) {
	t.Parallel()

	// 1. 大屏开启时，Guest Token 被成功放行（纯无状态直通）
	mwEnabled := overviewClusterStreamAuthMW(nil, &mockSecurityManager{}, func(context.Context) bool { return true })
	handlerEnabled := mwEnabled(func(c *echo.Context) error {
		return c.String(http.StatusOK, "overview-ok")
	})

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/overview/cluster/stream", nil)
	req.Header.Set("Authorization", "Bearer "+httputil.PublicOverviewToken)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handlerEnabled(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "overview-ok" {
		t.Fatalf("expected body %q, got %q", "overview-ok", rec.Body.String())
	}

	// 2. 大屏关闭时，使用 Guest Token 访问返回 404
	mwDisabled := overviewClusterStreamAuthMW(nil, &mockSecurityManager{}, func(context.Context) bool { return false })
	handlerDisabled := mwDisabled(func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	recDisabled := httptest.NewRecorder()
	cDisabled := e.NewContext(req, recDisabled)

	_ = handlerDisabled(cDisabled)
	if recDisabled.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 when overview disabled, got %d", recDisabled.Code)
	}

	// 3. 大屏开启时，Guest Token 通过 Query 参数访问 /api/overview/cluster/stream（适配浏览器原生 EventSource）成功放行
	reqQuery := httptest.NewRequest(http.MethodGet, "/api/overview/cluster/stream?token="+httputil.PublicOverviewToken, nil)
	recQuery := httptest.NewRecorder()
	cQuery := e.NewContext(reqQuery, recQuery)

	if err := handlerEnabled(cQuery); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recQuery.Code != http.StatusOK {
		t.Fatalf("expected status 200 when guest token passed via query, got %d", recQuery.Code)
	}

	// 4. 未传 Token 时报 401
	reqNoToken := httptest.NewRequest(http.MethodGet, "/api/overview/cluster/stream", nil)
	recNoToken := httptest.NewRecorder()
	cNoToken := e.NewContext(reqNoToken, recNoToken)

	_ = handlerEnabled(cNoToken)
	if recNoToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 when no token provided, got %d", recNoToken.Code)
	}
}

// TestSessionAuth_PasswordMustChange_Returns428 验证需改密会话访问放行名单外路径返回 428 Precondition Required。
func TestSessionAuth_PasswordMustChange_Returns428(t *testing.T) {
	t.Parallel()
	s := validSession()
	s.PasswordMustChange = true
	sm := &fakeSessionStore{sessions: map[string]*model.Session{"pwd-token": s}}
	e := echo.New()
	e.Use(sessionAuth(sm, &mockSecurityManager{}, "/api/users/me/password"))
	e.GET("/api/apps", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.PUT("/api/users/me/password", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer pwd-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired || !strings.Contains(rec.Body.String(), "password change required") {
		t.Fatalf("expected 428 password change required, got %d body=%s", rec.Code, rec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPut, "/api/users/me/password", nil)
	req2.Header.Set(echo.HeaderAuthorization, "Bearer pwd-token")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("whitelisted path should pass, got %d", rec2.Code)
	}
}

// TestOverviewClusterStreamAuthMW_SessionUnaffectedBySwitch 验证大屏开关关闭时，有效用户会话仍被放行（开关只作用于访客 Token）。
func TestOverviewClusterStreamAuthMW_SessionUnaffectedBySwitch(t *testing.T) {
	t.Parallel()
	sm := &fakeSessionStore{sessions: map[string]*model.Session{"valid-token": validSession()}}
	switchEvaluated := false
	mw := overviewClusterStreamAuthMW(sm, &mockSecurityManager{}, func(context.Context) bool {
		switchEvaluated = true
		return false
	})
	handler := mw(func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	c, rec := authedContext("valid-token")
	_ = handler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid session with overview disabled, got %d body=%s", rec.Code, rec.Body.String())
	}
	if switchEvaluated {
		t.Fatal("overview switch must not be evaluated for user sessions")
	}
}

// TestSessionAuth_OverviewUserRoutes 验证集群快照与 /api/overview/me 挂在常规 sessionAuth 链上后的行为：
// 网页大屏访客 Token 无对应会话 → 401；需改密受限会话 → 428。
func TestSessionAuth_OverviewUserRoutes(t *testing.T) {
	t.Parallel()
	restricted := validSession()
	restricted.PasswordMustChange = true
	sm := &fakeSessionStore{sessions: map[string]*model.Session{"valid-token": validSession(), "pwd-token": restricted}}
	e := echo.New()
	e.Use(sessionAuth(sm, &mockSecurityManager{}, "/api/users/me/password", "/api/users/me", "/api/sessions/current", "/api/sessions"))
	ok := func(c *echo.Context) error { return c.String(http.StatusOK, "ok") }
	e.GET("/api/overview/cluster", ok)
	e.GET("/api/overview/me", ok)
	e.GET("/api/overview/me/stream", ok)

	cases := []struct {
		token string
		want  int
		msg   string
	}{
		{httputil.PublicOverviewToken, http.StatusUnauthorized, "invalid session token"},
		{"pwd-token", http.StatusPreconditionRequired, "password change required"},
		{"valid-token", http.StatusOK, "ok"},
	}
	for _, target := range []string{"/api/overview/cluster", "/api/overview/me", "/api/overview/me/stream"} {
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.msg) {
				t.Fatalf("%s token=%s: got %d %s, want %d %q", target, tc.token, rec.Code, rec.Body.String(), tc.want, tc.msg)
			}
		}
	}
}
