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

func TestPolicySysAdminFileTransferRegisteredInBothAdminModes(t *testing.T) {
	for _, threeRoles := range []bool{false, true} {
		policies := BuildRoutePolicies(nil, &mockSecurityManager{}, PolicyConfig{
			EnableThreeRoles:  threeRoles,
			RateLimit:         10,
			RateBurst:         10,
			TransferRateLimit: 10,
			TransferRateBurst: 10,
			WSRateLimit:       10,
			WSRateBurst:       10,
		})
		chain := policies[PolicySysAdminFileTransfer]
		if len(chain) == 0 {
			t.Fatalf("threeRoles=%v: PolicySysAdminFileTransfer chain is empty", threeRoles)
		}

		e := echo.New()
		g := e.Group("/api/admin/system/desktop", chain...)
		g.PUT("/releases/files/:filename", func(c *echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/releases/files/app.exe", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("threeRoles=%v: unauthenticated upload status=%d, want 401", threeRoles, rec.Code)
		}
	}
}
