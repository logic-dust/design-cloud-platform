package desktop

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader loads desktop release settings.
type SettingsReader interface {
	GetDesktopSettings(ctx context.Context) (schema.DesktopSettings, error)
}
