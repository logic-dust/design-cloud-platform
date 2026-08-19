package command

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"design-cloud-engine/internal/common"
	fsutil "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"
)

func runServiceCommand(_ *Flags, args []string) error {
	if len(args) == 0 {
		printServiceUsage()
		return fmt.Errorf("service sub-command required: status, stop")
	}
	switch args[0] {
	case "status":
		return runLinuxServiceStatus()
	case "stop":
		return runLinuxServiceStop()
	default:
		printServiceUsage()
		return fmt.Errorf("unknown service command: %s", args[0])
	}
}

func printServiceUsage() {
	common.PrintStderr("Usage: %s service <status|stop>\n\n", config.AppName)
	common.PrintStderr("Subcommands:\n")
	common.PrintStderr("  status                   Check if local engine process is running\n")
	common.PrintStderr("  stop                     Gracefully stop local engine process\n")
}

// ProcessStartTime returns the process start time ticks from /proc/<pid>/stat.
func ProcessStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(data)
	idx := strings.LastIndexByte(s, ')')
	if idx < 0 {
		return 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[idx+1:])
	if len(fields) < 20 {
		return 0, fmt.Errorf("truncated /proc/%d/stat", pid)
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

type processStatus int

const (
	processRunning processStatus = iota
	processPermissionDenied
	processStale
	processUnknown
)

func checkProcessAlive(pid int, recordedStarttime uint64) (*os.Process, processStatus, error) {
	process, _ := os.FindProcess(pid)

	err := process.Signal(syscall.Signal(0))
	if err == nil {
		if recordedStarttime > 0 {
			st, err := ProcessStartTime(pid)
			if err == nil && st != recordedStarttime {
				return nil, processStale, fmt.Errorf("pid %d was reused by another process", pid)
			}
		}
		return process, processRunning, nil
	}

	if errors.Is(err, syscall.ESRCH) {
		return nil, processStale, err
	}
	if errors.Is(err, syscall.EPERM) {
		return process, processPermissionDenied, err
	}
	return nil, processUnknown, err
}

// isProcessLocked checks whether the engine lockfile is currently held by an active process.
// It performs a non-blocking flock test: if LOCK_EX returns EWOULDBLOCK or EAGAIN,
// an active engine process holds the exclusive lock. If it succeeds, no process holds the lock.
func isProcessLocked(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() {
		_ = f.Close()
	}()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	return false, err
}

func runLinuxServiceStatus() error {
	path := fsutil.PIDFilePath()
	locked, err := isProcessLocked(path)
	if err != nil {
		return fmt.Errorf("service status error: %w", err)
	}
	if !locked {
		common.PrintlnStdout("Engine is NOT running")
		return fmt.Errorf("process not running")
	}

	pid, starttime, err := readPIDRecord()
	if err != nil {
		common.PrintStdout("Engine is RUNNING (active exclusive lock held)\n")
		return nil
	}

	_, status, _ := checkProcessAlive(pid, starttime)
	switch status {
	case processRunning:
		common.PrintStdout("Engine is RUNNING (PID %d)\n", pid)
		return nil
	case processPermissionDenied:
		common.PrintStdout("Engine is RUNNING (PID %d) - Permission Denied to inspect process\n", pid)
		return nil
	default:
		common.PrintStdout("Engine is RUNNING (PID %d, active exclusive lock held)\n", pid)
		return nil
	}
}

func runLinuxServiceStop() error {
	path := fsutil.PIDFilePath()
	locked, err := isProcessLocked(path)
	if err != nil {
		return fmt.Errorf("cannot stop: %w", err)
	}
	if !locked {
		common.PrintStdout("Engine is not running\n")
		return nil
	}

	pid, starttime, err := readPIDRecord()
	if err != nil {
		return fmt.Errorf("cannot stop: process locked but failed to read PID: %w", err)
	}

	process, status, _ := checkProcessAlive(pid, starttime)
	if process == nil || (status != processRunning && status != processPermissionDenied) {
		return fmt.Errorf("cannot stop: process %d is not responding or not accessible", pid)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send stop signal to process %d: %w", pid, err)
	}
	common.PrintStdout("Sent stop signal to process %d, waiting for exit...\n", pid)

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		curLocked, _ := isProcessLocked(path)
		if !curLocked {
			common.PrintStdout("Stopped process %d\n", pid)
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			common.PrintStdout("Stop timed out, sending SIGKILL to process %d\n", pid)
			_ = process.Signal(syscall.SIGKILL)
			time.Sleep(500 * time.Millisecond)
			curLocked, _ := isProcessLocked(path)
			if !curLocked {
				common.PrintStdout("Stopped process %d\n", pid)
				return nil
			}
			return fmt.Errorf("failed to stop process %d", pid)
		}
	}
}
