package audit

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/db"
)

func newTestServiceWithDir(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	svc, err := NewService(Config{
		PrimaryLogPath:  filepath.Join(dir, "audit.log"),
		SecurityLogPath: filepath.Join(dir, "security.log"),
		NodeID:          "test-node",
		Version:         "0.0.0-test",
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	return svc, dir
}

func readPrimaryLog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	return string(data)
}

func readSecurityLog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "security.log"))
	if err != nil {
		t.Fatalf("read security.log: %v", err)
	}
	return string(data)
}

func TestNewService(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	if !svc.IsEnabled() {
		t.Fatal("expected service to be enabled")
	}
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	if !strings.Contains(content, "AUDIT_SERVICE_INIT") {
		t.Error("audit.log missing AUDIT_SERVICE_INIT")
	}
	if !strings.Contains(content, "AUDIT_SYSTEM_STOP") {
		t.Error("audit.log missing AUDIT_SYSTEM_STOP")
	}
}

func TestLogAuth(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	svc.LogAuth(context.Background(), "LOGIN", "success", "alice", "192.168.1.1", slog.LevelInfo, nil, 100*time.Millisecond)
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	for _, want := range []string{"LOGIN", "alice", "192.168.1.1", "auth"} {
		if !strings.Contains(content, want) {
			t.Errorf("audit.log missing %q", want)
		}
	}
}

func TestLogExecution(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	svc.LogExecution(context.Background(), "JOB_SUBMIT", "success", "alice", "trace-001", slog.LevelInfo, map[string]any{"jobId": "123"}, 0)
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	for _, want := range []string{"JOB_SUBMIT", "alice", "trace-001", "execution"} {
		if !strings.Contains(content, want) {
			t.Errorf("audit.log missing %q", want)
		}
	}
}

func TestLogSecurity(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	svc.LogSecurity(context.Background(), "BANNED_IP_ATTEMPT", "blocked", slog.LevelWarn, map[string]any{"ip": "10.0.0.1"})
	_ = svc.Close()

	content := readSecurityLog(t, dir)
	for _, want := range []string{"BANNED_IP_ATTEMPT", "10.0.0.1", "security"} {
		if !strings.Contains(content, want) {
			t.Errorf("security.log missing %q", want)
		}
	}
}

func TestLogUser(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	svc.LogUser(context.Background(), "USER_CREATE", "success", "bob", slog.LevelInfo, nil)
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	for _, want := range []string{"USER_CREATE", "bob", "user"} {
		if !strings.Contains(content, want) {
			t.Errorf("audit.log missing %q", want)
		}
	}
}

func TestLogNode(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	svc.LogNode(context.Background(), "NODE_REGISTER", "success", "node-01", slog.LevelInfo, nil)
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	for _, want := range []string{"NODE_REGISTER", "node-01", "node"} {
		if !strings.Contains(content, want) {
			t.Errorf("audit.log missing %q", want)
		}
	}
}

func TestLogSystem(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	svc.LogSystem(context.Background(), "SERVICE_START", slog.LevelInfo, nil)
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	for _, want := range []string{"SERVICE_START", "system"} {
		if !strings.Contains(content, want) {
			t.Errorf("audit.log missing %q", want)
		}
	}
}

func TestDisableEnable(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	_ = svc.Close()

	svc2, err := NewService(Config{
		PrimaryLogPath:  filepath.Join(dir, "audit.log"),
		SecurityLogPath: filepath.Join(dir, "security.log"),
		NodeID:          "test-node",
		Version:         "0.0.0-test",
	})
	if err != nil {
		t.Fatalf("reopen service: %v", err)
	}

	svc2.mu.Lock()
	svc2.enabled = false
	svc2.mu.Unlock()

	if svc2.IsEnabled() {
		t.Fatal("expected disabled after set")
	}
	svc2.LogSystem(context.Background(), "SHOULD_BE_SILENT", slog.LevelInfo, nil)
	_ = svc2.Close()

	content := readPrimaryLog(t, dir)
	if strings.Contains(content, "SHOULD_BE_SILENT") {
		t.Error("disabled service should not log SHOULD_BE_SILENT")
	}
}

func TestLogWithDetails(t *testing.T) {
	svc, dir := newTestServiceWithDir(t)
	details := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}
	svc.LogAuth(context.Background(), "LOGIN", "failed", "charlie", "10.0.0.1", slog.LevelWarn, details, 0)
	_ = svc.Close()

	content := readPrimaryLog(t, dir)
	for _, want := range []string{"LOGIN", "charlie", "value1"} {
		if !strings.Contains(content, want) {
			t.Errorf("audit.log missing %q", want)
		}
	}
}

func TestLogFilesCreated(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewService(Config{
		PrimaryLogPath:  filepath.Join(dir, "audit.log"),
		SecurityLogPath: filepath.Join(dir, "security.log"),
		NodeID:          "test-node",
		Version:         "0.0.0-test",
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	svc.LogSystem(context.Background(), "TEST_START", slog.LevelInfo, nil)
	svc.LogSecurity(context.Background(), "TEST_SECURITY", "test", slog.LevelInfo, nil)

	_ = svc.Close()

	if _, err := os.Stat(filepath.Join(dir, "audit.log")); err != nil {
		t.Error("audit.log not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "security.log")); err != nil {
		t.Error("security.log not created")
	}
}

func TestInvalidPath(t *testing.T) {
	// 路径的父级必须指向"不可创建的目录"：POSIX 字面量 /nonexistent/dir 在 Windows 上
	// 会被解析为当前盘符下的 C:\nonexistent\dir，管理员账户可以直接创建成功，
	// 于是 NewService 不再报错。用"普通文件下的子路径"作为父级：POSIX 报 ENOTDIR，
	// Windows 报"父级不是目录"，两平台都必然失败。
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup blocker file: %v", err)
	}

	_, err := NewService(Config{
		PrimaryLogPath:  filepath.Join(blocker, "audit.log"),
		SecurityLogPath: filepath.Join(blocker, "security.log"),
		NodeID:          "test-node",
		Version:         "0.0.0-test",
	})
	if err == nil {
		t.Fatal("expected error for invalid log path")
	}
}

// ---------------------------------------------------------------------------
// Execution query tests (audit API integration with ExecutionRepo)
// ---------------------------------------------------------------------------

func TestExecutionQueryWithoutAttachedRepoReturnsError(t *testing.T) {
	svc, _ := newTestServiceWithDir(t)
	defer func() { _ = svc.Close() }()

	_, err := svc.ListExecutions(context.Background(), db.ExecutionListFilter{})
	if err == nil {
		t.Fatal("expected ErrExecutionRepoNotAttached, got nil")
	}
	_, err = svc.GetExecutionByExecutionID(context.Background(), "any")
	if err == nil {
		t.Fatal("expected ErrExecutionRepoNotAttached, got nil")
	}
}
