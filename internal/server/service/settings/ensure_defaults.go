package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings/schema"

	"gorm.io/gorm"
)

// EnsureDefaults seeds default runtime settings into the database for all known domains.
// It only creates missing rows; settings are a fresh contract and are never migrated.
func EnsureDefaults(store *db.Store) error {
	for _, domain := range schema.AllDomains() {
		settings, ok := schema.DefaultSettings(domain)
		if !ok {
			continue
		}
		jsonBytes, err := json.Marshal(settings)
		if err != nil {
			return fmt.Errorf("marshal default %s settings: %w", domain.String(), err)
		}

		var compactBuf bytes.Buffer
		if err := json.Compact(&compactBuf, jsonBytes); err == nil {
			jsonBytes = compactBuf.Bytes()
		}

		// 默认值必须通过该域自己的校验：它是唯一绕过写入校验直接落库的路径，
		// 而读取侧又会在校验失败时回退到默认值——若默认值非法，等于"回退到脏数据"，
		// 且管理员无法通过任何 PUT 修复（写入会被同一套校验拒绝）。此处 fail-fast。
		if _, err := schema.DecodeForStore(domain, jsonBytes); err != nil {
			return fmt.Errorf("invalid defaults for %s: %w", domain.String(), err)
		}

		setting := model.Setting{
			Domain: domain.String(),
			Value:  string(jsonBytes),
		}

		var existing model.Setting
		result := store.DB().Where("domain = ?", domain.String()).First(&existing)
		switch {
		case errors.Is(result.Error, gorm.ErrRecordNotFound):
			if err := store.DB().Create(&setting).Error; err != nil {
				return fmt.Errorf("create default %s settings: %w", domain.String(), err)
			}
		case result.Error != nil:
			return fmt.Errorf("query default %s settings: %w", domain.String(), result.Error)
		}
	}
	return nil
}
