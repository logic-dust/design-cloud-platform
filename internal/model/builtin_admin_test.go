package model

import (
	"slices"
	"testing"
)

func TestBuiltinAdminAccounts(t *testing.T) {
	t.Parallel()

	single := BuiltinAdminAccounts(false)
	if len(single) != 1 || single[0].Username != UsernameBuiltinSuperAdmin || single[0].Role != RoleSuperAdmin {
		t.Fatalf("single-admin seeds = %#v, want [{%q %v}]", single, UsernameBuiltinSuperAdmin, RoleSuperAdmin)
	}

	three := BuiltinAdminAccounts(true)
	want := []BuiltinAdminAccount{
		{Username: UsernameBuiltinSecurityAdmin, Role: RoleSecurityAdmin},
		{Username: UsernameBuiltinSystemAdmin, Role: RoleSystemAdmin},
		{Username: UsernameBuiltinAuditAdmin, Role: RoleAuditAdmin},
	}
	if !slices.Equal(three, want) {
		t.Fatalf("three-role seeds = %#v, want %#v", three, want)
	}
}

func TestAllBuiltinAdminAccounts(t *testing.T) {
	t.Parallel()

	got := AllBuiltinAdminAccounts()
	if len(got) != 4 {
		t.Fatalf("AllBuiltinAdminAccounts len = %d, want 4", len(got))
	}
	wantUsernames := []string{
		UsernameBuiltinSuperAdmin,
		UsernameBuiltinSecurityAdmin,
		UsernameBuiltinSystemAdmin,
		UsernameBuiltinAuditAdmin,
	}
	for i, name := range wantUsernames {
		if got[i].Username != name {
			t.Fatalf("account[%d].Username = %q, want %q", i, got[i].Username, name)
		}
	}
}

func TestIsBuiltinAdminUsername(t *testing.T) {
	t.Parallel()

	if !IsBuiltinAdminUsername(UsernameBuiltinSuperAdmin) {
		t.Fatal("dceadmin should be built-in")
	}
	if IsBuiltinAdminUsername("systemadmin_bob") {
		t.Fatal("extended admin name should not match exact built-in list")
	}
}

func TestConflictingAdminRoles(t *testing.T) {
	t.Parallel()

	if got := ConflictingAdminRoles(true); !slices.Equal(got, []Role{RoleSuperAdmin}) {
		t.Fatalf("three-role conflicts = %v, want [%v]", got, RoleSuperAdmin)
	}
	if got := ConflictingAdminRoles(false); !slices.Equal(got, []Role{RoleSecurityAdmin, RoleSystemAdmin, RoleAuditAdmin}) {
		t.Fatalf("single-admin conflicts = %v", got)
	}
}
