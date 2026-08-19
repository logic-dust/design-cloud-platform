package db

import (
	"context"
	"time"

	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BanRepo struct {
	store *Store
}

// ---- Manager methods ----

func (r *BanRepo) List(ctx context.Context) ([]model.BanRecord, error) {
	var records []model.BanRecord
	if err := r.store.db.WithContext(ctx).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (r *BanRepo) DeleteByKey(ctx context.Context, key string, afterSave ...func(tx *gorm.DB) error) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.BanRecord{}, "key = ?", key).Error; err != nil {
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

func (r *BanRepo) DeleteAll(ctx context.Context, afterSave ...func(tx *gorm.DB) error) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.BanRecord{}).Error; err != nil {
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

func (r *BanRepo) SetBannedUntil(ctx context.Context, key string, until *time.Time, afterSave ...func(tx *gorm.DB) error) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.BanRecord{}).Where("key = ?", key).Update("banned_until", until).Error; err != nil {
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

// DeleteByKeys bulk-deletes auth failure records by their keys.
// Used by BanCache.pruneExpired to remove stale entries from DB.
func (r *BanRepo) DeleteByKeys(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Delete(&model.BanRecord{}, "key IN ?", keys).Error
}

// Upsert writes a full BanRecord to DB using INSERT-or-UPDATE.
// Used by the in-memory BanCache to flush accumulated counts.
func (r *BanRepo) Upsert(ctx context.Context, record model.BanRecord) error {
	return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"count":      record.Count,
			"updated_at": record.UpdatedAt,
			"ip":         record.IP,
			"username":   record.Username,
			"type":       record.Type,
		}),
	}).Create(&record).Error
}
