package user

import "design-cloud-engine/internal/common"

var (
	ErrInvalidUserStatus = common.NewValidationError("invalid status")
	ErrForbidden         = common.NewForbiddenError("insufficient permissions to modify this user")
	ErrUserNotFound      = common.NewNotFoundError("user not found")
	// ErrScopeRequiresNormalRole indicates a non-normal user cannot have scopes.
	ErrScopeRequiresNormalRole = common.NewValidationError("admin scopes require normal user role")
	// ErrAdminScopeDisabledInThreeRoles indicates scopes are not used in three-role mode.
	ErrAdminScopeDisabledInThreeRoles = common.NewValidationError("admin scopes are disabled in three-role mode")
	// ErrPasswordPolicy indicates the password failed local strength validation.
	ErrPasswordPolicy = common.NewValidationError("password policy violation")
)
