package license

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader loads license-domain runtime settings.
type SettingsReader interface {
	GetLicenseSettings(ctx context.Context) (schema.LicenseSettings, error)
}
