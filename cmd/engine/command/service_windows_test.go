package command

import (
	"os"
	"path/filepath"
	"testing"

	fsutil "design-cloud-engine/internal/common/fs"
)

// Windows-specific service management tests.
// Pure CLI dispatch tests (requires-subcommand, unknown-subcommand) live in service_test.go.

func TestReadPIDRecord_Windows(t *testing.T) {
	// 使用临时目录模拟 PROGRAMDATA，避免污染系统目录
	tmpDir := t.TempDir()
	t.Setenv("PROGRAMDATA", tmpDir)

	// Case 1: 文件不存在
	_, _, err := readPIDRecord()
	if err == nil {
		t.Fatal("expected error when PID file does not exist")
	}

	runtimeDir := filepath.Join(tmpDir, "DesignCloudEngine")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		t.Fatalf("failed to create mock runtime dir: %v", err)
	}
	pidPath := fsutil.PIDFilePath()

	// Case 2: 空文件
	if err := os.WriteFile(pidPath, []byte("   \n"), 0644); err != nil {
		t.Fatalf("failed to write empty PID file: %v", err)
	}
	if _, _, err := readPIDRecord(); err == nil {
		t.Fatal("expected error for empty PID file")
	}

	// Case 3: 仅 PID
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
	pid, starttime, err := readPIDRecord()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 12345 || starttime != 0 {
		t.Fatalf("got pid=%d, starttime=%d; want 12345, 0", pid, starttime)
	}

	// Case 4: PID + starttime
	if err := os.WriteFile(pidPath, []byte("54321 13380000000\n"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
	pid, starttime, err = readPIDRecord()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 54321 || starttime != 13380000000 {
		t.Fatalf("got pid=%d, starttime=%d; want 54321, 13380000000", pid, starttime)
	}

	// Case 5: 损坏内容
	if err := os.WriteFile(pidPath, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatalf("failed to write corrupt PID file: %v", err)
	}
	if _, _, err := readPIDRecord(); err == nil {
		t.Fatal("expected error for non-numeric PID")
	}
}
