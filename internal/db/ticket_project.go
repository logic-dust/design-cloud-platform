package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

type TicketProjectRepo struct {
	store *Store
}

func (r *TicketProjectRepo) Create(ctx context.Context, project *model.TicketProject) error {
	return r.store.db.WithContext(ctx).Create(project).Error
}

func (r *TicketProjectRepo) GetByID(ctx context.Context, id uint) (model.TicketProject, error) {
	var project model.TicketProject
	if err := r.store.db.WithContext(ctx).Unscoped().First(&project, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.TicketProject{}, fmt.Errorf("ticket_project(id=%d): %w", id, common.ErrNotFound)
		}
		return model.TicketProject{}, err
	}
	return project, nil
}

func (r *TicketProjectRepo) List(ctx context.Context, limit, offset int, includeArchived, includeDeleted bool) ([]model.TicketProject, int64, error) {
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

	query = query.Order("id ASC")

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	if err := query.Find(&projects).Error; err != nil {
		return nil, 0, err
	}

	return projects, total, nil
}

func (r *TicketProjectRepo) Update(ctx context.Context, project *model.TicketProject) error {
	result := r.store.db.WithContext(ctx).Model(project).Select("*").Updates(project)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ticket project(id=%d): %w", project.ID, common.ErrNotFound)
	}
	return nil
}

func (r *TicketProjectRepo) SoftDelete(ctx context.Context, id uint) error {
	result := r.store.db.WithContext(ctx).Delete(&model.TicketProject{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ticket_project(id=%d): %w", id, common.ErrNotFound)
	}
	return nil
}

func (r *TicketProjectRepo) Archive(ctx context.Context, id uint, archived bool) error {
	result := r.store.db.WithContext(ctx).Model(&model.TicketProject{}).
		Where("id = ?", id).
		Update("is_archived", archived)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ticket_project(id=%d): %w", id, common.ErrNotFound)
	}
	return nil
}
