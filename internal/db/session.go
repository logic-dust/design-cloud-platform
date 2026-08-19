package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

type SessionRepo struct {
	store *Store
}

func (r *SessionRepo) Get(ctx context.Context, sessionID uint) (model.Session, error) {
	var session model.Session
	if err := r.store.db.WithContext(ctx).Select("id,token,user_id,expires_at").Where("id = ? AND expires_at > ?", sessionID, time.Now()).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Session{}, fmt.Errorf("session %d: %w", sessionID, common.ErrNotFound)
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

// CountByUser returns the number of active sessions a user currently holds.
func (r *SessionRepo) CountByUser(ctx context.Context, userID uint) (int64, error) {
	var count int64
	err := r.store.db.WithContext(ctx).Model(&model.Session{}).Where("user_id = ? AND expires_at > ?", userID, time.Now()).Count(&count).Error
	return count, err
}

func (r *SessionRepo) List(ctx context.Context, limit, offset int) ([]model.Session, int64, error) {
	var sessions []model.Session
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.Session{}).
		Select("id,user_id,username,token,ip,role,user_type,admin_scopes,created_at,updated_at,expires_at").
		Where("expires_at > ?", time.Now())
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}
	if err := query.Find(&sessions).Error; err != nil {
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
		return fmt.Errorf("session %d: %w", sessionID, common.ErrNotFound)
	}
	return nil
}

func (r *SessionRepo) GetByToken(ctx context.Context, token string) (*model.Session, error) {
	var session model.Session
	if err := r.store.db.WithContext(ctx).Where("token = ? AND expires_at > ?", token, time.Now()).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("session by token: %w", common.ErrNotFound)
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
