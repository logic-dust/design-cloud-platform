//go:build !dev

package main

import (
	"log/slog"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
)

// appLogPath returns the app log file path for production builds.
func appLogPath(cfg *config.Config) string {
	return cfg.AppLogPath()
}

// setupServerDevEnv is a no-op stub for production builds.
func setupServerDevEnv(_ *config.Config, _ *db.Store, _ *slog.Logger) error {
	return nil
}
