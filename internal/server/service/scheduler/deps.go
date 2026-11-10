package scheduler

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader loads scheduler-domain runtime settings.
type SettingsReader interface {
	GetSchedulerSettings(ctx context.Context) (schema.SchedulerSettings, error)
}
