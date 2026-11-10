package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TaskLeaseRepo struct {
	store *Store
}

// TryAcquire 尝试获取或续租租约。epoch 仅在 acquired=true 时有意义：
//   - 同 Owner 存活续约：epoch 不变（否则每 15s 变更一次会让在途 fencing 校验全部失效）；
//   - 新建行、新 Owner 抢占、同 Owner 跨过期重获：epoch+1（Owner 在「租约已过期但尚未察觉」
//     窗口内仍以旧 epoch 签发命令，重获后 epoch 不变会让这些旧命令继续被视为有效）。
func (r *TaskLeaseRepo) TryAcquire(ctx context.Context, taskName, ownerID string, ttl time.Duration) (bool, uint64, error) {
	if taskName == "" || ownerID == "" || ttl <= 0 {
		return false, 0, nil
	}

	var acquired bool
	var epoch uint64
	err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lease model.TaskLease

		// Lease expiry is compared across engines, so the clock must be the database clock.
		// A failed read aborts this attempt instead of mixing in the local process clock.
		dbNow, err := getDatabaseTimeInTx(tx)
		if err != nil {
			return fmt.Errorf("read database time: %w", err)
		}

		expiresAt := dbNow.Add(ttl)

		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_name = ?", taskName).
			First(&lease).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			acquired = true
			epoch = 1
			return tx.Create(&model.TaskLease{
				TaskName:  taskName,
				OwnerID:   ownerID,
				Epoch:     epoch,
				ExpiresAt: expiresAt,
			}).Error
		case err != nil:
			return err
		case lease.OwnerID == ownerID && lease.ExpiresAt.After(dbNow):
			// 存活续约：只顺延过期时间，epoch 不变。
			acquired = true
			epoch = lease.Epoch
			return tx.Model(&lease).Updates(map[string]any{
				"expires_at": expiresAt,
			}).Error
		case !lease.ExpiresAt.After(dbNow):
			// 租约已过期（任意 Owner）：重新获取，epoch+1。
			acquired = true
			epoch = lease.Epoch + 1
			return tx.Model(&lease).Updates(map[string]any{
				"owner_id":   ownerID,
				"epoch":      epoch,
				"expires_at": expiresAt,
			}).Error
		default:
			acquired = false
			return nil
		}
	})
	if err != nil {
		return false, 0, err
	}
	if acquired {
		return true, epoch, nil
	}
	return false, 0, nil
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

// getDatabaseTimeInTx returns the clock used for lease expiry.
// PostgreSQL is the shared store for multiple engines, so the clock is the database clock.
// SQLite is single-process (tests and standalone); the process clock is enough and is not read from the driver.
func getDatabaseTimeInTx(tx *gorm.DB) (time.Time, error) {
	if tx.Dialector.Name() != "postgres" {
		return time.Now(), nil
	}
	var dbNow time.Time
	if err := tx.Raw("SELECT NOW()").Scan(&dbNow).Error; err != nil {
		return time.Time{}, err
	}
	return dbNow, nil
}
