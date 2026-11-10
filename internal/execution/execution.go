package execution

import (
	"cmp"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/platform/crypto"
)

var (
	ErrExecutionRequestNil = errors.New("execution request is nil")
	ErrActionNameRequired  = errors.New("action_name is required")
	ErrTraceIDRequired     = errors.New("trace_id is required")
)

// ============================================================================
// Request & Response
// ============================================================================

// NodeAffinity controls how a node is selected from the candidate pool.
type NodeAffinity int

const (
	// NodeAffinityLocalPreferred selects the local node when it is in the
	// candidate pool; falls back to a random candidate otherwise.
	NodeAffinityLocalPreferred NodeAffinity = iota
	// NodeAffinityAny selects a random node from the candidate pool.
	NodeAffinityAny
)

// RunMode controls whether an action is executed synchronously or tracked
// as an asynchronous background task.
type RunMode int

const (
	// RunModeSync blocks until the command finishes and returns the final result.
	RunModeSync RunMode = iota
	// RunModeAsyncTracked returns once the command is accepted for dispatch and
	// persists task status in execution_records.
	RunModeAsyncTracked
)

// ExecuteRequest represents a command execution request
// This is a pure data container (POJO-like in Go)
type ExecuteRequest struct {
	TraceID      string            `json:"traceId"`
	ExecutionID  string            `json:"executionId"`
	ActionName   ActionName        `json:"actionName"`
	TargetUser   string            `json:"targetUser,omitempty"`
	Mode         RunMode           `json:"mode,omitempty"`
	TargetNode   string            `json:"targetNode,omitempty"`
	NodeSelector []string          `json:"nodeSelector,omitempty"`
	NodeAffinity NodeAffinity      `json:"nodeAffinity,omitempty"`
	Payload      any               `json:"payload,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Timeout      int               `json:"timeout,omitempty"`
}

// ExecuteResponse represents the result of command execution
type ExecuteResponse struct {
	ExecutionID string `json:"executionId,omitempty"`
	TraceID     string `json:"traceId,omitempty"`
	Accepted    bool   `json:"accepted,omitempty"`
	Success     bool   `json:"success"`
	ExitCode    int    `json:"exitCode"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	SystemError string `json:"systemError,omitempty"`
	Duration    int64  `json:"duration,omitempty"`
}

// Validate checks for required fields in the request
func (r *ExecuteRequest) Validate() error {
	if r == nil {
		return ErrExecutionRequestNil
	}
	if r.ActionName == "" {
		return ErrActionNameRequired
	}
	if r.TraceID == "" {
		return ErrTraceIDRequired
	}
	return nil
}

// NormalizePayload 把 JSON 解码得到的 Payload 收敛回 json.RawMessage。
//
// Payload 是 any，两种来源形态并不一致：
//   - 进程内构造（agent 直接调用）：类型化结构体或 json.RawMessage；
//   - JSON 往返（PrivilegedExec 从 stdin 解码）：payload 是 JSON 对象，必然解成 map[string]any。
//
// GetPayload 只接受类型化值、json.RawMessage、[]byte、string，故 JSON 解码得到的
// map[string]any 会被判为未知类型而拒绝，使所有携带 payload 的 PrivilegedExec 动作失败。
// 在解码边界调用本方法，可让解码请求与进程内请求在 GetPayload 面前走同一条分支。
func (r *ExecuteRequest) NormalizePayload() error {
	if r == nil {
		return ErrExecutionRequestNil
	}

	switch r.Payload.(type) {
	case nil, json.RawMessage, []byte, string:
		return nil
	}

	raw, err := json.Marshal(r.Payload)
	if err != nil {
		return fmt.Errorf("normalize payload: %w", err)
	}
	r.Payload = json.RawMessage(raw)
	return nil
}

// defaultExecuteTimeout is the default timeout for execution requests in seconds
const defaultExecuteTimeout = 30

// ============================================================================
// Request Builders
// ============================================================================

// RequestOption defines a functional option for configuring ExecuteRequest
type RequestOption func(*ExecuteRequest)

// NewExecuteRequest creates an ExecuteRequest with sensible defaults.
// Use With* options to customize the request.
// actionName: 操作名称常量（如 ActionJobExecute, ActionUserList 等）
// executionID: 执行唯一标识符；如果为空则自动生成
func NewExecuteRequest(actionName ActionName, executionID string, opts ...RequestOption) *ExecuteRequest {
	if executionID == "" {
		executionID = generateExecutionID(actionName)
	}

	req := &ExecuteRequest{
		TraceID:     generateTraceID(),
		ExecutionID: executionID,
		ActionName:  actionName,
		Mode:        RunModeSync,
		Env:         map[string]string{"LC_ALL": "en_US.UTF-8"},
		Timeout:     defaultExecuteTimeout,
	}

	for _, opt := range opts {
		opt(req)
	}

	return req
}

// WithTargetUser sets the target username for the request
func WithTargetUser(username string) RequestOption {
	return func(r *ExecuteRequest) {
		r.TargetUser = username
	}
}

// WithTargetNode pins the request to a specific agent by node ID / hostname.
func WithTargetNode(nodeID string) RequestOption {
	return func(r *ExecuteRequest) {
		r.TargetNode = nodeID
	}
}

// WithPayload sets the structured payload for the request
func WithPayload(payload any) RequestOption {
	return func(r *ExecuteRequest) {
		r.Payload = payload
	}
}

// WithTimeout sets the timeout in seconds
func WithTimeout(d time.Duration) RequestOption {
	return func(r *ExecuteRequest) {
		if d > 0 {
			r.Timeout = int(d.Seconds())
		}
	}
}

// WithNodeAffinity sets the node-selection strategy applied within the
// candidate pool produced by NodeSelector.
func WithNodeAffinity(affinity NodeAffinity) RequestOption {
	return func(r *ExecuteRequest) {
		r.NodeAffinity = affinity
	}
}

const generatedExecutionIDLength = 24

// generateExecutionID creates a unique execution ID for idempotency.
// It is intentionally action-agnostic: action_name already exists as a separate field.
func generateExecutionID(_ ActionName) string {
	return crypto.GenRandomId(generatedExecutionIDLength)
}

// generateTraceID creates a pure trace identifier for request correlation.
// It must not encode action or request identity because those already have dedicated fields.
func generateTraceID() string {
	return crypto.GenRandomId(generatedExecutionIDLength)
}

// ============================================================================
// PrivilegedExec I/O (stdin/stdout wrapper)
// ============================================================================

// ExecuteInput is the JSON input sent to PrivilegedExec via stdin
type ExecuteInput struct {
	// Version must exactly match the PrivilegedExec binary's build version.
	// Missing or mismatched values are rejected (no negotiation / fallback).
	Version string          `json:"version"`
	Token   string          `json:"token"`
	Request *ExecuteRequest `json:"request"`
}

// MatchDcExecProtocolVersion rejects any engine↔PrivilegedExec version skew (exact match only).
func MatchDcExecProtocolVersion(engineVersion, dcExecVersion string) error {
	if engineVersion != dcExecVersion {
		return fmt.Errorf(
			"protocol version mismatch: engine sent %q, PrivilegedExec is %q — install matching design-cloud-engine package",
			engineVersion, dcExecVersion)
	}
	return nil
}

// ExecuteOutput defines the JSON response received from the PrivilegedExec
type ExecuteOutput struct {
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Written int64  `json:"written,omitempty"`
}

// ============================================================================
// Errors
// ============================================================================

// Error codes for execution errors
const (
	ErrCodeExecutionService = 5001 // General execution service error
	ErrCodeCommandFailed    = 5002 // Command execution failed
)

// ActionError represents an error during command execution
type ActionError struct {
	Code        int
	Message     string
	TraceID     string
	ExecutionID string
	ActionName  ActionName
	ExitCode    int
	Stderr      string
}

func (e *ActionError) Error() string {
	return e.Message
}

// ============================================================================
// Core Interface
// ============================================================================

// Executor 定义命令执行的统一接口。
// 由 server.control.Dispatcher（远程分发）和 agent/executor.Service（本地执行）共同实现。
type Executor interface {
	// Execute 执行一个命令请求并返回结果
	Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResponse, error)
}

// ============================================================================
// Helper Functions
// ============================================================================

// Run executes a request via the Executor and returns stdout or ActionError.
// This is the primary entry point for services to execute actions.
func Run(ctx context.Context, svc Executor, req *ExecuteRequest) (string, error) {
	resp, err := svc.Execute(ctx, req)
	if err != nil {
		return "", &ActionError{
			Code:        ErrCodeExecutionService,
			Message:     fmt.Sprintf("execution service error: %v", err),
			TraceID:     req.TraceID,
			ExecutionID: req.ExecutionID,
			ActionName:  req.ActionName,
			ExitCode:    -1,
			Stderr:      err.Error(),
		}
	}

	if !resp.Success {
		msg := cmp.Or(resp.Stderr, fmt.Sprintf("command failed with exit code %d", resp.ExitCode))
		return resp.Stdout, &ActionError{
			Code:        ErrCodeCommandFailed,
			Message:     msg,
			TraceID:     req.TraceID,
			ExecutionID: req.ExecutionID,
			ActionName:  req.ActionName,
			ExitCode:    resp.ExitCode,
			Stderr:      resp.Stderr,
		}
	}

	return resp.Stdout, nil
}

// HandleError logs structured execution errors and returns the original error.
func HandleError(l *slog.Logger, err error, context string) error {
	if err == nil {
		return nil
	}

	if execErr, ok := errors.AsType[*ActionError](err); ok {
		l.Error("Execution failed",
			"context", context,
			"code", execErr.Code,
			"trace_id", execErr.TraceID,
			"execution_id", execErr.ExecutionID,
			"action_name", execErr.ActionName,
			"exit_code", execErr.ExitCode,
			"stderr", execErr.Stderr,
			"error", err,
		)
		return execErr
	}

	return err
}
