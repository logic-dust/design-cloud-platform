package executor

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"log/slog"
	"os"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
)

// mockExecutor records which executor was used and returns a controlled response.
type mockExecutor struct {
	name     string
	executed bool
	resp     *execution.ExecuteResponse
	err      error
}

func (m *mockExecutor) Execute(_ context.Context, _ *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	m.executed = true
	return m.resp, m.err
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func newMockService(dcexecExec, direct *mockExecutor) *Service {
	return &Service{
		dcexecExec: dcexecExec,
		directExec: direct,
		logger:     noopLogger(),
	}
}

// pickAction returns a registered action with the requested routing kind.
// Windows builds register Direct actions only (no PrivilegedExec), so dc-exec-routing cases
// skip there: the contract does not exist on that platform, and skipping says so explicitly
// instead of failing on a Linux-only action name.
func pickAction(t *testing.T, direct bool) execution.ActionName {
	t.Helper()

	for name, spec := range execution.Actions {
		if spec.IsDirect() == direct {
			return name
		}
	}

	kind := "dcexec"
	if direct {
		kind = "direct"
	}
	t.Skipf("当前平台未注册任何 %s 类型的 action", kind)
	return ""
}

// ============================================================================
// Request Validation
// ============================================================================

func TestExecute_NilRequest(t *testing.T) {
	t.Parallel()

	svc := newMockService(&mockExecutor{name: "dcexec"}, &mockExecutor{name: "direct"})
	resp, err := svc.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success || resp.TraceID != "" || resp.Stderr == "" {
		t.Fatalf("invalid nil-request response: %+v", resp)
	}
}

func TestExecute_MissingActionName(t *testing.T) {
	t.Parallel()

	svc := newMockService(&mockExecutor{name: "dcexec"}, &mockExecutor{name: "direct"})
	req := &execution.ExecuteRequest{
		TraceID: "trace-1",
	}
	resp, err := svc.Execute(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure for missing action name")
	}
}

func TestExecute_MissingTraceID(t *testing.T) {
	t.Parallel()

	svc := newMockService(&mockExecutor{name: "dcexec"}, &mockExecutor{name: "direct"})
	req := &execution.ExecuteRequest{
		ActionName: "some-action",
	}
	resp, err := svc.Execute(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure for missing trace ID")
	}
}

// ============================================================================
// Unknown / Unsupported Action
// ============================================================================

func TestExecute_UnknownAction(t *testing.T) {
	t.Parallel()

	dcexecExec := &mockExecutor{name: "dcexec"}
	direct := &mockExecutor{name: "direct"}
	svc := newMockService(dcexecExec, direct)

	req := &execution.ExecuteRequest{
		TraceID:    "trace-1",
		ActionName: "non.existent.action",
	}
	resp, err := svc.Execute(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure for unknown action")
	}
	if dcexecExec.executed || direct.executed {
		t.Error("neither executor should have been called for unknown action")
	}
}

// ============================================================================
// Executor Routing
// ============================================================================

func TestExecute_RoutesToDirect(t *testing.T) {
	t.Parallel()

	dcexecExec := &mockExecutor{name: "dcexec"}
	direct := &mockExecutor{
		name: "direct",
		resp: &execution.ExecuteResponse{TraceID: "trace-1", Success: true},
	}

	svc := newMockService(dcexecExec, direct)

	action := pickAction(t, true) // direct action → routes to direct executor
	req := &execution.ExecuteRequest{
		TraceID:    "trace-1",
		ActionName: action,
	}

	resp, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}
	if !direct.executed {
		t.Errorf("expected direct executor to be called for %s", action)
	}
	if dcexecExec.executed {
		t.Error("dc-exec executor should NOT be called for direct action")
	}
}

func TestExecute_RoutesToDcExec(t *testing.T) {
	t.Parallel()

	dcexecExec := &mockExecutor{
		name: "dcexec",
		resp: &execution.ExecuteResponse{TraceID: "trace-2", Success: true},
	}
	direct := &mockExecutor{name: "direct"}

	svc := newMockService(dcexecExec, direct)

	action := pickAction(t, false) // PrivilegedExec action → routes to PrivilegedExec executor
	req := &execution.ExecuteRequest{
		TraceID:    "trace-2",
		ActionName: action,
	}

	resp, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}
	if !dcexecExec.executed {
		t.Errorf("expected dc-exec executor to be called for %s", action)
	}
	if direct.executed {
		t.Error("direct should NOT be called for PrivilegedExec action")
	}
}

// ============================================================================
// Executor Error Propagation
// ============================================================================

func TestExecute_PropagatesDcExecError(t *testing.T) {
	t.Parallel()

	dcexecExec := &mockExecutor{
		name: "dcexec",
		resp: &execution.ExecuteResponse{Success: false, ExitCode: 1, Stderr: "file operation failed"},
	}
	direct := &mockExecutor{name: "direct"}

	svc := newMockService(dcexecExec, direct)

	req := &execution.ExecuteRequest{
		TraceID:    "trace-3",
		ActionName: pickAction(t, false), // routes to PrivilegedExec
	}

	resp, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure from dc-exec")
	}
	if !dcexecExec.executed {
		t.Error("dc-exec executor should have been called")
	}
}

func TestExecute_PropagatesDirectError(t *testing.T) {
	t.Parallel()

	dcexecExec := &mockExecutor{name: "dcexec"}
	direct := &mockExecutor{
		name: "direct",
		resp: &execution.ExecuteResponse{Success: false, ExitCode: 2, Stderr: "direct exec failed"},
	}

	svc := newMockService(dcexecExec, direct)

	req := &execution.ExecuteRequest{
		TraceID:    "trace-4",
		ActionName: pickAction(t, true), // routes to direct
	}

	resp, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure from direct executor")
	}
	if !direct.executed {
		t.Error("direct should have been called")
	}
}

// ============================================================================
// Request Builder
// ============================================================================

func TestNewExecuteRequest_Defaults(t *testing.T) {
	t.Parallel()

	req := execution.NewExecuteRequest("test.action", "")
	if req.TraceID == "" {
		t.Error("expected non-empty TraceID")
	}
	if req.ExecutionID == "" {
		t.Error("expected non-empty ExecutionID")
	}
	if req.ActionName != "test.action" {
		t.Errorf("ActionName = %q, want %q", req.ActionName, "test.action")
	}
}

func TestExecuteRequestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *execution.ExecuteRequest
		wantErr bool
	}{
		{"valid", &execution.ExecuteRequest{ActionName: "a", TraceID: "t"}, false},
		{"nil", nil, true},
		{"no action", &execution.ExecuteRequest{TraceID: "t"}, true},
		{"no trace", &execution.ExecuteRequest{ActionName: "a"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestExecute_InjectsSchedulerEnvScript(t *testing.T) {
	cases := []struct {
		action  execution.ActionName
		payload any
		read    func(req *execution.ExecuteRequest) string
	}{
		{execution.ActionJobQuery, &payload.JobQuery{}, func(r *execution.ExecuteRequest) string { return r.Payload.(*payload.JobQuery).EnvScript }},
		{execution.ActionJobSubmit, &payload.JobSubmit{}, func(r *execution.ExecuteRequest) string { return r.Payload.(*payload.JobSubmit).EnvScript }},
		{execution.ActionJobControl, &payload.JobControl{}, func(r *execution.ExecuteRequest) string { return r.Payload.(*payload.JobControl).EnvScript }},
		{execution.ActionJobModify, &payload.JobModify{}, func(r *execution.ExecuteRequest) string { return r.Payload.(*payload.JobModify).EnvScript }},
	}
	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			svc := newMockService(&mockExecutor{}, &mockExecutor{})
			svc.SetSchedulerEnvScript(func() string { return "/node/local/env.sh" })

			// Server-supplied value must be replaced by the node-local one;
			// raw JSON payloads (as received over gRPC) must be decoded first.
			req := &execution.ExecuteRequest{ActionName: tc.action, Payload: json.RawMessage(`{"envScript":"/server/side.sh"}`)}
			if err := svc.injectJobEnvScript(req); err != nil {
				t.Fatalf("injectJobEnvScript: %v", err)
			}
			if got := tc.read(req); got != "/node/local/env.sh" {
				t.Errorf("EnvScript = %q, want node-local value", got)
			}

			// No provider or an empty provider value: no script is sourced.
			svc.SetSchedulerEnvScript(nil)
			req = &execution.ExecuteRequest{ActionName: tc.action, Payload: tc.payload}
			if err := svc.injectJobEnvScript(req); err != nil {
				t.Fatalf("injectJobEnvScript(nil provider): %v", err)
			}
			if got := tc.read(req); got != "" {
				t.Errorf("EnvScript = %q, want empty", got)
			}
		})
	}

	// Non-job actions are left untouched.
	svc := newMockService(&mockExecutor{}, &mockExecutor{})
	svc.SetSchedulerEnvScript(func() string { return "/node/local/env.sh" })
	req := &execution.ExecuteRequest{ActionName: execution.ActionFileList, Payload: json.RawMessage(`{}`)}
	if err := svc.injectJobEnvScript(req); err != nil {
		t.Fatalf("non-job action: %v", err)
	}
	if _, ok := req.Payload.(json.RawMessage); !ok {
		t.Errorf("non-job payload was rewritten: %T", req.Payload)
	}
}
