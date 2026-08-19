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

var ErrExecutionActionExists = errors.New("execution action already exists")

type ExecutionRepo struct {
	store *Store
}

func (r *ExecutionRepo) GetByExecutionID(ctx context.Context, executionID string) (model.ExecutionRecord, error) {
	var record model.ExecutionRecord
	if err := r.store.db.WithContext(ctx).Where("execution_id = ?", executionID).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.ExecutionRecord{}, fmt.Errorf("execution record (executionID=%s): %w", executionID, common.ErrNotFound)
		}
		return model.ExecutionRecord{}, err
	}
	return record, nil
}

func (r *ExecutionRepo) Create(ctx context.Context, executionID, actionName, traceID, serverNode, targetNode string) error {
	record := &model.ExecutionRecord{
		ExecutionID: executionID,
		ActionName:  actionName,
		TraceID:     traceID,
		ServerNode:  serverNode,
		TargetNode:  targetNode,
		Status:      model.ExecutionStatusPending,
	}
	if err := r.store.db.WithContext(ctx).Create(record).Error; err != nil {
		if common.IsUniqueConstraintError(err) {
			return fmt.Errorf("%w: %s", ErrExecutionActionExists, executionID)
		}
		return err
	}
	return nil
}

func (r *ExecutionRepo) MarkDispatched(ctx context.Context, traceID string) error {
	return r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{}).
		Where("trace_id = ? AND status = ?", traceID, model.ExecutionStatusPending).
		Update("status", model.ExecutionStatusDispatched).Error
}

// ExecutionListFilter is the filter criteria for listing execution records.
type ExecutionListFilter struct {
	ServerNode string                 // empty = any
	ActionName string                 // empty = any
	Status     *model.ExecutionStatus // nil = any
	From       *time.Time             // nil = no lower bound on CreatedAt
	To         *time.Time             // nil = no upper bound on CreatedAt
	Limit      int                    // 0 = default 50, max 500
	Offset     int                    // 0 = start from the beginning
}

func (r *ExecutionRepo) List(ctx context.Context, f ExecutionListFilter) ([]model.ExecutionRecord, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)

	q := r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{})
	if f.ServerNode != "" {
		q = q.Where("server_node = ?", f.ServerNode)
	}
	if f.ActionName != "" {
		q = q.Where("action_name = ?", f.ActionName)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var records []model.ExecutionRecord
	if err := q.Order("created_at DESC").Limit(limit).Offset(f.Offset).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func (r *ExecutionRepo) MarkTerminal(ctx context.Context, traceID string, status model.ExecutionStatus, success bool, exitCode int, systemError string) (bool, error) {
	result := r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{}).
		Where("trace_id = ? AND status IN ?", traceID, []model.ExecutionStatus{
			model.ExecutionStatusPending,
			model.ExecutionStatusDispatched,
		}).
		Updates(map[string]any{
			"status":       status,
			"success":      success,
			"exit_code":    exitCode,
			"system_error": systemError,
			"finished_at":  time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
