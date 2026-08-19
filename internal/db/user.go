package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

type UserRepo struct {
	store *Store
}

func (r *UserRepo) GetByID(ctx context.Context, id uint) (model.User, error) {
	var user model.User
	if err := r.store.db.WithContext(ctx).First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, fmt.Errorf("user(id=%d): %w", id, common.ErrNotFound)
		}
		return model.User{}, err
	}
	return user, nil
}

func (r *UserRepo) GetByName(ctx context.Context, username string) (model.User, error) {
	var user model.User
	if err := r.store.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, fmt.Errorf("user %q: %w", username, common.ErrNotFound)
		}
		return model.User{}, err
	}
	return user, nil
}

func (r *UserRepo) GetByNames(ctx context.Context, usernames []string) ([]model.User, error) {
	var users []model.User
	if err := r.store.db.WithContext(ctx).Select("id,username").Where("username IN ?", usernames).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepo) List(ctx context.Context, limit, offset int) ([]model.User, int64, error) {
	var users []model.User
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.User{})

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}

	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (r *UserRepo) ListSystem(ctx context.Context) ([]model.User, error) {
	var users []model.User
	if err := r.store.db.WithContext(ctx).Select("username,status").Where("type = ?", model.UserTypeSystem).Find(&users).Error; err != nil {
		r.store.log.Warn("failed to list system users", "error", err)
		return nil, err
	}
	return users, nil
}

func (r *UserRepo) ListScoped(ctx context.Context) ([]model.User, error) {
	var users []model.User
	if err := r.store.db.WithContext(ctx).
		Select("username, admin_scopes").
		Where("admin_scopes != 0").
		Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepo) ChangeStatus(ctx context.Context, username string, statusCode model.UserStatusCode) error {
	result := r.store.db.WithContext(ctx).Model(&model.User{}).
		Where("username = ?", username).
		Update("status", statusCode)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user %q: %w", username, common.ErrNotFound)
	}
	return nil
}

func (r *UserRepo) UpdateFields(ctx context.Context, username string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	result := r.store.db.WithContext(ctx).Model(&model.User{}).Where("username = ?", username).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user %q: %w", username, common.ErrNotFound)
	}
	return nil
}

func (r *UserRepo) UpdateScopesIfRole(ctx context.Context, username string, expectedRole model.Role, scopes model.AdminScopes) (bool, error) {
	result := r.store.db.WithContext(ctx).Model(&model.User{}).
		Where("username = ? AND role = ?", username, expectedRole).
		Update("admin_scopes", scopes)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *UserRepo) Update(ctx context.Context, user *model.User) error {
	if user == nil {
		return nil
	}
	result := r.store.db.WithContext(ctx).Model(user).Select("*").Updates(user)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user %q: %w", user.Username, common.ErrNotFound)
	}
	return nil
}

func (r *UserRepo) Create(ctx context.Context, user *model.User) error {
	return r.store.db.WithContext(ctx).Where("username = ?", user.Username).FirstOrCreate(user).Error
}

// UpdateUserStatusByRoles updates the status of all users matching the given roles.
func (r *UserRepo) UpdateUserStatusByRoles(ctx context.Context, roles []model.Role, status model.UserStatusCode) error {
	if len(roles) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).
		Model(&model.User{}).
		Where("role IN ?", roles).
		Update("status", status).Error
}

// ListMembersByRole returns paginated usernames with the given role.
func (r *UserRepo) ListMembersByRole(ctx context.Context, role model.Role, limit, offset int) ([]string, int64, error) {
	var usernames []string
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.User{}).Where("role = ?", role)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}
	if err := query.Order("username ASC").Pluck("username", &usernames).Error; err != nil {
		return nil, 0, err
	}
	return usernames, total, nil
}
