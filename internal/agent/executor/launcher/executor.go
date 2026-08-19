//go:build !windows

package launcher

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/execution"
)

// AuditHook reports a privileged execution outcome to the audit subsystem.
// It is injected by the caller so the launcher executor stays free of a direct
// dependency on the audit package (the audit write is a cross-cutting concern
// owned by the upper layers, not by the execution adapter).
type AuditHook func(ctx context.Context, actionName string, result string, targetUser string, traceID string, level slog.Level, details map[string]any, duration time.Duration)

// launcherMaxStdoutBytes is the upper bound on launcher stdout.
// Launcher stdout carries a JSON protocol response; if the output exceeds this
// limit the JSON cannot be parsed reliably, so we return an explicit error
// rather than attempting to parse a truncated payload.
const launcherMaxStdoutBytes = 16 * 1024 * 1024 // 16 MiB

// Executor implements execution.Executor using the dc-launcher binary for privileged operations
type Executor struct {
	getLauncherPath func() string
	tokenFile       string
	token           string
	tokenOnce       sync.Once
	tokenErr        error
	logger          *slog.Logger
	auditHook       AuditHook
}

type Config struct {
	GetLauncherPath func() string
}

// NewExecutor creates a new Launcher Executor instance. auditHook may be nil to
// disable execution auditing (e.g. in tests or environments without audit).
func NewExecutor(cfg Config, logger *slog.Logger, auditHook AuditHook) *Executor {
	return &Executor{
		getLauncherPath: cfg.GetLauncherPath,
		tokenFile:       fs.LauncherTokenPath(),
		logger:          logger,
		auditHook:       auditHook,
	}
}

// Execute executes a privileged action through the Launcher
func (e *Executor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	startTime := time.Now()

	e.logger.Info("Executing privileged command via launcher",
		"trace_id", req.TraceID,
		"execution_id", req.ExecutionID,
		"action_type", req.ActionName,
		"target_user", req.TargetUser)

	// Execute once (no retry)
	result, err := e.executeOnce(ctx, req)
	return e.handleExecutionResult(ctx, req, result, err, startTime)
}

// executeOnce performs a single execution attempt
func (e *Executor) executeOnce(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	// Lazy load token if needed (sync.Once ensures no data race under concurrent calls)
	if err := e.ensureToken(); err != nil {
		return nil, fmt.Errorf("launcher token not available: %w", err)
	}

	input := execution.ExecuteInput{
		Version: config.VERSION,
		Token:   e.token,
		Request: req,
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	stdout := common.NewBoundedBuffer(launcherMaxStdoutBytes)
	stderr := common.NewBoundedBuffer(common.DefaultMaxCommandOutputBytes)
	launcherBin := e.getLauncherPath()
	if launcherBin == "" {
		return nil, fmt.Errorf("launcher path is not configured")
	}
	cmd := exec.CommandContext(ctx, launcherBin)
	cmd.Env = execution.BuildLauncherEnv(nil)
	cmd.Stdin = bytes.NewReader(append(inputJSON, '\n'))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second

	// Execute
	runErr := cmd.Run()
	if errors.Is(runErr, exec.ErrWaitDelay) {
		e.logger.Warn("Launcher stdio pipes did not close promptly after process exit",
			"trace_id", req.TraceID,
			"execution_id", req.ExecutionID,
			"action_type", req.ActionName)
		runErr = nil
	}
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, launcherContextError(ctxErr, stdout.Bytes(), stderr.Bytes())
		}
		if execution.ExitErrorSignaled(runErr, syscall.SIGSYS) {
			return nil, fmt.Errorf("launcher killed by seccomp: blocked system call (SIGSYS)")
		}
	}

	// Stdout carries the JSON protocol response. If it was truncated the payload
	// cannot be parsed reliably, so we return an explicit error.
	if stdout.Truncated() {
		return nil, fmt.Errorf("launcher output exceeded protocol limit (%d bytes)", launcherMaxStdoutBytes)
	}

	// Determine exit code
	exitCode := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		exitCode = exitErr.ExitCode()
	} else if runErr != nil {
		// Non-ExitError implies execution failure (not just non-zero exit code)
		return nil, fmt.Errorf("failed to run launcher: %w", runErr)
	}

	// Parse Output — append truncation marker to stderr if it was cut off so that
	// the caller can see the boundary in logs / audit records.
	stderrBytes := stderr.Bytes()
	if stderr.Truncated() {
		suffix := fmt.Sprintf("\n[stderr truncated: exceeded %d bytes]", common.DefaultMaxCommandOutputBytes)
		stderrBytes = append(stderrBytes, []byte(suffix)...)
	}
	return e.parseLauncherOutput(stdout.Bytes(), stderrBytes, exitCode)
}

func launcherContextError(ctxErr error, stdout, stderr []byte) error {
	msg := cmp.Or(strings.TrimSpace(string(stderr)), strings.TrimSpace(string(stdout)))
	if msg == "" {
		return fmt.Errorf("launcher execution interrupted: %w", ctxErr)
	}
	return fmt.Errorf("launcher execution interrupted: %w (output: %s)", ctxErr, msg)
}

// ensureToken loads the launcher token exactly once, safe for concurrent callers.
func (e *Executor) ensureToken() error {
	e.tokenOnce.Do(func() {
		e.tokenErr = e.loadToken()
	})
	return e.tokenErr
}

// loadToken loads the launcher token from file (private, for internal use only)
func (e *Executor) loadToken() error {
	data, err := os.ReadFile(e.tokenFile)
	if err != nil {
		return fmt.Errorf("failed to read token file: %w", err)
	}

	e.token = strings.TrimSpace(string(data))
	if e.token == "" {
		return fmt.Errorf("token file %s is empty", e.tokenFile)
	}

	return nil
}

func (e *Executor) parseLauncherOutput(stdout, stderr []byte, exitCode int) (*execution.ExecuteResponse, error) {
	var resp execution.ExecuteOutput

	// 1. Try to parse JSON output
	if err := json.Unmarshal(stdout, &resp); err != nil {
		// Log protocol violation - dc-launcher binary uses panic recovery internally,
		// so invalid JSON output here is a serious anomaly indicating a crash or corruption.
		rawStdout := strings.TrimSpace(string(stdout))
		rawStderr := strings.TrimSpace(string(stderr))

		e.logger.Error("Launcher protocol violation: invalid JSON output",
			"exitCode", exitCode,
			"stdout", rawStdout,
			"stderr", rawStderr,
			"err", err)

		errMsg := cmp.Or(rawStdout, rawStderr, "no output")
		return nil, fmt.Errorf("launcher protocol error: invalid JSON response (output: %s)", errMsg)
	}

	// 2. Map response to ExecuteResponse object
	res := &execution.ExecuteResponse{
		Success:  resp.Success,
		ExitCode: exitCode,
		Stdout:   resp.Output,
		Stderr:   resp.Error,
	}

	// 3. Ensure error information is captured if execution failed
	if !res.Success {
		res.Stderr = cmp.Or(
			res.Stderr,
			strings.TrimSpace(string(stderr)),
			fmt.Sprintf("execution failed (exit code %d)", exitCode),
		)
	}

	return res, nil
}

func (e *Executor) handleExecutionResult(ctx context.Context, req *execution.ExecuteRequest, response *execution.ExecuteResponse, err error, startTime time.Time) (*execution.ExecuteResponse, error) {
	executionDuration := time.Since(startTime)

	if err != nil {
		e.logger.Error("Privileged command execution failed", "error", err, "trace_id", req.TraceID)

		// Log execution failure to audit
		e.logExecution(ctx, req, "failed", slog.LevelError, executionDuration, map[string]any{
			"error":         err.Error(),
			"failure_stage": inferLauncherFailureStage(err.Error()),
			"launcher_path": e.getLauncherPath(),
		})

		return &execution.ExecuteResponse{
			ExecutionID: req.ExecutionID,
			TraceID:     req.TraceID,
			Success:     false,
			ExitCode:    1,
			Duration:    executionDuration.Milliseconds(),
			Stderr:      err.Error(),
		}, nil
	}

	response.ExecutionID = req.ExecutionID
	response.TraceID = req.TraceID
	response.Duration = executionDuration.Milliseconds()

	if response.Success {
		e.logger.Info("Privileged command executed successfully",
			"trace_id", req.TraceID,
			"duration_ms", response.Duration)

		// Log successful execution to audit
		e.logExecution(ctx, req, "success", slog.LevelInfo, executionDuration, map[string]any{
			"exit_code": response.ExitCode,
		})
	} else {
		e.logger.Error("Privileged command execution failed",
			"trace_id", req.TraceID,
			"exit_code", response.ExitCode,
			"error", response.Stderr,
			"output", response.Stdout)

		// Log execution failure to audit
		e.logExecution(ctx, req, "failed", slog.LevelWarn, executionDuration, map[string]any{
			"exit_code":     response.ExitCode,
			"error":         response.Stderr,
			"failure_stage": inferLauncherFailureStage(response.Stderr),
			"launcher_path": e.getLauncherPath(),
		})
	}

	return response, nil
}

func (e *Executor) logExecution(ctx context.Context, req *execution.ExecuteRequest, result string, level slog.Level, duration time.Duration, extra map[string]any) {
	if e.auditHook == nil {
		return
	}
	details := map[string]any{
		"trace_id":     req.TraceID,
		"execution_id": req.ExecutionID,
		"action_type":  req.ActionName,
		"target_user":  req.TargetUser,
		"method":       "launcher",
	}
	maps.Copy(details, launcherPayloadAuditFields(req))
	maps.Copy(details, extra)
	e.auditHook(ctx, string(req.ActionName), result, req.TargetUser, req.TraceID, level, details, duration)
}

func inferLauncherFailureStage(errMsg string) string {
	switch {
	case strings.Contains(errMsg, "launcher killed by seccomp"):
		return "seccomp"
	case strings.Contains(errMsg, "launcher protocol error:"):
		return "launcher_protocol"
	case strings.Contains(errMsg, "RuntimeEnvironment verification failed:"):
		return "runtime_verify"
	case strings.Contains(errMsg, "Token verification failed:"):
		return "token_verify"
	case strings.Contains(errMsg, "Action not allowed:"):
		return "action_verify"
	default:
		return "action_execute"
	}
}

func launcherPayloadAuditFields(req *execution.ExecuteRequest) map[string]any {
	if req == nil {
		return nil
	}

	switch req.ActionName {
	case execution.ActionFileList:
		p, err := execution.GetPayload[payload.FileList](req)
		if err == nil && p != nil && p.Dir != "" {
			return map[string]any{"dir": p.Dir}
		}
	case execution.ActionFileRead:
		p, err := execution.GetPayload[payload.FileRead](req)
		if err == nil && p != nil && p.Path != "" {
			return map[string]any{"path": p.Path}
		}
	case execution.ActionFileWriteCheck:
		p, err := execution.GetPayload[payload.FileWriteCheck](req)
		if err == nil && p != nil && p.Dir != "" {
			return map[string]any{"dir": p.Dir}
		}
	}

	return nil
}
