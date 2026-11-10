package model

import (
	"testing"
)

func TestRole_String(t *testing.T) {
	tests := []struct {
		role     Role
		expected string
	}{
		{RoleNormal, "normalUser"},
		{RoleAuditAdmin, "auditAdmin"},
		{RoleSystemAdmin, "systemAdmin"},
		{RoleSecurityAdmin, "securityAdmin"},
		{RoleSuperAdmin, "superAdmin"},
		{Role(99), "role(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.role.String()
			if got != tt.expected {
				t.Errorf("Role(%d).String() = %q, want %q", tt.role, got, tt.expected)
			}
		})
	}
}

func TestValidRole(t *testing.T) {
	tests := []struct {
		role     Role
		expected bool
	}{
		{RoleNormal, true},
		{RoleAuditAdmin, true},
		{RoleSystemAdmin, true},
		{RoleSecurityAdmin, true},
		{RoleSuperAdmin, true},
		{Role(-1), false},
		{Role(5), false},
		{Role(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.role.String(), func(t *testing.T) {
			got := ValidRole(tt.role)
			if got != tt.expected {
				t.Errorf("ValidRole(%d) = %v, want %v", tt.role, got, tt.expected)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	tests := []struct {
		input    string
		expected Role
		wantErr  bool
	}{
		{"normalUser", RoleNormal, false},
		{"NormalUser", RoleNormal, false},
		{"NORMALUSER", RoleNormal, false},
		{"auditAdmin", RoleAuditAdmin, false},
		{"AUDITADMIN", RoleAuditAdmin, false},
		{"systemAdmin", RoleSystemAdmin, false},
		{"securityAdmin", RoleSecurityAdmin, false},
		{"superAdmin", RoleSuperAdmin, false},
		{"normal", 0, true},
		{"", 0, true},
		{"unknown", 0, true},
		{"manager", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRole(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRole(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err == nil && got != tt.expected {
				t.Errorf("ParseRole(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRoleConstants(t *testing.T) {
	if int(RoleNormal) != 0 {
		t.Errorf("RoleNormal should be 0, got %d", RoleNormal)
	}
	if int(RoleAuditAdmin) != 1 {
		t.Errorf("RoleAuditAdmin should be 1, got %d", RoleAuditAdmin)
	}
	if int(RoleSystemAdmin) != 3 {
		t.Errorf("RoleSystemAdmin should be 3, got %d", RoleSystemAdmin)
	}
	if int(RoleSecurityAdmin) != 2 {
		t.Errorf("RoleSecurityAdmin should be 2, got %d", RoleSecurityAdmin)
	}
	if int(RoleSuperAdmin) != 4 {
		t.Errorf("RoleSuperAdmin should be 4, got %d", RoleSuperAdmin)
	}
}

func TestParseRole_RoundTrip(t *testing.T) {
	for _, r := range []Role{RoleNormal, RoleAuditAdmin, RoleSystemAdmin, RoleSecurityAdmin, RoleSuperAdmin} {
		parsed, err := ParseRole(r.String())
		if err != nil {
			t.Errorf("ParseRole(%q) unexpected error: %v", r.String(), err)
			continue
		}
		if parsed != r {
			t.Errorf("ParseRole.String roundtrip: %d → %q → %d", r, r.String(), parsed)
		}
	}
}

func TestAllRoles(t *testing.T) {
	roles := AllRoles()
	want := []Role{RoleNormal, RoleAuditAdmin, RoleSecurityAdmin, RoleSystemAdmin, RoleSuperAdmin}
	if len(roles) != len(want) {
		t.Fatalf("AllRoles() length = %d, want %d", len(roles), len(want))
	}
	for i, r := range want {
		if roles[i] != r {
			t.Errorf("AllRoles()[%d] = %d, want %d", i, roles[i], r)
		}
	}
}
