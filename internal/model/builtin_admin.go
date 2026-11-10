package model

// Built-in portal admin usernames seeded at install / bootstrap.
// Admin-target protection keys off Role, not these strings; constants avoid drift in bootstrap and tests.
const (
	UsernameBuiltinSuperAdmin    = "dceadmin"
	UsernameBuiltinSecurityAdmin = "securityadmin"
	UsernameBuiltinSystemAdmin   = "systemadmin"
	UsernameBuiltinAuditAdmin    = "auditadmin"
)

// BuiltinAdminAccount is a default app-user seed for admin mode bootstrap.
type BuiltinAdminAccount struct {
	Username string
	Role     Role
}

// BuiltinAdminUsernames lists every reserved portal admin username (exact match).
var BuiltinAdminUsernames = [...]string{
	UsernameBuiltinSuperAdmin,
	UsernameBuiltinSecurityAdmin,
	UsernameBuiltinSystemAdmin,
	UsernameBuiltinAuditAdmin,
}

// IsBuiltinAdminUsername reports whether username is a reserved built-in admin name.
func IsBuiltinAdminUsername(username string) bool {
	for _, name := range BuiltinAdminUsernames {
		if username == name {
			return true
		}
	}
	return false
}

// AllBuiltinAdminAccounts returns all built-in admin seeds (always four rows in DB).
func AllBuiltinAdminAccounts() []BuiltinAdminAccount {
	return []BuiltinAdminAccount{
		{Username: UsernameBuiltinSuperAdmin, Role: RoleSuperAdmin},
		{Username: UsernameBuiltinSecurityAdmin, Role: RoleSecurityAdmin},
		{Username: UsernameBuiltinSystemAdmin, Role: RoleSystemAdmin},
		{Username: UsernameBuiltinAuditAdmin, Role: RoleAuditAdmin},
	}
}

// BuiltinAdminAccounts returns admin seeds that may log in under the active admin mode.
func BuiltinAdminAccounts(enableThreeRoles bool) []BuiltinAdminAccount {
	if enableThreeRoles {
		return []BuiltinAdminAccount{
			{Username: UsernameBuiltinSecurityAdmin, Role: RoleSecurityAdmin},
			{Username: UsernameBuiltinSystemAdmin, Role: RoleSystemAdmin},
			{Username: UsernameBuiltinAuditAdmin, Role: RoleAuditAdmin},
		}
	}
	return []BuiltinAdminAccount{
		{Username: UsernameBuiltinSuperAdmin, Role: RoleSuperAdmin},
	}
}

// NewBuiltinAdminUser returns an app-user row for bootstrap (no password; must set via CLI before login).
func NewBuiltinAdminUser(a BuiltinAdminAccount) User {
	return User{
		Username: a.Username,
		Password: "",
		Role:     a.Role,
		Status:   UserPasswordExpired,
		Type:     UserTypeApp,
	}
}

// ConflictingAdminRoles returns admin roles that must be disabled when the given mode is active.
func ConflictingAdminRoles(enableThreeRoles bool) []Role {
	if enableThreeRoles {
		return []Role{RoleSuperAdmin}
	}
	return []Role{RoleSecurityAdmin, RoleSystemAdmin, RoleAuditAdmin}
}
