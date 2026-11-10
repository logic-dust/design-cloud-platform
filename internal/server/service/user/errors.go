package user

import (
	"design-cloud-engine/internal/contract"
)

var (
	ErrInvalidUserStatus = contract.NewValidationError("invalid status")
	ErrForbidden         = contract.NewForbiddenError("insufficient permissions to modify this user")
	ErrUserNotFound      = contract.NewNotFoundError("user not found")
	// ErrScopeRequiresNormalRole indicates a non-normal user cannot have scopes.
	ErrScopeRequiresNormalRole = contract.NewValidationError("admin scopes require normal user role")
	// ErrAdminScopeDisabledInThreeRoles indicates scopes are not used in three-role mode.
	ErrAdminScopeDisabledInThreeRoles = contract.NewValidationError("admin scopes are disabled in three-role mode")
	// ErrPasswordPolicy indicates the password failed local strength validation.
	ErrPasswordPolicy = contract.NewValidationError("password policy violation")
)
