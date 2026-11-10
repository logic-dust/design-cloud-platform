package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

type TicketProjectRepo struct {
	store *Store
}

func (r *TicketProjectRepo) Create(ctx context.Context, project *model.TicketProject) error {
	return r.store.db.WithContext(ctx).Create(project).Error
}

// Get 读取项目；includeDeleted 为 false 时已删除项目视为不存在。
func (r *TicketProjectRepo) Get(ctx context.Context, id uint, includeDeleted bool) (model.TicketProject, error) {
	var project model.TicketProject
	q := r.store.db.WithContext(ctx)
	if includeDeleted {
		q = q.Unscoped()
	}
	if err := q.First(&project, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.TicketProject{}, fmt.Errorf("ticket_project(id=%d): %w", id, contract.ErrNotFound)
		}
		return model.TicketProject{}, err
	}
	return project, nil
}

func (r *TicketProjectRepo) List(ctx context.Context, limit, offset int, includeArchived, includeDeleted bool) ([]model.TicketProject, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	var projects []model.TicketProject
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.TicketProject{})
	if !includeArchived {
		query = query.Where("is_archived = ?", false)
	}
	if includeDeleted {
		query = query.Unscoped()
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id ASC").Limit(limit).Offset(offset).Find(&projects).Error; err != nil {
		return nil, 0, err
	}
	return projects, total, nil
}

// UpdateFields 更新未删除项目的字段。
func (r *TicketProjectRepo) UpdateFields(ctx context.Context, id uint, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	result := r.store.db.WithContext(ctx).Model(&model.TicketProject{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ticket_project(id=%d): %w", id, contract.ErrNotFound)
	}
	return nil
}

func (r *TicketProjectRepo) SoftDelete(ctx context.Context, id uint) error {
	result := r.store.db.WithContext(ctx).Delete(&model.TicketProject{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ticket_project(id=%d): %w", id, contract.ErrNotFound)
	}
	return nil
}
