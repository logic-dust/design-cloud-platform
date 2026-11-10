package session

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SettingsReader is the settings subset session.Service needs for login and portal flags.
type SettingsReader interface {
	GetAuthSettings(ctx context.Context) (schema.AuthSettings, error)
	GetPlatformSettings(ctx context.Context) (schema.PlatformSettings, error)
	GetFileSettings(ctx context.Context) (schema.FileSettings, error)
}

// ProxyConnectionRevoker closes all proxy connections for a requester (wired from proxy in container).
type ProxyConnectionRevoker func(requester string)
