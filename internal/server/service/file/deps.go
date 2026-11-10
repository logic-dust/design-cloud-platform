package file

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader loads file-domain runtime settings.
type SettingsReader interface {
	GetFileSettings(ctx context.Context) (schema.FileSettings, error)
}
