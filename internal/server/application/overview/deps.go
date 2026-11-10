package overview

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader is the settings subset overview.Service aggregates.
type SettingsReader interface {
	GetAgentSettings(ctx context.Context) (schema.AgentSettings, error)
	GetStorageSettings(ctx context.Context) (schema.StorageSettings, error)
	GetPlatformSettings(ctx context.Context) (schema.PlatformSettings, error)
	GetOverviewSettings(ctx context.Context) (schema.OverviewSettings, error)
	GetAlertSettings(ctx context.Context) (schema.AlertSettings, error)
}
