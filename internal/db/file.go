package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

type FileRepo struct {
	store *Store
}

func (r *FileRepo) AddDownloadInfo(ctx context.Context, downloadFileInfo *model.Download) error {
	return r.store.db.WithContext(ctx).Create(downloadFileInfo).Error
}

func (r *FileRepo) GetDownloadByID(ctx context.Context, downloadID int) (model.Download, error) {
	var downloadRequest model.Download
	if err := r.store.db.WithContext(ctx).First(&downloadRequest, downloadID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Download{}, fmt.Errorf("download %d: %w", downloadID, common.ErrNotFound)
		}
		return model.Download{}, err
	}
	return downloadRequest, nil
}

func (r *FileRepo) ListDownloadRequests(ctx context.Context, status model.DownloadStatus, requester string) ([]model.Download, error) {
	var downloads []model.Download
	query := r.store.db.WithContext(ctx).Model(&model.Download{})
	if status != 0 {
		query = query.Where("status = ?", status)
	}
	if requester != "" {
		query = query.Where("requester = ?", requester)
	}
	if err := query.Order("created_at desc").Find(&downloads).Error; err != nil {
		return nil, err
	}
	return downloads, nil
}

func (r *FileRepo) SaveDownloadRequest(ctx context.Context, download *model.Download) error {
	return r.store.db.WithContext(ctx).Save(download).Error
}
