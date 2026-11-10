package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
	echoMW "github.com/labstack/echo/v5/middleware"
)

// 普通请求策略不依赖路由名称后缀隐式豁免超时。
func TestRequestTimeoutAppliesToStreamNamedRoutes(t *testing.T) {
	e := echo.New()
	g := e.Group("/api", echoMW.ContextTimeoutWithConfig(echoMW.ContextTimeoutConfig{
		Timeout: DefaultRequestContextTimeout,
	}))
	hasDeadline := func(c *echo.Context) error {
		if _, ok := c.Request().Context().Deadline(); ok {
			return c.String(http.StatusOK, "deadline")
		}
		return c.String(http.StatusOK, "none")
	}
	g.GET("/overview/cluster", hasDeadline)
	g.GET("/overview/cluster/stream", hasDeadline)

	for _, target := range []string{
		"/api/overview/cluster",
		"/api/overview/cluster?stream=true",
		"/api/overview/cluster/stream",
	} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if got := rec.Body.String(); got != "deadline" {
			t.Fatalf("%s: got %q, want deadline", target, got)
		}
	}
}

func TestOverviewClusterStreamPolicyHasNoRequestTimeout(t *testing.T) {
	policies := BuildRoutePolicies(nil, &mockSecurityManager{}, PolicyConfig{
		RateLimit:         10,
		RateBurst:         10,
		TransferRateLimit: 10,
		TransferRateBurst: 10,
		WSRateLimit:       10,
		WSRateBurst:       10,
	})
	e := echo.New()
	g := e.Group("/api", policies[PolicyOverviewClusterStream]...)
	g.GET("/overview/cluster/stream", func(c *echo.Context) error {
		if _, ok := c.Request().Context().Deadline(); ok {
			return c.String(http.StatusInternalServerError, "unexpected deadline")
		}
		return c.String(http.StatusOK, "no deadline")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/overview/cluster/stream", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+httputil.PublicOverviewToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "no deadline" {
		t.Fatalf("overview stream policy = status %d, body %q; want no request deadline", rec.Code, rec.Body.String())
	}
}
