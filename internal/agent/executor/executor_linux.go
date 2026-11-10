package executor

import (
	"log/slog"

	"design-cloud-engine/internal/execution"
)

// NewService creates a service with both PrivilegedExec and direct executors for Linux.
func NewService(dcexecExec, directExec execution.Executor, logger *slog.Logger) *Service {
	return &Service{
		dcexecExec: dcexecExec,
		directExec: directExec,
		logger:     logger,
	}
}
