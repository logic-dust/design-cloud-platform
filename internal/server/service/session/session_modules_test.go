package session

import (
	"slices"
	"testing"

	"design-cloud-engine/internal/model"
)

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
			wantModules: []string{"overview", "sessions"},
			notModules:  []string{"security"},
		},
		{
			name:        "bans scope",
			role:        model.RoleNormal,
			scopes:      model.AdminScopes(model.ScopeBans),
			wantModules: []string{"overview", "security"},
			notModules:  []string{"sessions"},
		},
		{
			name:        "security admin role",
			role:        model.RoleSecurityAdmin,
			wantModules: []string{"overview", "sessions", "security"},
		},
		{
			name:        "super admin role",
			role:        model.RoleSuperAdmin,
			wantModules: []string{"overview", "sessions", "security"},
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
