package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAppNotFoundToUpdate = errors.New("cannot find this app to update")
	ErrAppSlugExists       = errors.New("app slug already exists")
)

type AppRepo struct {
	store *Store
}

func (r *AppRepo) Create(ctx context.Context, app *model.App) error {
	if err := r.store.db.WithContext(ctx).Create(app).Error; err != nil {
		if IsUniqueConstraintError(err) {
			return ErrAppSlugExists
		}
		return err
	}
	return nil
}

func (r *AppRepo) GetByID(ctx context.Context, id uint) (model.App, error) {
	var app model.App
	if err := r.store.db.WithContext(ctx).First(&app, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.App{}, fmt.Errorf("app %d: %w", id, contract.ErrNotFound)
		}
		return model.App{}, err
	}
	return app, nil
}

const appListFields = "id, slug, name, type, icon, public, description, queue, created_at, updated_at"

// List returns a page of apps. limit must be > 0.
func (r *AppRepo) List(ctx context.Context, limit, offset int) ([]model.App, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	var apps []model.App
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.App{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	query = query.Limit(limit).Offset(offset)
	if err := query.Select(appListFields).Find(&apps).Error; err != nil {
		return nil, 0, err
	}
	return apps, total, nil
}

// ListAccessible returns all apps accessible by a user (public apps or explicitly authorized apps), ordered by name.
func (r *AppRepo) ListAccessible(ctx context.Context, username string) ([]model.App, error) {
	var apps []model.App
	err := accessibleAppsQuery(r.store.db.WithContext(ctx), username).
		Select(appListFields).
		Order("name ASC").
		Find(&apps).Error
	if err != nil {
		return nil, err
	}
	return apps, nil
}

func accessibleAppsQuery(db *gorm.DB, username string) *gorm.DB {
	return db.Model(&model.App{}).
		Where("public = ?", true).
		Or("id IN (?)",
			db.Model(&model.UserApp{}).
				Select("app_id").
				Where("username = ?", username),
		)
}

func (r *AppRepo) ListAccessiblePage(ctx context.Context, username string, limit, offset int) ([]model.App, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	db := r.store.db.WithContext(ctx)
	query := accessibleAppsQuery(db, username)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var apps []model.App
	if err := accessibleAppsQuery(db, username).
		Select(appListFields).
		Order("name ASC").
		Limit(limit).
		Offset(offset).
		Find(&apps).Error; err != nil {
		return nil, 0, err
	}
	return apps, total, nil
}

func (r *AppRepo) Update(ctx context.Context, app *model.App) error {
	if app.ID == 0 {
		return ErrAppNotFoundToUpdate
	}
	// 使用 Select 指定更新字段，避免 Save 全量更新（包括零值字段）
	result := r.store.db.WithContext(ctx).Model(app).Select("*").Updates(app)
	if result.Error != nil {
		if IsUniqueConstraintError(result.Error) {
			return ErrAppSlugExists
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAppNotFoundToUpdate
	}
	return nil
}

// Delete 硬删除应用，并在同一事务内清理 user_apps 关联行。
// App 不使用软删除（无 DeletedAt）：Slug 唯一约束要求物理移除，否则残留行会永久占用唯一索引。
// 本项目禁用物理外键（DisableForeignKeyConstraintWhenMigrating），因此关联表需显式清理。
func (r *AppRepo) Delete(ctx context.Context, appID uint) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("app_id = ?", appID).Delete(&model.UserApp{}).Error; err != nil {
			return fmt.Errorf("clear app %d user associations: %w", appID, err)
		}
		res := tx.Delete(&model.App{}, appID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return contract.ErrNotFound
		}
		return nil
	})
}

func (r *AppRepo) UserHasAccess(ctx context.Context, username string, appID uint) (bool, error) {
	var count int64
	err := r.store.db.WithContext(ctx).Model(&model.UserApp{}).
		Where("username = ? AND app_id = ?", username, appID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetUsers replaces the entire user list for an app atomically within a transaction.
func (r *AppRepo) SetUsers(ctx context.Context, appID uint, users []model.User) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var app model.App
		if err := tx.First(&app, appID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
			}
			return err
		}

		if err := tx.Where("app_id = ?", appID).Delete(&model.UserApp{}).Error; err != nil {
			return err
		}
		mappings := userAppMappings(appID, users)
		if len(mappings) == 0 {
			return nil
		}
		return tx.Create(&mappings).Error
	})
}

// AddUsers adds multiple users to an app atomically within a transaction.
// Users who already have access are skipped (idempotent operation).
func (r *AppRepo) AddUsers(ctx context.Context, appID uint, users []model.User) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		mappings := userAppMappings(appID, users)
		if len(mappings) == 0 {
			return nil
		}

		var app model.App
		if err := tx.First(&app, appID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
			}
			return err
		}

		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mappings).Error
	})
}

// RemoveUsers removes multiple users from an app atomically within a transaction.
// Users who don't have access are skipped (idempotent operation).
func (r *AppRepo) RemoveUsers(ctx context.Context, appID uint, users []model.User) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		names := usernamesFromUsers(users)
		if len(names) == 0 {
			return nil
		}

		var app model.App
		if err := tx.First(&app, appID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
			}
			return err
		}

		return tx.Where("app_id = ? AND username IN ?", appID, names).Delete(&model.UserApp{}).Error
	})
}

func userAppMappings(appID uint, users []model.User) []model.UserApp {
	if len(users) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(users))
	out := make([]model.UserApp, 0, len(users))
	for _, u := range users {
		name := u.Username
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, model.UserApp{Username: name, AppID: appID})
	}
	return out
}

func usernamesFromUsers(users []model.User) []string {
	if len(users) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(users))
	out := make([]string, 0, len(users))
	for _, u := range users {
		if u.Username == "" {
			continue
		}
		if _, ok := seen[u.Username]; ok {
			continue
		}
		seen[u.Username] = struct{}{}
		out = append(out, u.Username)
	}
	return out
}

func (r *AppRepo) ListAuthorizedUsers(ctx context.Context, appID uint) ([]string, error) {
	var count int64
	if err := r.store.db.WithContext(ctx).Model(&model.App{}).Where("id = ?", appID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
	}

	var usernames []string
	if err := r.store.db.WithContext(ctx).Model(&model.UserApp{}).
		Where("app_id = ?", appID).
		Order("username ASC").
		Pluck("username", &usernames).Error; err != nil {
		return nil, err
	}
	return usernames, nil
}

func (r *AppRepo) ListAuthorizedUsersPage(ctx context.Context, appID uint, limit, offset int) ([]string, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	base := r.store.db.WithContext(ctx).Model(&model.UserApp{}).Where("app_id = ?", appID)

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		var apps int64
		if err := r.store.db.WithContext(ctx).Model(&model.App{}).Where("id = ?", appID).Count(&apps).Error; err != nil {
			return nil, 0, err
		}
		if apps == 0 {
			return nil, 0, fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
		}
		return []string{}, 0, nil
	}

	var usernames []string
	if err := base.Order("username ASC").Limit(limit).Offset(offset).Pluck("username", &usernames).Error; err != nil {
		return nil, 0, err
	}
	return usernames, total, nil
}
