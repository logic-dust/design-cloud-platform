package role

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

var (
	// ErrInvalidRole is a client error: unsupported or forbidden role value.
	ErrInvalidRole = contract.NewValidationError("invalid role")
	// ErrAdminUsernamePrefixRequired is a client error: admin accounts need the role prefix.
	ErrAdminUsernamePrefixRequired = contract.NewValidationError("admin username prefix required")
	// ErrSuperAdminImmutable rejects role writes that would change the seeded SuperAdmin.
	ErrSuperAdminImmutable = contract.NewForbiddenError("cannot modify superadmin role")
)

// Service handles role management (three-role mode: SecurityAdmin assigns roles).
type Service struct {
	userRepo UserRoleRepo
	auditSvc *audit.Service
}

// NewService creates a role management service.
// Session eviction after role changes is orchestrated by the identity application layer
// (identity.AssignRole), not by this service.
func NewService(userRepo UserRoleRepo, auditSvc *audit.Service) *Service {
	return &Service{
		userRepo: userRepo,
		auditSvc: auditSvc,
	}
}

// SetUserRole sets a single role for the target user (atomic write only —
// session eviction is orchestrated by identity.AssignRole).
// The route is only mounted in three-role mode (see securityAdminAPI);
// SuperAdmin (RoleSuperAdmin) can never be assigned nor modified.
func (s *Service) SetUserRole(ctx context.Context, username string, role model.Role) (model.User, error) {
	if !model.ValidRole(role) {
		return model.User{}, fmt.Errorf("%w: %d", ErrInvalidRole, role)
	}
	if role == model.RoleSuperAdmin {
		return model.User{}, fmt.Errorf("%w: cannot set SuperAdmin role", ErrInvalidRole)
	}

	// Validate username prefix for admin roles
	switch role {
	case model.RoleSystemAdmin:
		if username != model.UsernameBuiltinSystemAdmin && !strings.HasPrefix(username, model.UsernameBuiltinSystemAdmin+"_") {
			return model.User{}, fmt.Errorf("%w: systemadmin_ prefix required", ErrAdminUsernamePrefixRequired)
		}
	case model.RoleSecurityAdmin:
		if username != model.UsernameBuiltinSecurityAdmin && !strings.HasPrefix(username, model.UsernameBuiltinSecurityAdmin+"_") {
			return model.User{}, fmt.Errorf("%w: securityadmin_ prefix required", ErrAdminUsernamePrefixRequired)
		}
	case model.RoleAuditAdmin:
		if username != model.UsernameBuiltinAuditAdmin && !strings.HasPrefix(username, model.UsernameBuiltinAuditAdmin+"_") {
			return model.User{}, fmt.Errorf("%w: auditadmin_ prefix required", ErrAdminUsernamePrefixRequired)
		}
	}

	dbUser, err := s.userRepo.GetByName(ctx, username)
	if err != nil {
		return model.User{}, fmt.Errorf("get user %q for role update: %w", username, err)
	}
	// SuperAdmin (dceadmin) is seeded and immutable through admin APIs.
	if dbUser.Role == model.RoleSuperAdmin {
		return model.User{}, ErrSuperAdminImmutable
	}

	scopesCleared := dbUser.AdminScopes != 0
	dbUser.Role = role
	dbUser.AdminScopes = 0
	if err := s.userRepo.Update(ctx, &dbUser); err != nil {
		return model.User{}, fmt.Errorf("update user %q role: %w", username, err)
	}
	s.auditSvc.LogUser(ctx, "USER_ROLE_UPDATE", "success", username, slog.LevelInfo, map[string]any{
		"role":               role.String(),
		"admin_scopes_clear": scopesCleared,
	})
	return dbUser, nil
}

// ListRoleMembers returns paginated members of a single role.
func (s *Service) ListRoleMembers(ctx context.Context, role model.Role, limit, offset int) ([]string, int64, error) {
	if !model.ValidRole(role) {
		return nil, 0, fmt.Errorf("%w: %d", ErrInvalidRole, role)
	}
	members, total, err := s.userRepo.ListMembersByRole(ctx, role, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list members of role %q: %w", role.String(), err)
	}
	return members, total, nil
}
