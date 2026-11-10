package session

import (
	"slices"
	"testing"

	"design-cloud-engine/internal/model"
)

func TestBuildSessionContextPayload_PortalFlagsByRole(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	tests := []struct {
		name           string
		role           model.Role
		scopes         model.AdminScopes
		wantAdmin      bool
		wantUserPortal bool
	}{
		{name: "normal", role: model.RoleNormal, wantAdmin: false, wantUserPortal: true},
		{name: "scoped normal", role: model.RoleNormal, scopes: model.AdminScopes(model.ScopeUsers), wantAdmin: true, wantUserPortal: true},
		{name: "audit admin", role: model.RoleAuditAdmin, wantAdmin: true, wantUserPortal: false},
		{name: "security admin", role: model.RoleSecurityAdmin, wantAdmin: true, wantUserPortal: false},
		{name: "system admin", role: model.RoleSystemAdmin, wantAdmin: true, wantUserPortal: false},
		{name: "super admin", role: model.RoleSuperAdmin, wantAdmin: true, wantUserPortal: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := svc.BuildSessionContextPayload(t.Context(), &model.Session{
				Role:        tt.role,
				AdminScopes: tt.scopes,
				Username:    "u",
			}, nil, "127.0.0.1", false)

			if payload.UI.AdminConsole.Enabled != tt.wantAdmin {
				t.Errorf("adminConsole.enabled = %v, want %v", payload.UI.AdminConsole.Enabled, tt.wantAdmin)
			}
			if payload.UI.UserPortal.Enabled != tt.wantUserPortal {
				t.Errorf("userPortal.enabled = %v, want %v", payload.UI.UserPortal.Enabled, tt.wantUserPortal)
			}
		})
	}
}

func TestBuildAdminConsoleModulesSeparatesSessionAndSecurityPages(t *testing.T) {
	tests := []struct {
		name        string
		role        model.Role
		scopes      model.AdminScopes
		wantModules []string
		notModules  []string
	}{
		{
			name:        "sessions scope",
			role:        model.RoleNormal,
			scopes:      model.AdminScopes(model.ScopeSessions),
			wantModules: []string{"overview", "session"},
			notModules:  []string{"ban"},
		},
		{
			name:        "bans scope",
			role:        model.RoleNormal,
			scopes:      model.AdminScopes(model.ScopeBans),
			wantModules: []string{"overview", "ban"},
			notModules:  []string{"session"},
		},
		{
			name:        "security admin role",
			role:        model.RoleSecurityAdmin,
			wantModules: []string{"overview", "session", "ban"},
		},
		{
			name:        "super admin role",
			role:        model.RoleSuperAdmin,
			wantModules: []string{"overview", "desktop", "session", "ban"},
		},
		{
			name:        "desktop scope",
			role:        model.RoleNormal,
			scopes:      model.AdminScopes(model.ScopeDesktop),
			wantModules: []string{"overview", "desktop", "settings"},
			notModules:  []string{"session", "node"},
		},
		{
			name:        "system admin role",
			role:        model.RoleSystemAdmin,
			wantModules: []string{"overview", "settings", "desktop"},
			notModules:  []string{"session", "ban"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildAdminConsoleModules(&model.Session{
				Role:        tt.role,
				AdminScopes: tt.scopes,
			})

			for _, module := range tt.wantModules {
				if !slices.Contains(got, module) {
					t.Errorf("modules %v does not contain %q", got, module)
				}
			}
			for _, module := range tt.notModules {
				if slices.Contains(got, module) {
					t.Errorf("modules %v unexpectedly contains %q", got, module)
				}
			}
		})
	}
}
