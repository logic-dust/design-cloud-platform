package user

import (
	"context"

	"design-cloud-engine/internal/contract/schema"
)

// SessionRevoker evicts portal sessions for a username.
// Implemented by session.Evictor, which user and session services share; the
// interface is declared here so the user package never imports the session
// package and the construction order stays a DAG.
type SessionRevoker interface {
	InvalidateByUsername(username string)
	Delete(ctx context.Context, username string) error
}

// AuthSettingsReader loads auth-domain runtime settings for sync and ID mapping.
type AuthSettingsReader interface {
	GetAuthSettings(ctx context.Context) (schema.AuthSettings, error)
}
