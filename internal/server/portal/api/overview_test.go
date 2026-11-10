package api

import (
	"bytes"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/application/overview"
	"design-cloud-engine/internal/server/portal/middleware"

	"github.com/labstack/echo/v5"
)

// TestWriteOverviewFrame_CarriesVersion 验证 SSE 帧携带 version（引擎版本）：
// 前端收到帧即可对比版本，无需额外 fetch /api/version。
func TestWriteOverviewFrame_CarriesVersion(t *testing.T) {
	config.VERSION = "0.3.7-abc1234"

	var buf bytes.Buffer
	writeOverviewFrame(&buf, overview.Response{})

	out := buf.String()
	if !strings.HasPrefix(out, "event: overview\n") {
		t.Errorf("frame missing event line: %q", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Errorf("frame missing SSE terminator: %q", out)
	}

	// 提取 data 行并解析 version
	line := strings.TrimPrefix(out, "event: overview\ndata: ")
	line = strings.TrimSuffix(line, "\n\n")
	var frame struct {
		Data    overview.Response `json:"data"`
		Version string            `json:"version"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if frame.Version != "0.3.7-abc1234" {
		t.Errorf("version = %q, want 0.3.7-abc1234", frame.Version)
	}
}

// TestOverviewRoutes_PolicyMounting 验证路由挂载的策略链：
// 集群实时流走 PolicyOverviewClusterStream（支持网页大屏访客 Token），
// /me、/me/stream、/cluster 与裸 /api/overview（显式 404）走常规 PolicyUser。
func TestOverviewRoutes_PolicyMounting(t *testing.T) {
	h := buildZeroHandler()
	h.policies = map[middleware.APIPolicy][]echo.MiddlewareFunc{}
	for _, p := range []middleware.APIPolicy{
		middleware.PolicyGuest, middleware.PolicyUser, middleware.PolicyOverviewClusterStream, middleware.PolicyFileTransfer,
		middleware.PolicyAuditAdmin, middleware.PolicySysAdmin, middleware.PolicySecurityAdmin,
		middleware.PolicyProxyTunnel, middleware.PolicySSE,
	} {
		name := fmt.Sprint(int(p))
		h.policies[p] = []echo.MiddlewareFunc{func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error { return c.String(http.StatusOK, name) }
		}}
	}
	e := echo.New()
	h.InitRouter(e)

	want := map[string]middleware.APIPolicy{
		"/api/overview":                middleware.PolicyUser, // 显式 404 handler，仍走用户链
		"/api/overview/me":             middleware.PolicyUser,
		"/api/overview/me/stream":      middleware.PolicyUser,
		"/api/overview/cluster":        middleware.PolicyUser,
		"/api/overview/cluster/stream": middleware.PolicyOverviewClusterStream,
	}
	for target, policy := range want {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != fmt.Sprint(int(policy)) {
			t.Fatalf("%s: got status=%d policy=%q, want policy %d", target, rec.Code, rec.Body.String(), policy)
		}
	}
}

// TestOverviewBarePath_Returns404 验证裸 GET /api/overview 不提供数据。
func TestOverviewBarePath_Returns404(t *testing.T) {
	e := echo.New()
	h := &overviewHandler{}
	g := e.Group("/api/overview")
	h.overviewUserAPI(g)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/overview: got %d, want 404", rec.Code)
	}
}
