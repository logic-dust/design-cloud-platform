package command

import (
	"os"
	"syscall"
	"testing"
)

func TestCheckProcessAlive_PIDReuseIsStale(t *testing.T) {
	t.Parallel()

	_, status, err := checkProcessAlive(os.Getpid(), 1)
	if err == nil {
		t.Fatal("expected error for starttime mismatch")
	}
	if status != processStale {
		t.Errorf("status = %v, want processStale", status)
	}
}

func TestCheckProcessAlive_RunningWithMatch(t *testing.T) {
	t.Parallel()

	st, err := ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	_, status, err := checkProcessAlive(os.Getpid(), st)
	if err != nil {
		t.Fatalf("checkProcessAlive: %v", err)
	}
	if status != processRunning {
		t.Errorf("status = %v, want processRunning", status)
	}
}

func TestIsProcessLocked(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := tmpDir + "/test.lock"

	// 1. Non-existent file -> not locked
	locked, err := isProcessLocked(lockPath)
	if err != nil {
		t.Fatalf("expected nil error for non-existent file, got %v", err)
	}
	if locked {
		t.Errorf("expected false for non-existent file, got true")
	}

	// 2. Existing file without lock -> not locked
	f, err := os.Create(lockPath)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	locked, err = isProcessLocked(lockPath)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if locked {
		t.Errorf("expected false for unlocked file, got true")
	}

	// 3. Acquire exclusive lock on fd
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("failed to flock: %v", err)
	}

	locked, err = isProcessLocked(lockPath)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !locked {
		t.Errorf("expected true for locked file, got false")
	}

	// 4. Close file -> lock automatically released
	_ = f.Close()
	locked, err = isProcessLocked(lockPath)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if locked {
		t.Errorf("expected false after lock release, got true")
	}
}

func TestCheckProcessAlive_NonExistentPID(t *testing.T) {
	t.Parallel()

	proc, status, err := checkProcessAlive(99999999, 0)
	if proc != nil {
		t.Errorf("expected proc == nil for non-existent pid, got %v", proc)
	}
	if status == processRunning {
		t.Errorf("status should not be processRunning for non-existent pid")
	}
	if err == nil {
		t.Errorf("expected error for non-existent pid")
	}
}
