package system

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader is the settings subset system.Service reads for status snapshots.
type SettingsReader interface {
	GetPlatformSettings(ctx context.Context) (schema.PlatformSettings, error)
}
