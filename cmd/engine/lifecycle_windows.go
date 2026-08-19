package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"

	"design-cloud-engine/cmd/engine/command"
	fsutil "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// --- 1. Pre-flight Environment Validation ---

// checkPlatformPreconditions performs process-level pre-flight environment checks for Windows.
// Windows nodes are permitted to execute under service or administrative accounts (e.g. LOCALSYSTEM).
func checkPlatformPreconditions() error {
	return nil
}

// --- 2. Runtime Lifecycle Supervision (SCM / Console) ---

// runWithLifecycle provides the process lifecycle driver for Windows.
// It acquires an exclusive process lockfile to prevent multi-instance runs,
// handles SCM or Console execution, and ensures clean resource release on shutdown.
func runWithLifecycle(_ *command.Flags, runFn func(ctx context.Context) error) error {
	unlock, err := acquireProcessLock()
	if err != nil {
		return err
	}
	defer unlock()

	isSvc, err := svc.IsWindowsService()
	if err == nil && isSvc {
		if err := svc.Run(command.WindowsServiceName, &engineService{runFn: runFn}); err != nil {
			return fmt.Errorf("windows service failed: %w", err)
		}
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return runFn(ctx)
}

type engineService struct {
	runFn func(ctx context.Context) error
}

func (s *engineService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.runFn(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	var runErr error
	gotResult := false

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				break loop
			default:
			}
		case err := <-errCh:
			runErr = err
			gotResult = true
			break loop
		}
	}

	// Drain runFn exactly once: if the select already received the result, do not
	// wait on the empty channel again (that permanently blocks SCM stop/recovery).
	if !gotResult {
		runErr = <-errCh
	}

	changes <- svc.Status{State: svc.Stopped}
	if runErr != nil {
		return false, 1
	}
	return false, 0
}

// --- 3. Process Singleton Lock Helpers ---

func acquireProcessLock() (func(), error) {
	path := fsutil.PIDFilePath()
	dir := filepath.Dir(path)
	if err := fsutil.MkdirPrivate(dir); err != nil {
		return nil, err
	}

	pid := os.Getpid()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open PID file %s: %w", path, err)
	}

	var ov windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov); err != nil {
		_ = f.Close()
		content, _ := os.ReadFile(path)
		return nil, fmt.Errorf("could not lock PID file %s: process %s might be running: %w", path, string(content), err)
	}

	content := strconv.Itoa(pid)
	if st, err := command.ProcessStartTime(pid); err == nil && st > 0 {
		content += " " + strconv.FormatUint(st, 10)
	}

	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to truncate %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to seek %s: %w", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to write PID to %s: %w", path, err)
	}
	_ = f.Sync()

	// Keep the sentinel lockfile in place on exit; closing the file descriptor automatically
	// releases the LockFileEx byte-range lock without introducing inode recreation TOCTOU races.
	return func() {
		_ = f.Close()
	}, nil
}

// --- 4. Windows Server Component Lifecycle (Stub) ---

func startServer(_ context.Context, _ *config.Config, _ *slog.Logger, _ chan<- error) (func(), error) {
	return nil, fmt.Errorf("server role is not supported on windows; windows runs in agent mode only")
}
