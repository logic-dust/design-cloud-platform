package proxy

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader loads connection-domain runtime settings (including nested proxy).
type SettingsReader interface {
	GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error)
}
