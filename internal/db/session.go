package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
)

type SessionRepo struct {
	store *Store
}

func (r *SessionRepo) Get(ctx context.Context, sessionID uint) (model.Session, error) {
	var session model.Session
	if err := r.store.db.WithContext(ctx).Select("id,token,user_id,expires_at").Where("id = ? AND expires_at > ?", sessionID, time.Now()).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Session{}, fmt.Errorf("session %d: %w", sessionID, contract.ErrNotFound)
		}
		return model.Session{}, err
	}
	return session, nil
}

// GetByUserID returns all active sessions for a user.
// Replaces the old single-session GetSessionByUserID to support multi-device logins.
func (r *SessionRepo) GetByUserID(ctx context.Context, userID uint) ([]model.Session, error) {
	var sessions []model.Session
	err := r.store.db.WithContext(ctx).Where("user_id = ? AND expires_at > ?", userID, time.Now()).
		Order("created_at ASC, id ASC").
		Find(&sessions).Error
	return sessions, err
}

func (r *SessionRepo) ListByUserIDPage(ctx context.Context, userID uint, limit, offset int) ([]model.Session, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	base := r.store.db.WithContext(ctx).Model(&model.Session{}).
		Where("user_id = ? AND expires_at > ?", userID, time.Now())
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []model.Session
	if err := base.Omit("Token").Order("created_at ASC, id ASC").Limit(limit).Offset(offset).Find(&sessions).Error; err != nil {
		return nil, 0, err
	}
	return sessions, total, nil
}

// CountByUser returns the number of active sessions a user currently holds.
func (r *SessionRepo) CountByUser(ctx context.Context, userID uint) (int64, error) {
	var count int64
	err := r.store.db.WithContext(ctx).Model(&model.Session{}).Where("user_id = ? AND expires_at > ?", userID, time.Now()).Count(&count).Error
	return count, err
}

// List returns a page of non-expired sessions. limit must be > 0.
func (r *SessionRepo) List(ctx context.Context, limit, offset int) ([]model.Session, int64, error) {
	return r.list(ctx, limit, offset, "")
}

// ListByUsername returns a page of non-expired sessions for one username (exact match).
func (r *SessionRepo) ListByUsername(ctx context.Context, username string, limit, offset int) ([]model.Session, int64, error) {
	return r.list(ctx, limit, offset, username)
}

func (r *SessionRepo) list(ctx context.Context, limit, offset int, username string) ([]model.Session, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	now := time.Now()
	base := r.store.db.WithContext(ctx).Model(&model.Session{}).
		Where("expires_at > ?", now)
	if username != "" {
		base = base.Where("username = ?", username)
	}

	var total int64
	// Clone so Count does not pollute the Find statement chain.
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Username-scoped lists prefer recent activity; full admin list keeps stable id order.
	order := "id ASC"
	if username != "" {
		order = "updated_at DESC, id DESC"
	}

	var sessions []model.Session
	// Omit bearer tokens: list APIs never return them (json:"-"), and loading them
	// into every admin/user list page would retain secrets in process memory.
	if err := base.Omit("Token").Order(order).Limit(limit).Offset(offset).Find(&sessions).Error; err != nil {
		return nil, 0, err
	}
	return sessions, total, nil
}

func (r *SessionRepo) Create(ctx context.Context, session *model.Session) error {
	// CreatedAt/UpdatedAt 由 GORM 自动维护
	return r.store.db.WithContext(ctx).Create(session).Error
}

func (r *SessionRepo) Renew(ctx context.Context, sessionID uint, expiresAt time.Time) error {
	result := r.store.db.WithContext(ctx).Model(&model.Session{}).
		Where("id = ? AND expires_at > ?", sessionID, time.Now()).
		Updates(map[string]any{
			"expires_at": expiresAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("session %d: %w", sessionID, contract.ErrNotFound)
	}
	return nil
}

func (r *SessionRepo) GetByToken(ctx context.Context, token string) (*model.Session, error) {
	var session model.Session
	if err := r.store.db.WithContext(ctx).Where("token = ? AND expires_at > ?", token, time.Now()).Take(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("session by token: %w", contract.ErrNotFound)
		}
		return nil, err
	}
	return &session, nil
}

func (r *SessionRepo) DeleteByUsername(ctx context.Context, username string, afterSave ...func(tx *gorm.DB) error) error {
	// 使用 Where 条件而不是结构体字段，避免 GORM 忽略零值
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("username = ?", username).Delete(&model.Session{}).Error; err != nil {
			return err
		}
		for _, fn := range afterSave {
			if err := fn(tx); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SessionRepo) GetByIDs(ctx context.Context, ids []uint) ([]model.Session, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var sessions []model.Session
	if err := r.store.db.WithContext(ctx).Where("id IN ?", ids).Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

func (r *SessionRepo) DeleteByIDs(ctx context.Context, sessionIDs []uint, afterSave ...func(tx *gorm.DB) error) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id IN ?", sessionIDs).Delete(&model.Session{}).Error; err != nil {
			return err
		}
		for _, fn := range afterSave {
			if err := fn(tx); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SessionRepo) DeleteExpired(ctx context.Context, afterSave ...func(tx *gorm.DB) error) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at < ?", time.Now()).Delete(&model.Session{}).Error; err != nil {
			return err
		}
		for _, fn := range afterSave {
			if err := fn(tx); err != nil {
				return err
			}
		}
		return nil
	})
}
