//go:build !dev

package main

import (
	"log/slog"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/config"
)

// setupServerDevEnv is a no-op stub for production builds.
func setupServerDevEnv(_ *config.Config, _ *db.Store, _ *slog.Logger) error {
	return nil
}
