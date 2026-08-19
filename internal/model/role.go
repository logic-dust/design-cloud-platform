package model

import (
	"fmt"
	"strings"
)

// Role is a single flat enum; admin roles are parallel to normal (0).
// 0 = normal, 1 = audit, 2 = security, 3 = system
// RoleSuperAdmin (4) is only active in single-admin mode; unavailable in three-admin mode.
type Role int

const (
	RoleNormal        Role = 0
	RoleAuditAdmin    Role = 1
	RoleSecurityAdmin Role = 2
	RoleSystemAdmin   Role = 3
	RoleSuperAdmin    Role = 4
)

// ValidRole reports whether r is one of the predefined Role values.
func ValidRole(r Role) bool {
	return r >= RoleNormal && r <= RoleSuperAdmin
}

func (r Role) String() string {
	switch r {
	case RoleNormal:
		return "normalUser"
	case RoleAuditAdmin:
		return "auditAdmin"
	case RoleSystemAdmin:
		return "systemAdmin"
	case RoleSecurityAdmin:
		return "securityAdmin"
	case RoleSuperAdmin:
		return "superAdmin"
	default:
		return fmt.Sprintf("role(%d)", r)
	}
}

// ParseRole converts a case-insensitive string to a Role.
func ParseRole(s string) (Role, error) {
	switch strings.ToLower(s) {
	case "normal", "normaluser":
		return RoleNormal, nil
	case "auditadmin":
		return RoleAuditAdmin, nil
	case "systemadmin":
		return RoleSystemAdmin, nil
	case "securityadmin":
		return RoleSecurityAdmin, nil
	case "admin", "superadmin":
		return RoleSuperAdmin, nil
	default:
		return 0, fmt.Errorf("unknown role: %q", s)
	}
}

// AllRoles returns all predefined roles in declaration order.
func AllRoles() []Role {
	return []Role{RoleNormal, RoleAuditAdmin, RoleSecurityAdmin, RoleSystemAdmin, RoleSuperAdmin}
}
