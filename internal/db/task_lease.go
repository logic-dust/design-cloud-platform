package db

import (
	"context"
	"errors"
	"time"

	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TaskLeaseRepo struct {
	store *Store
}

func (r *TaskLeaseRepo) TryAcquire(ctx context.Context, taskName, ownerID string, ttl time.Duration) (bool, error) {
	if taskName == "" || ownerID == "" || ttl <= 0 {
		return false, nil
	}

	var acquired bool
	err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lease model.TaskLease

		// Use database time for consistency across nodes
		dbNow, err := getDatabaseTimeInTx(tx)
		if err != nil {
			// Fallback to local time if database query fails
			dbNow = time.Now()
		}

		expiresAt := dbNow.Add(ttl)

		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_name = ?", taskName).
			First(&lease).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			acquired = true
			return tx.Create(&model.TaskLease{
				TaskName:  taskName,
				OwnerID:   ownerID,
				ExpiresAt: expiresAt,
			}).Error
		case err != nil:
			return err
		case lease.OwnerID == ownerID || !lease.ExpiresAt.After(dbNow):
			acquired = true
			return tx.Model(&lease).Updates(map[string]any{
				"owner_id":   ownerID,
				"expires_at": expiresAt,
			}).Error
		default:
			acquired = false
			return nil
		}
	})
	if err != nil {
		return false, err
	}
	if acquired {
		return true, nil
	}
	return false, nil
}

func (r *TaskLeaseRepo) GetDatabaseTime(ctx context.Context) (time.Time, error) {
	return getDatabaseTimeInTx(r.store.db.WithContext(ctx))
}

func (r *TaskLeaseRepo) Get(ctx context.Context, taskName string) (model.TaskLease, bool, error) {
	if taskName == "" {
		return model.TaskLease{}, false, nil
	}

	var lease model.TaskLease
	if err := r.store.db.WithContext(ctx).Where("task_name = ?", taskName).First(&lease).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.TaskLease{}, false, nil
		}
		return model.TaskLease{}, false, err
	}
	return lease, true, nil
}

func (r *TaskLeaseRepo) ReleaseIfOwner(ctx context.Context, taskName, ownerID string) (bool, error) {
	if taskName == "" || ownerID == "" {
		return false, nil
	}

	result := r.store.db.WithContext(ctx).Where("task_name = ? AND owner_id = ?", taskName, ownerID).Delete(&model.TaskLease{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ---- Pure helper functions (keep as is) ----

func getDatabaseTimeInTx(tx *gorm.DB) (time.Time, error) {
	var dbNow time.Time
	if err := tx.Raw("SELECT CURRENT_TIMESTAMP").Scan(&dbNow).Error; err != nil {
		return time.Time{}, err
	}
	return dbNow, nil
}
