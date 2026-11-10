package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/portal/middleware"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

func TestAdminBatchRoutesRequireTheirScope(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		scope      model.AdminScope
		other      model.AdminScope
		wantStatus int
	}{
		{
			name:       "node batch update",
			method:     http.MethodPost,
			path:       "/api/admin/system/nodes/batch-update",
			scope:      model.ScopeNodes,
			other:      model.ScopeConnections,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "node settings batch update",
			method:     http.MethodPost,
			path:       "/api/admin/system/nodes/settings/batch-update",
			scope:      model.ScopeNodes,
			other:      model.ScopeConnections,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "connection resource batch upsert",
			method:     http.MethodPost,
			path:       "/api/admin/system/connections/resources/batch-upsert",
			scope:      model.ScopeConnections,
			other:      model.ScopeNodes,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "desktop release batch delete",
			method:     http.MethodPost,
			path:       "/api/admin/system/desktop/releases/files/batch-delete",
			scope:      model.ScopeDesktop,
			other:      model.ScopeSettings,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "desktop release upload",
			method:     http.MethodPut,
			path:       "/api/admin/system/desktop/releases/files/invalid.txt",
			scope:      model.ScopeDesktop,
			other:      model.ScopeSettings,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "desktop plugin upload",
			method:     http.MethodPut,
			path:       "/api/admin/system/desktop/plugins/BadName/platforms/win",
			scope:      model.ScopeDesktop,
			other:      model.ScopeSettings,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "settings desktop domain update",
			method:     http.MethodPut,
			path:       "/api/admin/system/settings/desktop",
			scope:      model.ScopeDesktop,
			other:      model.ScopeApps,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "settings platform domain update",
			method:     http.MethodPut,
			path:       "/api/admin/system/settings/platform",
			scope:      model.ScopeSettings,
			other:      model.ScopeDesktop,
			wantStatus: http.StatusUnprocessableEntity,
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
	_, settingsSvc := newTestSettingsServiceForAPI(t)
	h := buildZeroHandler()
	h.desktopClientHandler = newDesktopClientHandler(desktop.NewService(t.TempDir(), nil, nil))
	h.settingsHandler = newSettingsHandler(settingsSvc)
	h.policies = map[middleware.APIPolicy][]echo.MiddlewareFunc{
		middleware.PolicySysAdmin:             {policy},
		middleware.PolicySysAdminFileTransfer: {policy},
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
				{name: "matching scope reaches handler", scopes: model.AdminScopes(tc.scope), status: tc.wantStatus},
			} {
				t.Run(check.name, func(t *testing.T) {
					scopes = check.scopes
					req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{"))
					req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
					rec := httptest.NewRecorder()
					e.ServeHTTP(rec, req)
					if rec.Code != check.status {
						t.Fatalf("%s %s with %s: status=%d, want %d; body=%s", tc.method, tc.path, check.name, rec.Code, check.status, rec.Body.String())
					}
				})
			}
		})
	}
}
