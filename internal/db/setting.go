package db

import (
	"bytes"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"

	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

type SettingRepo struct {
	store *Store
}

// Load reads a settings row by domain string and unmarshals its JSON value into out.
// If the row does not exist, out is unchanged and no error is returned.
func (r *SettingRepo) Load(ctx context.Context, domain string, out any) error {
	var conf model.Setting
	result := r.store.db.WithContext(ctx).Where("domain = ?", domain).First(&conf)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil
	}
	if result.Error != nil {
		return fmt.Errorf("query %s settings: %w", domain, result.Error)
	}
	if err := json.Unmarshal([]byte(conf.Value), out); err != nil {
		return fmt.Errorf("unmarshal %s settings: %w", domain, err)
	}
	return nil
}

// Save persists settings as JSON and optionally executes afterSave callbacks
// within the same transaction (only when callbacks are provided).
func (r *SettingRepo) Save(ctx context.Context, domain string, settings any, afterSave ...func(tx *gorm.DB) error) error {
	jsonBytes, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal %s settings: %w", domain, err)
	}

	var compactBuf bytes.Buffer
	if err := json.Compact(&compactBuf, jsonBytes); err == nil {
		jsonBytes = compactBuf.Bytes()
	}

	upsert := func(tx *gorm.DB) error {
		return tx.
			Where(model.Setting{Domain: domain}).
			Assign(model.Setting{Value: string(jsonBytes)}).
			FirstOrCreate(&model.Setting{}).Error
	}

	// Only wrap in an explicit transaction when afterSave callbacks need to run
	// atomically with the upsert. Otherwise GORM's implicit per-statement tx is enough.
	if len(afterSave) == 0 {
		if err := upsert(r.store.db.WithContext(ctx)); err != nil {
			return fmt.Errorf("upsert %s settings: %w", domain, err)
		}
		return nil
	}

	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := upsert(tx); err != nil {
			return fmt.Errorf("upsert %s settings: %w", domain, err)
		}
		for _, fn := range afterSave {
			if err := fn(tx); err != nil {
				return err
			}
		}
		return nil
	})
}
