//go:build !dev

package main

import (
	"log/slog"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
)

// setupServerDevEnv is a no-op stub for production builds.
func setupServerDevEnv(_ *config.Config, _ *db.Store, _ *slog.Logger) error {
	return nil
}
