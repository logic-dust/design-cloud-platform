package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/portal/middleware"

	"github.com/labstack/echo/v5"
)

func TestAdminBatchRoutesRequireTheirScope(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		scope model.AdminScope
		other model.AdminScope
	}{
		{
			name:  "node batch update",
			path:  "/api/admin/system/nodes/batch-update",
			scope: model.ScopeNodes,
			other: model.ScopeConnections,
		},
		{
			name:  "node settings batch update",
			path:  "/api/admin/system/nodes/settings/batch-update",
			scope: model.ScopeNodes,
			other: model.ScopeConnections,
		},
		{
			name:  "connection resource batch upsert",
			path:  "/api/admin/system/connections/resources/batch-upsert",
			scope: model.ScopeConnections,
			other: model.ScopeNodes,
		},
	}

	var scopes model.AdminScopes
	policy := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(httputil.CtxSessionKey, &model.Session{
				Username:    "scoped-user",
				Role:        model.RoleNormal,
				AdminScopes: scopes,
			})
			return next(c)
		}
	}
	h := buildZeroHandler()
	h.policies = map[middleware.APIPolicy][]echo.MiddlewareFunc{
		middleware.PolicySysAdmin: {policy},
	}
	e := echo.New()
	h.InitRouter(e)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, check := range []struct {
				name   string
				scopes model.AdminScopes
				status int
			}{
				{name: "no scope denied", scopes: 0, status: http.StatusForbidden},
				{name: "different scope denied", scopes: model.AdminScopes(tc.other), status: http.StatusForbidden},
				{name: "matching scope reaches handler", scopes: model.AdminScopes(tc.scope), status: http.StatusBadRequest},
			} {
				t.Run(check.name, func(t *testing.T) {
					scopes = check.scopes
					req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{"))
					req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
					rec := httptest.NewRecorder()
					e.ServeHTTP(rec, req)
					if rec.Code != check.status {
						t.Fatalf("POST %s with %s: status=%d, want %d; body=%s", tc.path, check.name, rec.Code, check.status, rec.Body.String())
					}
				})
			}
		})
	}
}
