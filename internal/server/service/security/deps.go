package security

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsService is the settings surface security.Service depends on.
type SettingsService interface {
	GetAuthSettings(ctx context.Context) (schema.AuthSettings, error)
	Subscribe(fn func(ctx context.Context) error)
}
