package direct

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os/exec"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/process"
)

// Config holds configuration for the direct executor. It has no platform-specific
// fields and is shared across all build targets.
type Config struct {
	GetLmutilPath func() string
}

// Executor implements execution.Executor for non-privileged operations
// that can be executed directly (job scripts, license commands, read-only user actions)
type Executor struct {
	logger *slog.Logger
	cfg    Config
}

// NewExecutor creates a new direct Executor
func NewExecutor(cfg Config, logger *slog.Logger) *Executor {
	return &Executor{logger: logger, cfg: cfg}
}

// Execute routes the request to the appropriate direct execution function based on actionName
func (e *Executor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	startTime := time.Now()

	e.logger.Debug("Executing via DirectExecutor",
		"trace_id", req.TraceID,
		"execution_id", req.ExecutionID,
		"action_type", req.ActionName)

	var resp *execution.ExecuteResponse
	var err error

	switch req.ActionName {
	case execution.ActionJobQuery:
		resp, err = executeJobQuery(ctx, req)

	// License actions
	case execution.ActionLicenseStatus, execution.ActionLicenseFeatures:
		resp, err = e.executeLicenseCommand(ctx, req)

	// User/Group actions (read-only)
	case execution.ActionUserList:
		resp, err = executeAction(ctx, req, handleUserList)
	case execution.ActionUserGet:
		resp, err = executeAction(ctx, req, handleUserGet)
	case execution.ActionGroupList:
		resp, err = executeAction(ctx, req, handleGroupList)
	case execution.ActionGroupGet:
		resp, err = executeAction(ctx, req, handleGroupGet)

	case execution.ActionSessionControl:
		resp, err = executeAction(ctx, req, handleSessionControl)

	default:
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   fmt.Sprintf("DirectExecutor: unsupported action: %s", req.ActionName),
		}, nil
	}

	if err != nil {
		e.logger.Error("DirectExecutor execution failed",
			"trace_id", req.TraceID,
			"execution_id", req.ExecutionID,
			"action_type", req.ActionName,
			"error", err)
		return &execution.ExecuteResponse{
			TraceID:  req.TraceID,
			Success:  false,
			ExitCode: 1,
			Stderr:   err.Error(),
			Duration: time.Since(startTime).Milliseconds(),
		}, nil
	}

	if resp != nil {
		resp.Duration = time.Since(startTime).Milliseconds()
	}

	return resp, nil
}

// ============================================================================
// Command Execution Utilities
// ============================================================================

// executeAction wraps direct in-process handlers into a standard execution response.
func executeAction(ctx context.Context, req *execution.ExecuteRequest, handler func(context.Context, *execution.ExecuteRequest) (string, error)) (*execution.ExecuteResponse, error) {
	output, err := handler(ctx, req)
	if err != nil {
		return nil, err
	}
	return &execution.ExecuteResponse{
		TraceID:  req.TraceID,
		Success:  true,
		ExitCode: 0,
		Stdout:   output,
	}, nil
}

// executeCommand encapsulates the common logic for running a command and returning a standardized execution response.
func executeCommand(ctx context.Context, req *execution.ExecuteRequest, name string, args ...string) (*execution.ExecuteResponse, error) {
	return executeCommandWithDir(ctx, req, "", name, args...)
}

func executeJobQuery(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	p, err := execution.GetPayload[payload.JobQuery](req)
	if err != nil {
		return nil, fmt.Errorf("failed to parse job query payload: %w", err)
	}
	if p.Command == nil {
		return nil, fmt.Errorf("job query command is required")
	}
	if err := p.Command.Validate(); err != nil {
		return nil, fmt.Errorf("invalid job query command: %w", err)
	}

	reqEnv := make(map[string]string)
	maps.Copy(reqEnv, req.Env)
	for k, v := range p.Env {
		if !execution.IsValidEnvKey(k) || execution.IsBlockedEnvKey(k) {
			continue
		}
		reqEnv[k] = v
	}

	jobReq := *req
	jobReq.Env = reqEnv

	spec := p.Command
	if p.EnvScript != "" {
		args := append([]string{"-c", ". \"$0\" && exec \"$@\"", p.EnvScript, spec.Program}, spec.Args...)
		return executeCommandWithDir(ctx, &jobReq, spec.WorkDir, "bash", args...)
	}
	return executeCommandWithDir(ctx, &jobReq, spec.WorkDir, spec.Program, spec.Args...)
}

func executeCommandWithDir(ctx context.Context, req *execution.ExecuteRequest, dir, name string, args ...string) (*execution.ExecuteResponse, error) {
	startTime := time.Now()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = execution.BuildCleanEnv(req.Env)

	var stdout process.BoundedBuffer
	var stderr process.BoundedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(startTime)

	exitCode := 0
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	resp := &execution.ExecuteResponse{
		TraceID:  req.TraceID,
		Success:  err == nil,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration.Milliseconds(),
	}
	if err != nil {
		resp.Stderr = cmp.Or(resp.Stderr, err.Error())
	}

	return resp, nil
}
