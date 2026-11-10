package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/portal/middleware"

	"github.com/labstack/echo/v5"
)

// 单管理员模式没有可分配角色（dceadmin 为内置唯一超级管理员）：
// 角色分配子路由必须由 RequireRole(SecurityAdmin) 在入口拒绝 SuperAdmin，
// 不允许落到 handler 里再做模式判断。
func TestRoleWriteRoute_RequiresSecurityRole(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		role       model.Role
		wantStatus int
	}{
		{"superadmin rejected (single-admin mode)", model.RoleSuperAdmin, http.StatusForbidden},
		{"normal user rejected", model.RoleNormal, http.StatusForbidden},
		{"audit admin rejected", model.RoleAuditAdmin, http.StatusForbidden},
		{"system admin rejected", model.RoleSystemAdmin, http.StatusForbidden},
		{"security admin allowed (three-role mode)", model.RoleSecurityAdmin, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodPut, "/api/admin/security/users/bob/role", nil), rec)
			c.Set(httputil.CtxSessionKey, &model.Session{
				Username:  "operator",
				Token:     "token",
				Role:      tc.role,
				ExpiresAt: time.Now().Add(time.Hour),
			})

			handler := middleware.RequireRole(model.RoleSecurityAdmin)(func(*echo.Context) error {
				return c.NoContent(http.StatusOK)
			})
			if err := handler(c); err != nil {
				t.Fatalf("handler: %v", err)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
