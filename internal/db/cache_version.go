package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"design-cloud-engine/internal/model"
)

// CacheVersionRepo 管理多节点缓存广播版本号
type CacheVersionRepo struct {
	store *Store
}

// Bump 在独立短事务内递增缓存主题版本号（无其它业务状态变更时使用，如纯缓存刷新）。
func (r *CacheVersionRepo) Bump(ctx context.Context, name string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.BumpTx(ctx, tx, name)
	})
}

// BumpTx 在指定的事务内将对应缓存主题 name 的版本号原子递增。
// 必须在业务状态变更的同一个事务内调用。
func (r *CacheVersionRepo) BumpTx(ctx context.Context, tx *gorm.DB, name string) error {
	res := tx.WithContext(ctx).
		Model(&model.CacheVersion{}).
		Where("name = ?", name).
		Update("version", gorm.Expr("version + 1"))
	if res.Error != nil {
		return fmt.Errorf("bump cache version for %s: %w", name, res.Error)
	}
	if res.RowsAffected == 0 {
		// 如果记录不存在（理论上通过 initCacheVersions 保证存在），退级插入
		err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CacheVersion{
			Name:    name,
			Version: 1,
		}).Error
		if err != nil {
			return fmt.Errorf("create cache version for %s: %w", name, err)
		}
	}
	return nil
}

// GetAll 无锁获取所有缓存主题的最新版本号，用于轮询协调器
func (r *CacheVersionRepo) GetAll(ctx context.Context) (map[string]int64, error) {
	var records []model.CacheVersion
	if err := r.store.db.WithContext(ctx).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("get all cache versions: %w", err)
	}

	res := make(map[string]int64, len(records))
	for _, rec := range records {
		res[rec.Name] = rec.Version
	}
	return res, nil
}
