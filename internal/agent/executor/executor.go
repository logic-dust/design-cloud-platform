package executor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
)

// ============================================================================
// Service Implementation
// ============================================================================

// Service represents the unified execution service that routes requests
// to the appropriate executor based on the protocol action catalog.
// It implements execution.Executor.
type Service struct {
	logger     *slog.Logger
	dcexecExec execution.Executor
	directExec execution.Executor

	// schedulerEnvScript returns this node's effective scheduler environment script
	// (settings.scheduler.envScript merged with the node override). Nil or empty
	// means job commands run without sourcing a script.
	schedulerEnvScript func() string
}

// SetSchedulerEnvScript installs the provider of the node-local scheduler environment script.
func (s *Service) SetSchedulerEnvScript(fn func() string) {
	s.schedulerEnvScript = fn
}

// injectJobEnvScript stamps the node-local environment script into scheduler job payloads.
// The server never sets it: the script path is resolved per node, after dispatch.
func (s *Service) injectJobEnvScript(req *execution.ExecuteRequest) error {
	if req.Payload == nil {
		return nil // handlers reject a missing payload themselves
	}
	if s.schedulerEnvScript == nil {
		return nil
	}
	script := s.schedulerEnvScript()
	if script == "" {
		return nil
	}
	switch req.ActionName {
	case execution.ActionJobQuery:
		p, err := execution.GetPayload[payload.JobQuery](req)
		if err != nil {
			return err
		}
		p.EnvScript = script
		req.Payload = p
	case execution.ActionJobSubmit:
		p, err := execution.GetPayload[payload.JobSubmit](req)
		if err != nil {
			return err
		}
		p.EnvScript = script
		req.Payload = p
	case execution.ActionJobControl:
		p, err := execution.GetPayload[payload.JobControl](req)
		if err != nil {
			return err
		}
		p.EnvScript = script
		req.Payload = p
	case execution.ActionJobModify:
		p, err := execution.GetPayload[payload.JobModify](req)
		if err != nil {
			return err
		}
		p.EnvScript = script
		req.Payload = p
	}
	return nil
}

// Execute executes a command through the appropriate executor based on Registry
func (s *Service) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	// Validate request using unified validation logic
	if err := req.Validate(); err != nil {
		s.logger.Error("Invalid execution request", "error", err)
		traceID := ""
		if req != nil {
			traceID = req.TraceID
		}
		return &execution.ExecuteResponse{
			TraceID:  traceID,
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

	if err := s.injectJobEnvScript(req); err != nil {
		s.logger.Error("Failed to inject scheduler env script", "action_name", req.ActionName, "error", err)
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   err.Error(),
		}, nil
	}

	// Route to appropriate executor
	if spec.IsDirect() {
		return s.directExec.Execute(ctx, req)
	}

	if s.dcexecExec == nil {
		s.logger.Error("Action requires PrivilegedExec which is not available", "action_name", req.ActionName)
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   fmt.Sprintf("action requires PrivilegedExec which is not configured: %s", req.ActionName),
		}, nil
	}

	return s.dcexecExec.Execute(ctx, req)
}
