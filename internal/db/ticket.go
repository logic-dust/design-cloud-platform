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

// TicketFilter defines filtering criteria for listing tickets.
type TicketFilter struct {
	ProjectID      *uint
	ReporterID     *uint
	AssigneeID     *uint
	Type           *model.TicketType
	Status         *model.TicketStatus
	Priority       *model.TicketPriority
	Limit          int
	Offset         int
	IncludeDeleted bool
}

// DailyTicketCount represents the count of tickets created on a given date.
type DailyTicketCount struct {
	Date  time.Time
	Count int
}

type TicketRepo struct {
	store *Store
}

func (r *TicketRepo) Create(ctx context.Context, ticket *model.Ticket) error {
	return r.store.db.WithContext(ctx).Create(ticket).Error
}

func (r *TicketRepo) GetByID(ctx context.Context, id uint) (model.Ticket, error) {
	var ticket model.Ticket
	err := r.store.db.WithContext(ctx).Unscoped().
		Preload("Project").
		Preload("Reporter").
		Preload("Assignee").
		First(&ticket, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Ticket{}, fmt.Errorf("ticket(id=%d): %w", id, common.ErrNotFound)
		}
		return model.Ticket{}, err
	}
	return ticket, nil
}

func (r *TicketRepo) UpdateFields(ctx context.Context, id uint, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	result := r.store.db.WithContext(ctx).Model(&model.Ticket{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ticket(id=%d): %w", id, common.ErrNotFound)
	}
	return nil
}

func (r *TicketRepo) GetByIDs(ctx context.Context, ids []uint) ([]model.Ticket, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tickets []model.Ticket
	err := r.store.db.WithContext(ctx).
		Unscoped().
		Preload("Project").
		Preload("Reporter").
		Preload("Assignee").
		Where("id IN ?", ids).
		Find(&tickets).Error
	if err != nil {
		return nil, err
	}
	return tickets, nil
}

func (r *TicketRepo) SoftDeleteByIDs(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.Ticket{}).Error
}

func (r *TicketRepo) HardDeleteByIDs(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Unscoped().Where("id IN ?", ids).Delete(&model.Ticket{}).Error
}

func (r *TicketRepo) List(ctx context.Context, filter TicketFilter) ([]model.Ticket, int64, error) {
	var tickets []model.Ticket
	var totalCount int64

	query := r.store.db.WithContext(ctx).Model(&model.Ticket{})

	// 应用过滤条件
	query = r.applyFilter(query, filter)

	// 计算总数
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	// 预加载关联数据（避免 N+1）
	query = query.Preload("Project").
		Preload("Reporter").
		Preload("Assignee")

	// 排序和分页
	query = query.Order("sort_order ASC")

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query = query.Limit(limit)
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}

	if err := query.Find(&tickets).Error; err != nil {
		return nil, 0, err
	}

	return tickets, totalCount, nil
}

func (r *TicketRepo) applyFilter(query *gorm.DB, filter TicketFilter) *gorm.DB {
	if filter.ProjectID != nil {
		query = query.Where("project_id = ?", *filter.ProjectID)
	}
	if filter.Type != nil {
		query = query.Where("type = ?", *filter.Type)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.Priority != nil {
		query = query.Where("priority = ?", *filter.Priority)
	}
	if filter.ReporterID != nil {
		query = query.Where("reporter_id = ?", *filter.ReporterID)
	}
	if filter.AssigneeID != nil {
		query = query.Where("assignee_id = ?", *filter.AssigneeID)
	}

	// 默认不包含已删除（GORM 自动加 WHERE deleted_at IS NULL）
	if filter.IncludeDeleted {
		query = query.Unscoped()
	}

	return query
}

func (r *TicketRepo) CountByProject(ctx context.Context, projectID uint, includeDeleted bool) (int64, error) {
	var count int64
	query := r.store.db.WithContext(ctx).Model(&model.Ticket{}).Where("project_id = ?", projectID)
	if includeDeleted {
		query = query.Unscoped()
	}
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *TicketRepo) CountByStatus(ctx context.Context, projectID uint) (map[model.TicketStatus]int, error) {
	type statusCount struct {
		Status model.TicketStatus
		Count  int
	}

	query := r.store.db.WithContext(ctx).Model(&model.Ticket{}).
		Select("status, COUNT(*) as count")

	if projectID != 0 {
		query = query.Where("project_id = ?", projectID)
	}

	var results []statusCount
	if err := query.Group("status").Scan(&results).Error; err != nil {
		return nil, err
	}

	counts := make(map[model.TicketStatus]int)
	for _, r := range results {
		counts[r.Status] = r.Count
	}
	return counts, nil
}

func (r *TicketRepo) CountByDateRange(ctx context.Context, projectID uint, start, end time.Time) ([]DailyTicketCount, error) {
	type row struct {
		DateStr string `gorm:"column:date_str"`
		Count   int    `gorm:"column:count"`
	}

	query := r.store.db.WithContext(ctx).Model(&model.Ticket{}).
		Select("DATE(created_at) as date_str, COUNT(*) as count").
		Where("created_at BETWEEN ? AND ?", start, end)

	if projectID != 0 {
		query = query.Where("project_id = ?", projectID)
	}

	var rows []row
	if err := query.Group("DATE(created_at)").Order("DATE(created_at) ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]DailyTicketCount, 0, len(rows))
	for _, r := range rows {
		parsed, err := time.Parse("2006-01-02", r.DateStr)
		if err != nil {
			continue
		}
		result = append(result, DailyTicketCount{Date: parsed, Count: r.Count})
	}
	return result, nil
}

func (r *TicketRepo) GetMaxSortOrderInStatus(ctx context.Context, projectID uint, status model.TicketStatus) (float64, error) {
	var maxSort float64
	err := r.store.db.WithContext(ctx).Model(&model.Ticket{}).
		Where("project_id = ? AND status = ?", projectID, status).
		Select("COALESCE(MAX(sort_order), 0)").
		Scan(&maxSort).Error
	return maxSort, err
}
