package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
)

type LicenseRepo struct {
	store *Store
}

func (r *LicenseRepo) Save(ctx context.Context, licService *model.LicenseService) error {
	if licService.ID == 0 {
		return r.store.db.WithContext(ctx).Create(licService).Error
	}
	// 使用 Select 指定更新字段，避免 Save 全量更新
	return r.store.db.WithContext(ctx).Model(licService).Select("*").Updates(licService).Error
}

func (r *LicenseRepo) Get(ctx context.Context, id uint) (model.LicenseService, error) {
	var licService model.LicenseService
	if err := r.store.db.WithContext(ctx).First(&licService, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.LicenseService{}, fmt.Errorf("license service %d: %w", id, contract.ErrNotFound)
		}
		return model.LicenseService{}, err
	}
	return licService, nil
}

func (r *LicenseRepo) List(ctx context.Context) ([]model.LicenseService, error) {
	var licServices []model.LicenseService
	if err := r.store.db.WithContext(ctx).Order("id").Find(&licServices).Error; err != nil {
		return nil, err
	}
	return licServices, nil
}

func (r *LicenseRepo) ListPage(ctx context.Context, limit, offset int, enabledOnly bool) ([]model.LicenseService, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	query := r.store.db.WithContext(ctx).Model(&model.LicenseService{})
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var licServices []model.LicenseService
	if err := query.Order("id ASC").Limit(limit).Offset(offset).Find(&licServices).Error; err != nil {
		return nil, 0, err
	}
	return licServices, total, nil
}

func (r *LicenseRepo) Delete(ctx context.Context, id uint) error {
	db := r.store.db.WithContext(ctx).Delete(&model.LicenseService{}, id)
	if db.Error != nil {
		return db.Error
	}
	if db.RowsAffected == 0 {
		return contract.ErrNotFound
	}
	return nil
}
