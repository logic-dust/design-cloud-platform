package executor

import (
	"log/slog"

	"design-cloud-engine/internal/execution"
)

// NewService creates a service with only the direct executor for Windows.
func NewService(directExec execution.Executor, logger *slog.Logger) *Service {
	return &Service{
		directExec: directExec,
		logger:     logger,
	}
}
