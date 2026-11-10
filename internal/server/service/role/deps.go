package role

import (
	"context"

	"design-cloud-engine/internal/model"
)

// UserRoleRepo is the subset of db.UserRepo role.Service needs.
// Implemented by *db.UserRepo.
type UserRoleRepo interface {
	GetByName(ctx context.Context, username string) (model.User, error)
	Update(ctx context.Context, user *model.User) error
	ListMembersByRole(ctx context.Context, role model.Role, limit, offset int) ([]string, int64, error)
}
