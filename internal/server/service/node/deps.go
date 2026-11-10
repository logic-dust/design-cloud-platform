package node

import (
	"context"

	"design-cloud-engine/internal/contract/schema"

	"gorm.io/gorm"
)

// ConnectionChecker reports whether an agent is connected (control registry).
type ConnectionChecker interface {
	IsConnected(nodeID string) bool
}

// SettingsCoordinator manages node override cache and cluster invalidation bumps.
// Implemented by settings.Service.
type SettingsCoordinator interface {
	BuildNodeEffectiveSettings(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (schema.NodeEffectiveSettings, error)
	InvalidateNodeOverride(nodeID string)
	BumpNodeOverridesTopicTx(ctx context.Context, tx *gorm.DB) error
	BumpNodeOverridesTopic(ctx context.Context) error
}
