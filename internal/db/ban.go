package db

import (
	"context"

	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BanRepo struct {
	store *Store
}

// ---- Manager methods ----

func (r *BanRepo) List(ctx context.Context) ([]model.Ban, error) {
	var records []model.Ban
	if err := r.store.db.WithContext(ctx).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (r *BanRepo) DeleteByKey(ctx context.Context, key string, afterSave ...func(tx *gorm.DB) error) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.Ban{}, "key = ?", key).Error; err != nil {
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
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Ban{}).Error; err != nil {
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
	return r.store.db.WithContext(ctx).Delete(&model.Ban{}, "key IN ?", keys).Error
}

// Upsert writes a full Ban to DB using INSERT-or-UPDATE.
// Used by the in-memory BanCache to flush accumulated counts.
func (r *BanRepo) Upsert(ctx context.Context, record model.Ban) error {
	return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"count":          record.Count,
			"last_failed_at": record.LastFailedAt,
			"ip":             record.IP,
			"username":       record.Username,
			"subject":        record.Subject,
			"source":         record.Source,
		}),
	}).Create(&record).Error
}

// UpsertFull writes a complete ban record including banned_until and reason.
// Used by all ban-expiry writes (auto-triggered and manual); the counting
// flush path uses Upsert, which deliberately omits banned_until/reason to
// avoid clobbering a newer expiry with a stale snapshot.
func (r *BanRepo) UpsertFull(ctx context.Context, record model.Ban, afterSave ...func(tx *gorm.DB) error) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}},
			DoUpdates: clause.Assignments(map[string]any{
				"subject":        record.Subject,
				"source":         record.Source,
				"ip":             record.IP,
				"username":       record.Username,
				"last_failed_at": record.LastFailedAt,
				"banned_until":   record.BannedUntil,
				"reason":         record.Reason,
			}),
		}).Create(&record).Error; err != nil {
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
