package middleware

import (
	"net/http"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// RequireRoleOrSuperAdmin checks exact role match OR RoleSuperAdmin.
// Unlike requireRoleOrAdminScope, this does NOT check admin scopes — it is used for
// actions that should only be accessible by the role itself (e.g., scope assignment).
func RequireRoleOrSuperAdmin(expected model.Role) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			info, err := httputil.SessionOrForbidden(c)
			if err != nil {
				return httputil.RespondError(c, err)
			}
			if info.Role == model.RoleSuperAdmin {
				return next(c)
			}
			if info.Role == expected {
				return next(c)
			}
			return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "insufficient permissions"))
		}
	}
}

// RequireRole checks that the session has the exact required role.
// Unlike RequireRoleOrSuperAdmin, this does NOT allow SuperAdmin bypass.
// Used for endpoints that should only be accessible by a specific admin role
// in three-role mode (e.g., role-setting by SecurityAdmin).
func RequireRole(expected model.Role) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			info, err := httputil.SessionOrForbidden(c)
			if err != nil {
				return httputil.RespondError(c, err)
			}
			if info.Role != expected {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "insufficient permissions"))
			}
			return next(c)
		}
	}
}

// requireReadOnly restricts AuditAdmin to GET/HEAD/OPTIONS methods.
// Used in three-role mode to enforce audit role read-only constraint.
func requireReadOnly() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			method := c.Request().Method
			if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "audit role is read-only"))
			}
			return next(c)
		}
	}
}

// requireRoleOrAdminScope checks role match OR RoleSuperAdmin OR admin scope.
// Used in single-admin mode — RoleSuperAdmin bypasses all checks, normal
// users can pass with matching admin scopes.
// For audit routes, RoleAuditAdmin is restricted to GET/HEAD/OPTIONS; RoleSuperAdmin bypasses this.
func requireRoleOrAdminScope(expected model.Role, scopes ...model.AdminScope) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			info, err := httputil.SessionOrForbidden(c)
			if err != nil {
				return httputil.RespondError(c, err)
			}

			// 1. Admin scope — any admin route, normal users — check first, most frequent case
			if info.Role == model.RoleNormal && info.AdminScopes.HasAny(scopes...) {
				return next(c)
			}
			// 2. RoleSuperAdmin bypasses all per-role checks (single-admin mode)
			if info.Role == model.RoleSuperAdmin {
				return next(c)
			}
			// 3. Direct role match
			if info.Role == expected {
				// Audit role is read-only
				if expected == model.RoleAuditAdmin {
					method := c.Request().Method
					if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
						return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "audit role is read-only"))
					}
				}
				return next(c)
			}
			return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "insufficient permissions"))
		}
	}
}

// RequireScope checks that the current session holds at least one of the
// specified AdminScopes, OR has a role-based access (any admin role).
// ScopeAll users pass via HasAny short-circuit.
// SuperAdmin role (4) bypasses entirely — no scope check needed.
// In three-role mode, role-based users (role > RoleNormal) also bypass
// because the parent group middleware already validated their role.
func RequireScope(scopes ...model.AdminScope) echo.MiddlewareFunc {
	if len(scopes) == 0 {
		// Fail-closed: misconfigured caller gets a clear 500 rather than a silent
		// no-op middleware (would bypass auth) or a panic (would crash the process).
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "RequireScope: at least one scope is required")
			}
		}
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			info, err := httputil.SessionOrForbidden(c)
			if err != nil {
				return httputil.RespondError(c, err)
			}
			// SuperAdmin bypasses all scope checks
			if info.Role == model.RoleSuperAdmin {
				return next(c)
			}
			// Three-role mode: role-based users pass scope checks
			// (parent group middleware already validated their role)
			if info.Role > model.RoleNormal {
				return next(c)
			}
			if !info.AdminScopes.HasAny(scopes...) {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "insufficient AdminScope"))
			}
			return next(c)
		}
	}
}

// requireSingleAdminGate rejects sessions with three-role roles
// (AuditAdmin, SystemAdmin, SecurityAdmin) in single-admin mode.
func requireSingleAdminGate() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			info, err := httputil.SessionOrForbidden(c)
			if err != nil {
				return httputil.RespondError(c, err)
			}
			if info.Role == model.RoleAuditAdmin || info.Role == model.RoleSystemAdmin || info.Role == model.RoleSecurityAdmin {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "role not allowed in current admin mode"))
			}
			if !model.ValidRole(info.Role) {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "role not allowed in current admin mode"))
			}
			return next(c)
		}
	}
}

// requireThreeRoleGate rejects SuperAdmin sessions in three-role mode.
func requireThreeRoleGate() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			info, err := httputil.SessionOrForbidden(c)
			if err != nil {
				return httputil.RespondError(c, err)
			}
			if info.Role == model.RoleSuperAdmin || !model.ValidRole(info.Role) {
				return httputil.RespondError(c, echo.NewHTTPError(http.StatusForbidden, "role not allowed in current admin mode"))
			}
			return next(c)
		}
	}
}
