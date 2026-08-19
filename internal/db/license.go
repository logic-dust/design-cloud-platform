package db

import (
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

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
			return model.LicenseService{}, fmt.Errorf("license service %d: %w", id, common.ErrNotFound)
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

func (r *LicenseRepo) Delete(ctx context.Context, id uint) error {
	db := r.store.db.WithContext(ctx).Delete(&model.LicenseService{}, id)
	if db.Error != nil {
		return db.Error
	}
	if db.RowsAffected == 0 {
		return common.ErrNotFound
	}
	return nil
}
