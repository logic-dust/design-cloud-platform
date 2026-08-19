package middleware

import (
	"net/http"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

func TestRequireRoleOrAdmin_AllowsExpectedRole(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	setSessionOnContext(c, model.RoleSystemAdmin)

	handler := requireRoleOrAdminScope(model.RoleSystemAdmin)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireRoleOrSuperAdmin(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	setSessionOnContext(c, model.RoleSecurityAdmin)

	handler := RequireRoleOrSuperAdmin(model.RoleSecurityAdmin)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Normal user with AdminScope should fail RequireRoleOrSuperAdmin
	_, c2, rec2 := setupEchoTest(http.MethodGet, "/")
	c2.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "scoped_user",
		Token:       "token",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeAll),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler2 := RequireRoleOrSuperAdmin(model.RoleSecurityAdmin)(okHandler)
	if err := handler2(c2); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec2.Code, http.StatusForbidden)
	}
}

func TestRequireRoleOrAdmin_AllowsNormalUserWithScopeForSysAdmin(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "scoped_user",
		Token:       "token",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeAll),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler := requireRoleOrAdminScope(model.RoleSystemAdmin, model.ScopeAll)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireRoleOrAdmin_AllowsScopeForSecurity(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "scoped_user",
		Token:       "token",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeAll),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	// ScopeAll should pass any admin route with admin scope
	handler := requireRoleOrAdminScope(model.RoleSecurityAdmin, model.ScopeUsers)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireRoleOrAdmin_AllowsScopeForAudit(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "scoped_user",
		Token:       "token",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeBans),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler := requireRoleOrAdminScope(model.RoleAuditAdmin, model.ScopeBans)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireRoleOrAdmin_AuditReadOnly(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodPost, "/")
	setSessionOnContext(c, model.RoleAuditAdmin)

	handler := requireRoleOrAdminScope(model.RoleAuditAdmin)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequireRoleAllowedInAdminMode_RejectsOldSuperAdminSessionInThreeRoleMode(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	setSessionOnContext(c, model.RoleSuperAdmin)

	handler := requireThreeRoleGate()(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequireRoleAllowedInAdminMode_RejectsOldThreeRoleSessionInSingleAdminMode(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	setSessionOnContext(c, model.RoleSystemAdmin)

	handler := requireSingleAdminGate()(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequirePermission_AllowsMatchingScope(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "user",
		Token:       "t",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeNodes),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler := RequireScope(model.ScopeNodes)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequirePermission_RejectsNonMatchingScope(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "user",
		Token:       "t",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeNodes),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler := RequireScope(model.ScopeFiles)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequirePermission_AllowsPermAll(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "user",
		Token:       "t",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(model.ScopeAll),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler := RequireScope(model.ScopeFiles)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequirePermission_RejectsNoAdminScope(t *testing.T) {
	_, c, rec := setupEchoTest(http.MethodGet, "/")
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username:    "user",
		Token:       "t",
		Role:        model.RoleNormal,
		AdminScopes: model.AdminScopes(0),
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	handler := RequireScope(model.ScopeNodes)(okHandler)
	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func okHandler(c *echo.Context) error {
	return c.String(http.StatusOK, "ok")
}
