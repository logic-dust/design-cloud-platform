package alert

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader loads global and per-node effective alert rules.
type SettingsReader interface {
	GetAlertSettings(ctx context.Context) (schema.AlertSettings, error)
	GetEffectiveAlertSettingsForNode(ctx context.Context, nodeID string) (schema.AlertSettings, error)
}
