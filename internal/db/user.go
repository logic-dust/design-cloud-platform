package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserRepo struct {
	store *Store
}

func (r *UserRepo) GetByID(ctx context.Context, id uint) (model.User, error) {
	var user model.User
	if err := r.store.db.WithContext(ctx).First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, fmt.Errorf("user(id=%d): %w", id, contract.ErrNotFound)
		}
		return model.User{}, err
	}
	return user, nil
}

func (r *UserRepo) GetByName(ctx context.Context, username string) (model.User, error) {
	var user model.User
	if err := r.store.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, fmt.Errorf("user %q: %w", username, contract.ErrNotFound)
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

// List returns a page of users. limit must be > 0; use ListAll for a full scan.
// q, when non-empty after trim, filters username or last_login_ip with a
// case-insensitive substring match (%/_/\ literal). total is the filtered count.
func (r *UserRepo) List(ctx context.Context, limit, offset int, q string) ([]model.User, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	var users []model.User
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.User{})
	if q = strings.TrimSpace(q); q != "" {
		pattern := "%" + likeEscaper.Replace(strings.ToLower(q)) + "%"
		query = query.Where(
			"LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(last_login_ip) LIKE ? ESCAPE '\\'",
			pattern, pattern,
		)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query = query.Order("id ASC").Limit(limit).Offset(offset)
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// ListAll returns every user row (no pagination). Used by directory sync ID mapping.
func (r *UserRepo) ListAll(ctx context.Context) ([]model.User, error) {
	var users []model.User
	if err := r.store.db.WithContext(ctx).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// SearchAssignees 按用户名不区分大小写包含匹配 q，仅返回状态正常、无管理范围的
// 普通角色用户的 id 与 username；以 q 开头者排前，其余按用户名升序，最多 limit 条。
// q 中的 %、_ 与 \ 按字面匹配。
func (r *UserRepo) SearchAssignees(ctx context.Context, q string, limit int) ([]model.User, error) {
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	lower := strings.ToLower(q)
	escaped := likeEscaper.Replace(lower)
	var users []model.User
	err := r.store.db.WithContext(ctx).Model(&model.User{}).
		Select("id, username").
		Where("role = ? AND admin_scopes = 0 AND status = ?", model.RoleNormal, model.UserActive).
		Where("LOWER(username) LIKE ? ESCAPE '\\'", "%"+escaped+"%").
		Order(clause.OrderBy{Expression: clause.Expr{SQL: "CASE WHEN LOWER(username) LIKE ? ESCAPE '\\' THEN 0 ELSE 1 END, username ASC", Vars: []any{escaped + "%"}}}).
		Limit(limit).
		Find(&users).Error
	if err != nil {
		return nil, err
	}
	return users, nil
}

// ListHotAssignees 返回近期被指派过、且仍符合指派资格的用户，按最近指派时间降序，最多 limit 条。
func (r *UserRepo) ListHotAssignees(ctx context.Context, limit int) ([]model.User, error) {
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	var users []model.User
	err := r.store.db.WithContext(ctx).Model(&model.User{}).
		Select("users.id", "users.username").
		Joins(`INNER JOIN (
			SELECT assignee_id, MAX(updated_at) AS last_at
			FROM tickets
			WHERE assignee_id IS NOT NULL AND deleted_at IS NULL
			GROUP BY assignee_id
		) AS hot ON hot.assignee_id = users.id`).
		Where("users.role = ? AND users.admin_scopes = 0 AND users.status = ?", model.RoleNormal, model.UserActive).
		Order("hot.last_at DESC").
		Limit(limit).
		Find(&users).Error
	if err != nil {
		return nil, err
	}
	return users, nil
}

// GetAssignable 返回可被指派工单的用户（与 SearchAssignees 同一条件）；不满足条件视为不存在。
func (r *UserRepo) GetAssignable(ctx context.Context, id uint) (model.User, error) {
	var u model.User
	err := r.store.db.WithContext(ctx).Select("id, username").
		Where("id = ? AND role = ? AND admin_scopes = 0 AND status = ?", id, model.RoleNormal, model.UserActive).
		First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return u, fmt.Errorf("assignable user(id=%d): %w", id, contract.ErrNotFound)
	}
	return u, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

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
		return fmt.Errorf("user %q: %w", username, contract.ErrNotFound)
	}
	return nil
}

// ChangeStatusIf updates status for usernames that currently match fromStatus.
// Returns the number of rows updated.
func (r *UserRepo) ChangeStatusIf(ctx context.Context, usernames []string, fromStatus, toStatus model.UserStatusCode) (int64, error) {
	if len(usernames) == 0 {
		return 0, nil
	}
	result := r.store.db.WithContext(ctx).Model(&model.User{}).
		Where("username IN ? AND status = ?", usernames, fromStatus).
		Update("status", toStatus)
	return result.RowsAffected, result.Error
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
		return fmt.Errorf("user %q: %w", username, contract.ErrNotFound)
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
		return fmt.Errorf("user %q: %w", user.Username, contract.ErrNotFound)
	}
	return nil
}

func (r *UserRepo) Create(ctx context.Context, user *model.User) error {
	return r.store.db.WithContext(ctx).Where("username = ?", user.Username).FirstOrCreate(user).Error
}

func (r *UserRepo) Delete(ctx context.Context, username string) error {
	user, err := r.GetByName(ctx, username)
	if err != nil {
		return err
	}
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.UserConnectionResource{}).Error; err != nil {
			return err
		}
		return tx.Delete(&user).Error
	})
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
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	var usernames []string
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.User{}).Where("role = ?", role)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	query = query.Limit(limit).Offset(offset)
	if err := query.Order("username ASC").Pluck("username", &usernames).Error; err != nil {
		return nil, 0, err
	}
	return usernames, total, nil
}
