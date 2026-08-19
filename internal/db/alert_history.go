package db

import (
	"context"
	"time"

	"design-cloud-engine/internal/model"
)

type AlertHistoryRepo struct {
	store *Store
}

// Create 插入一条告警历史记录（全量保留，不做强行删除截断）。
func (r *AlertHistoryRepo) Create(ctx context.Context, item *model.AlertHistory) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	return r.store.db.WithContext(ctx).Create(item).Error
}

// List 查出最新的告警历史记录。
func (r *AlertHistoryRepo) List(ctx context.Context, limit int) ([]model.AlertHistory, error) {
	if limit <= 0 {
		limit = 100
	}
	var items []model.AlertHistory
	err := r.store.db.WithContext(ctx).Order("id desc").Limit(limit).Find(&items).Error
	return items, err
}
