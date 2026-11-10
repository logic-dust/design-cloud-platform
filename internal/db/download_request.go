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

type DownloadRequestRepo struct {
	store *Store
}

func (r *DownloadRequestRepo) Create(ctx context.Context, download *model.DownloadRequest) error {
	return r.store.db.WithContext(ctx).Create(download).Error
}

func (r *DownloadRequestRepo) GetByID(ctx context.Context, id uint) (model.DownloadRequest, error) {
	var downloadRequest model.DownloadRequest
	if err := r.store.db.WithContext(ctx).First(&downloadRequest, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.DownloadRequest{}, fmt.Errorf("download request %d: %w", id, contract.ErrNotFound)
		}
		return model.DownloadRequest{}, err
	}
	return downloadRequest, nil
}

func (r *DownloadRequestRepo) List(ctx context.Context, status model.DownloadRequestStatus, requester string, limit int) ([]model.DownloadRequest, error) {
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	var downloads []model.DownloadRequest
	query := r.store.db.WithContext(ctx).Model(&model.DownloadRequest{})
	if status != 0 {
		query = query.Where("status = ?", status)
	}
	if requester != "" {
		query = query.Where("requester = ?", requester)
	}
	if err := query.Order("created_at DESC").Limit(limit).Find(&downloads).Error; err != nil {
		return nil, err
	}
	return downloads, nil
}

func (r *DownloadRequestRepo) Save(ctx context.Context, download *model.DownloadRequest) error {
	return r.store.db.WithContext(ctx).Save(download).Error
}

// ApprovePending atomically transitions a pending download request to approved.
func (r *DownloadRequestRepo) ApprovePending(ctx context.Context, id uint, approver string, reviewedAt, expiresAt time.Time) (bool, error) {
	result := r.store.db.WithContext(ctx).Model(&model.DownloadRequest{}).
		Where("id = ? AND status = ?", id, model.DownloadRequestStatusPending).
		Updates(map[string]any{
			"status":      model.DownloadRequestStatusApproved,
			"approver":    approver,
			"reviewed_at": &reviewedAt,
			"expires_at":  &expiresAt,
			"reason":      "",
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// RejectPending atomically transitions a pending download request to rejected.
func (r *DownloadRequestRepo) RejectPending(ctx context.Context, id uint, approver string, reviewedAt time.Time, reason string) (bool, error) {
	result := r.store.db.WithContext(ctx).Model(&model.DownloadRequest{}).
		Where("id = ? AND status = ?", id, model.DownloadRequestStatusPending).
		Updates(map[string]any{
			"status":      model.DownloadRequestStatusRejected,
			"approver":    approver,
			"reviewed_at": &reviewedAt,
			"expires_at":  nil,
			"reason":      reason,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
