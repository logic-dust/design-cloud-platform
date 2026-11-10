package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
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
			return model.ExecutionRecord{}, fmt.Errorf("execution record (executionID=%s): %w", executionID, contract.ErrNotFound)
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
		if IsUniqueConstraintError(err) {
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

func (r *ExecutionRepo) List(ctx context.Context, f model.ExecutionListFilter) ([]model.ExecutionRecord, int64, error) {
	limit := f.Limit
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	limit = CapListLimit(limit)

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
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(f.Offset).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// PruneTerminal 批量删除 created_at 早于 before 的终态执行记录，单次最多 limit 条。
// 只删除终态：pending/dispatched 记录可能仍有在途请求，删除会让幂等回查误判为不存在。
func (r *ExecutionRepo) PruneTerminal(ctx context.Context, before time.Time, limit int) (int64, error) {
	if err := validateLimit(limit); err != nil {
		return 0, err
	}
	terminal := []model.ExecutionStatus{
		model.ExecutionStatusSucceeded,
		model.ExecutionStatusFailed,
		model.ExecutionStatusTimedOut,
		model.ExecutionStatusCancelled,
	}

	var ids []uint
	if err := r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{}).
		Where("status IN ? AND created_at < ?", terminal, before).
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := r.store.db.WithContext(ctx).Delete(&model.ExecutionRecord{}, ids)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// abandonedNonTerminalError is written when a pending/dispatched row is closed
// because it outlived the staleness window. The row is kept so a retry of the
// same execution_id replays a terminal failure instead of dispatching again.
const abandonedNonTerminalError = "abandoned: non-terminal record exceeded staleness window"

// ExpireStale marks pending/dispatched rows whose updated_at is before the
// cutoff as timed out. It does not delete them: deletion would make an
// idempotent retry look like the command never ran.
func (r *ExecutionRepo) ExpireStale(ctx context.Context, before time.Time, limit int) (int64, error) {
	if err := validateLimit(limit); err != nil {
		return 0, err
	}
	nonTerminal := []model.ExecutionStatus{
		model.ExecutionStatusPending,
		model.ExecutionStatusDispatched,
	}

	var ids []uint
	if err := r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{}).
		Where("status IN ? AND updated_at < ?", nonTerminal, before).
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	now := time.Now()
	result := r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{}).
		Where("id IN ? AND status IN ?", ids, nonTerminal).
		Updates(map[string]any{
			"status":       model.ExecutionStatusTimedOut,
			"system_error": abandonedNonTerminalError,
			"finished_at":  now,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// MarkTerminal transitions a pending/dispatched record to a terminal status.
// Success is derived, not stored: is_success ⇔ status == succeeded.
func (r *ExecutionRepo) MarkTerminal(ctx context.Context, traceID string, status model.ExecutionStatus, exitCode int, systemError string) (bool, error) {
	result := r.store.db.WithContext(ctx).Model(&model.ExecutionRecord{}).
		Where("trace_id = ? AND status IN ?", traceID, []model.ExecutionStatus{
			model.ExecutionStatusPending,
			model.ExecutionStatusDispatched,
		}).
		Updates(map[string]any{
			"status":       status,
			"exit_code":    exitCode,
			"system_error": systemError,
			"finished_at":  time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
