package executor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/execution"
)

// ============================================================================
// Service Implementation
// ============================================================================

// Service represents the unified execution service that routes requests
// to the appropriate executor based on the protocol action catalog.
// It implements execution.Executor.
type Service struct {
	logger       *slog.Logger
	launcherExec execution.Executor
	directExec   execution.Executor
}

// Execute executes a command through the appropriate executor based on Registry
func (s *Service) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	// Validate request using unified validation logic
	if err := req.Validate(); err != nil {
		s.logger.Error("Invalid execution request", "error", err)
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   err.Error(),
		}, nil
	}

	// Set timeout if specified
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	spec, ok := execution.Actions[req.ActionName]
	if !ok {
		s.logger.Error("Unsupported action", "action_name", req.ActionName)
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   fmt.Sprintf("action not supported on current platform: %s", req.ActionName),
		}, nil
	}

	// Route to appropriate executor
	if spec.IsDirect() {
		return s.directExec.Execute(ctx, req)
	}

	if s.launcherExec == nil {
		s.logger.Error("Action requires launcher which is not available", "action_name", req.ActionName)
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   fmt.Sprintf("action requires launcher which is not configured: %s", req.ActionName),
		}, nil
	}

	return s.launcherExec.Execute(ctx, req)
}
