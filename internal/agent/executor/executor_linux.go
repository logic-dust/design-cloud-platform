package executor

import (
	"log/slog"

	"design-cloud-engine/internal/execution"
)

// NewService creates a service with both launcher and direct executors for Linux.
func NewService(launcherExec, directExec execution.Executor, logger *slog.Logger) *Service {
	return &Service{
		launcherExec: launcherExec,
		directExec:   directExec,
		logger:       logger,
	}
}
