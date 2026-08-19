package executor

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"design-cloud-engine/internal/execution"
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

func newMockService(launcher, direct *mockExecutor) *Service {
	return &Service{
		launcherExec: launcher,
		directExec:   direct,
		logger:       noopLogger(),
	}
}

// pickAction returns a registered action with the requested routing kind.
// Windows builds register Direct actions only (no dc-launcher), so launcher-routing cases
// skip there: the contract does not exist on that platform, and skipping says so explicitly
// instead of failing on a Linux-only action name.
func pickAction(t *testing.T, direct bool) execution.ActionName {
	t.Helper()

	for name, spec := range execution.Actions {
		if spec.IsDirect() == direct {
			return name
		}
	}

	kind := "launcher"
	if direct {
		kind = "direct"
	}
	t.Skipf("当前平台未注册任何 %s 类型的 action", kind)
	return ""
}

// ============================================================================
// Request Validation
// ============================================================================

func TestExecute_MissingActionName(t *testing.T) {
	t.Parallel()

	svc := newMockService(&mockExecutor{name: "launcher"}, &mockExecutor{name: "direct"})
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

	svc := newMockService(&mockExecutor{name: "launcher"}, &mockExecutor{name: "direct"})
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

	launcher := &mockExecutor{name: "launcher"}
	direct := &mockExecutor{name: "direct"}
	svc := newMockService(launcher, direct)

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
	if launcher.executed || direct.executed {
		t.Error("neither executor should have been called for unknown action")
	}
}

// ============================================================================
// Executor Routing
// ============================================================================

func TestExecute_RoutesToDirect(t *testing.T) {
	t.Parallel()

	launcher := &mockExecutor{name: "launcher"}
	direct := &mockExecutor{
		name: "direct",
		resp: &execution.ExecuteResponse{TraceID: "trace-1", Success: true},
	}

	svc := newMockService(launcher, direct)

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
	if launcher.executed {
		t.Error("launcher should NOT be called for direct action")
	}
}

func TestExecute_RoutesToLauncher(t *testing.T) {
	t.Parallel()

	launcher := &mockExecutor{
		name: "launcher",
		resp: &execution.ExecuteResponse{TraceID: "trace-2", Success: true},
	}
	direct := &mockExecutor{name: "direct"}

	svc := newMockService(launcher, direct)

	action := pickAction(t, false) // launcher action → routes to launcher executor
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
	if !launcher.executed {
		t.Errorf("expected launcher executor to be called for %s", action)
	}
	if direct.executed {
		t.Error("direct should NOT be called for launcher action")
	}
}

// ============================================================================
// Executor Error Propagation
// ============================================================================

func TestExecute_PropagatesLauncherError(t *testing.T) {
	t.Parallel()

	launcher := &mockExecutor{
		name: "launcher",
		resp: &execution.ExecuteResponse{Success: false, ExitCode: 1, Stderr: "file operation failed"},
	}
	direct := &mockExecutor{name: "direct"}

	svc := newMockService(launcher, direct)

	req := &execution.ExecuteRequest{
		TraceID:    "trace-3",
		ActionName: pickAction(t, false), // routes to launcher
	}

	resp, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure from launcher")
	}
	if !launcher.executed {
		t.Error("launcher should have been called")
	}
}

func TestExecute_PropagatesDirectError(t *testing.T) {
	t.Parallel()

	launcher := &mockExecutor{name: "launcher"}
	direct := &mockExecutor{
		name: "direct",
		resp: &execution.ExecuteResponse{Success: false, ExitCode: 2, Stderr: "direct exec failed"},
	}

	svc := newMockService(launcher, direct)

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

// ============================================================================
// Response defaults
// ============================================================================

func TestExecuteResponseDefaults(t *testing.T) {
	t.Parallel()

	resp := &execution.ExecuteResponse{}
	if resp.Success {
		t.Error("zero-value response should not be successful")
	}
	if resp.ExitCode != 0 {
		t.Errorf("zero-value ExitCode = %d, want 0", resp.ExitCode)
	}
}
