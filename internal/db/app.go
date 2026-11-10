package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"

	"gorm.io/gorm"
)

var (
	ErrAppNotFoundToUpdate = errors.New("cannot find this app to update")
	ErrAppNameExists       = errors.New("app name already exists")
)

type AppRepo struct {
	store *Store
}

func (r *AppRepo) Create(ctx context.Context, app *model.App) error {
	if err := r.store.db.WithContext(ctx).Create(app).Error; err != nil {
		if IsUniqueConstraintError(err) {
			return ErrAppNameExists
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

const appListFields = "id, name, alias, type, icon, public, about, queue, created_at, updated_at"

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
			db.Table("user_apps").
				Select("app_id").
				Joins("JOIN users ON users.id = user_apps.user_id").
				Where("users.username = ?", username),
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
			return ErrAppNameExists
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAppNotFoundToUpdate
	}
	return nil
}

// Delete 硬删除应用，并在同一事务内清理 user_apps 关联行。
// App 不使用软删除（无 DeletedAt）：Name 唯一约束要求物理移除，否则残留行会永久占用唯一索引。
// 本项目禁用物理外键（DisableForeignKeyConstraintWhenMigrating），因此关联表需显式清理。
func (r *AppRepo) Delete(ctx context.Context, appID uint) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 清理多对多关联（逻辑外键，无级联）
		if err := tx.Exec("DELETE FROM user_apps WHERE app_id = ?", appID).Error; err != nil {
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
	err := r.store.db.WithContext(ctx).Table("users u").
		Joins("JOIN user_apps ua ON ua.user_id = u.id").
		Where("u.username = ? AND ua.app_id = ?", username, appID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetUsers replaces the entire user list for an app atomically within a transaction.
func (r *AppRepo) SetUsers(ctx context.Context, appID uint, users []model.User) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Validate app exists within transaction
		var app model.App
		if err := tx.First(&app, appID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
			}
			return err
		}

		// Clear existing associations
		if err := tx.Model(&app).Association("Users").Clear(); err != nil {
			return err
		}

		// Add new associations for all provided users
		if len(users) > 0 {
			if err := tx.Model(&app).Association("Users").Append(users); err != nil {
				return err
			}
		}

		return nil // Commit transaction
	})
}

// AddUsers adds multiple users to an app atomically within a transaction.
// Users who already have access are skipped (idempotent operation).
func (r *AppRepo) AddUsers(ctx context.Context, appID uint, users []model.User) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(users) == 0 {
			return nil
		}

		// Validate app exists within transaction
		var app model.App
		if err := tx.First(&app, appID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
			}
			return err
		}

		// Fetch existing associations in one query
		var existingUserIDs []uint
		if err := tx.Table("user_apps").Where("app_id = ?", appID).Pluck("user_id", &existingUserIDs).Error; err != nil {
			return err
		}

		existingMap := make(map[uint]bool, len(existingUserIDs))
		for _, id := range existingUserIDs {
			existingMap[id] = true
		}

		var toAdd []model.User
		for _, u := range users {
			if !existingMap[u.ID] {
				toAdd = append(toAdd, u)
			}
		}

		// Add new associations in one batch
		if len(toAdd) > 0 {
			if err := tx.Model(&app).Association("Users").Append(toAdd); err != nil {
				return err
			}
		}

		return nil // Commit transaction
	})
}

// RemoveUsers removes multiple users from an app atomically within a transaction.
// Users who don't have access are skipped (idempotent operation).
func (r *AppRepo) RemoveUsers(ctx context.Context, appID uint, users []model.User) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(users) == 0 {
			return nil
		}

		// Validate app exists within transaction
		var app model.App
		if err := tx.First(&app, appID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
			}
			return err
		}

		// Remove associations in one query (GORM Association Delete is idempotent)
		if err := tx.Model(&app).Association("Users").Delete(users); err != nil {
			return err
		}

		return nil // Commit transaction
	})
}

func (r *AppRepo) ListAuthorizedUsers(ctx context.Context, appID uint) ([]string, error) {
	// First check if app exists to preserve ErrNotFound behavior
	var count int64
	if err := r.store.db.WithContext(ctx).Model(&model.App{}).Where("id = ?", appID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("app %d: %w", appID, contract.ErrNotFound)
	}

	var usernames []string
	if err := r.store.db.WithContext(ctx).Table("users").
		Joins("JOIN user_apps ON user_apps.user_id = users.id").
		Where("user_apps.app_id = ?", appID).
		Order("users.username ASC").
		Pluck("users.username", &usernames).Error; err != nil {
		return nil, err
	}
	return usernames, nil
}

func (r *AppRepo) ListAuthorizedUsersPage(ctx context.Context, appID uint, limit, offset int) ([]string, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	userQuery := r.store.db.WithContext(ctx).Table("users").
		Joins("JOIN user_apps ON user_apps.user_id = users.id").
		Where("user_apps.app_id = ?", appID)

	var total int64
	if err := userQuery.Count(&total).Error; err != nil {
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
	if err := userQuery.Order("users.username ASC").Limit(limit).Offset(offset).Pluck("users.username", &usernames).Error; err != nil {
		return nil, 0, err
	}
	return usernames, total, nil
}
