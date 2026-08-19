package provider

import (
	"context"
	"reflect"
	"testing"

	"design-cloud-engine/internal/execution"
)

func TestParseUserListLine(t *testing.T) {
	t.Parallel()

	result := parseUserListLine("testuser:x:1001:1001:Test User:/home/testuser:/bin/bash")
	if result.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", result.Username)
	}
	if result.Uid != "1001" {
		t.Errorf("Uid = %q, want 1001", result.Uid)
	}
	if result.Gid != "1001" {
		t.Errorf("Gid = %q, want 1001", result.Gid)
	}
	if result.Home != "/home/testuser" {
		t.Errorf("Home = %q, want /home/testuser", result.Home)
	}
	if result.Shell != "/bin/bash" {
		t.Errorf("Shell = %q, want /bin/bash", result.Shell)
	}
}

func TestParseUserListLine_TooFewFields(t *testing.T) {
	t.Parallel()

	result := parseUserListLine("only:three:fields")
	if result.Username != "" {
		t.Errorf("expected empty SystemUser for too few fields, got %q", result.Username)
	}
}

func TestParseUserGetOutput(t *testing.T) {
	t.Parallel()

	result, err := parseUserGetOutput("testuser:x:1001:1001:Test User:/home/testuser:/bin/bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", result.Username)
	}
	if result.Uid != "1001" {
		t.Errorf("Uid = %q, want 1001", result.Uid)
	}
}

func TestParseUserGetOutput_TooFewFields(t *testing.T) {
	t.Parallel()

	_, err := parseUserGetOutput("too:few")
	if err == nil {
		t.Fatal("expected error for too few fields")
	}
}

func TestSplitGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"single comma", "group1,group2,group3", []string{"group1", "group2", "group3"}},
		{"spaces", "group1 group2", []string{"group1", "group2"}},
		{"mixed", "group1, group2", []string{"group1", "group2"}},
		{"tabs", "group1\tgroup2", []string{"group1", "group2"}},
		{"whitespace only", "  \t ", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitGroups(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitGroups(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestIsIDInRange(t *testing.T) {
	t.Parallel()

	min1000 := 1000
	max2000 := 2000

	tests := []struct {
		name   string
		id     string
		minVal *int
		maxVal *int
		want   bool
	}{
		{"in range", "1500", &min1000, &max2000, true},
		{"too low", "500", &min1000, &max2000, false},
		{"too high", "2500", &min1000, &max2000, false},
		{"at min", "1000", &min1000, &max2000, true},
		{"at max", "2000", &min1000, &max2000, true},
		{"only min", "1500", &min1000, nil, true},
		{"below min only", "500", &min1000, nil, false},
		{"only max", "1500", nil, &max2000, true},
		{"above max only", "2500", nil, &max2000, false},
		{"no range", "1500", nil, nil, true},
		{"not a number", "abc", &min1000, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isIDInRange(tc.id, tc.minVal, tc.maxVal)
			if got != tc.want {
				t.Errorf("isIDInRange(%q, %v, %v) = %v, want %v", tc.id, tc.minVal, tc.maxVal, got, tc.want)
			}
		})
	}
}

// mockExecutor implements execution.Executor for testing.
type mockExecutor struct {
	executeFn func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error)
}

func (m *mockExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return m.executeFn(ctx, req)
}

func TestLocalProvider_VerifyPassword_Success(t *testing.T) {
	t.Parallel()

	mockExec := &mockExecutor{
		executeFn: func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
			return &execution.ExecuteResponse{Success: true}, nil
		},
	}
	p := &localProvider{
		execSvc: mockExec,
	}

	err := p.VerifyPassword(context.Background(), "testuser", "correct-password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLocalProvider_VerifyPassword_Failure(t *testing.T) {
	t.Parallel()

	mockExec := &mockExecutor{
		executeFn: func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
			return nil, ErrUserPasswordIncorrect
		},
	}
	p := &localProvider{
		execSvc: mockExec,
	}

	err := p.VerifyPassword(context.Background(), "testuser", "wrong-password")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLocalProvider_ChangePassword(t *testing.T) {
	t.Parallel()

	var capturedReq *execution.ExecuteRequest
	mockExec := &mockExecutor{
		executeFn: func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
			capturedReq = req
			return &execution.ExecuteResponse{Success: true}, nil
		},
	}
	p := &localProvider{
		execSvc: mockExec,
	}

	err := p.ChangePassword(context.Background(), "testuser", "oldpass", "newpass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("expected Execute to be called")
	}
	if capturedReq.ActionName != execution.ActionUserPasswordChange {
		t.Errorf("action = %s, want %s", capturedReq.ActionName, execution.ActionUserPasswordChange)
	}
}

func TestLocalProvider_ResetPassword(t *testing.T) {
	t.Parallel()

	mockExec := &mockExecutor{
		executeFn: func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
			return &execution.ExecuteResponse{Success: true}, nil
		},
	}
	p := &localProvider{
		execSvc: mockExec,
	}

	err := p.ResetPassword(context.Background(), "testuser", "newpass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
