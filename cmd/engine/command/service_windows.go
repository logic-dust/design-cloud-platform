package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"design-cloud-engine/internal/common"
	fsutil "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	WindowsServiceName = "DesignCloudEngine"
	WindowsServiceDesc = "Design Cloud Workstation Managed Agent Service"
)

func runServiceCommand(flags *Flags, args []string) error {
	if len(args) == 0 {
		printServiceUsage()
		return fmt.Errorf("service sub-command required: status, stop, install, uninstall, start")
	}
	switch args[0] {
	case "status":
		return runWindowsServiceStatus()
	case "stop":
		return runWindowsServiceStop()
	case "install":
		return runWindowsServiceInstall(flags, args[1:])
	case "uninstall":
		return runWindowsServiceUninstall()
	case "start":
		return runWindowsServiceStart()
	default:
		printServiceUsage()
		return fmt.Errorf("unknown service command: %s", args[0])
	}
}

func printServiceUsage() {
	common.PrintStderr("Usage: %s service <status|stop|install|uninstall|start>\n\n", config.AppName)
	common.PrintStderr("Subcommands:\n")
	common.PrintStderr("  status                   Check if local engine process is running\n")
	common.PrintStderr("  stop                     Gracefully stop local engine process\n")
	common.PrintStderr("  install                  Install as Windows Service (Windows only)\n")
	common.PrintStderr("  uninstall                Uninstall Windows Service (Windows only)\n")
	common.PrintStderr("  start                    Start Windows Service (Windows only)\n")
}

func runWindowsServiceInstall(parent *Flags, args []string) error {
	f, err := MergeFlags(parent, args, os.Stderr, "install")
	if err != nil {
		return fmt.Errorf("install parse error: %w", err)
	}
	mode, err := ResolveStartMode(f)
	if err != nil {
		return err
	}
	serviceArgs, err := StartArgv(f, mode)
	if err != nil {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("detect executable path: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolve absolute executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(WindowsServiceName)
	if err == nil {
		_ = s.Close()
		return fmt.Errorf("service %s already exists", WindowsServiceName)
	}

	cfg := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  "Design Cloud Engine Agent",
		Description:  WindowsServiceDesc,
	}

	s, err = m.CreateService(WindowsServiceName, exePath, cfg, serviceArgs...)
	if err != nil {
		return fmt.Errorf("create service %s: %w", WindowsServiceName, err)
	}
	defer s.Close()

	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := s.SetRecoveryActions(recoveryActions, 86400); err != nil {
		common.PrintStderr("Warning: failed to set service recovery actions: %v\n", err)
	}

	common.PrintStdout("Service %s installed successfully (auto-start configured)\n", WindowsServiceName)
	return nil
}

func runWindowsServiceUninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed: %w", WindowsServiceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		time.Sleep(1 * time.Second)
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %s: %w", WindowsServiceName, err)
	}

	common.PrintStdout("Service %s uninstalled successfully\n", WindowsServiceName)
	return nil
}

func runWindowsServiceStart() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed: %w", WindowsServiceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State == svc.Running {
		common.PrintStdout("Service %s is already running\n", WindowsServiceName)
		return nil
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %s: %w", WindowsServiceName, err)
	}

	common.PrintStdout("Service %s started successfully\n", WindowsServiceName)
	return nil
}

const stillActive uint32 = 259 // Win32 STILL_ACTIVE

// ProcessStartTime returns the Win32 process creation timestamp (FILETIME ticks) of pid,
// used as a PID-reuse sentinel together with the PID file.
func ProcessStartTime(pid int) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	var createTime, exitTime, kernelTime, userTime windows.Filetime
	if err := windows.GetProcessTimes(h, &createTime, &exitTime, &kernelTime, &userTime); err != nil {
		return 0, err
	}
	return uint64(createTime.HighDateTime)<<32 | uint64(createTime.LowDateTime), nil
}

func isProcessLocked(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	var ov windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
	if err == nil {
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov)
		return false, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

func runWindowsServiceStatus() error {
	// 1. Query SCM first if installed as service
	if m, err := mgr.Connect(); err == nil {
		defer m.Disconnect()
		if s, err := m.OpenService(WindowsServiceName); err == nil {
			defer s.Close()
			if st, err := s.Query(); err == nil {
				switch st.State {
				case svc.Running:
					common.PrintStdout("Engine is RUNNING (Windows Service %s)\n", WindowsServiceName)
					return nil
				case svc.StartPending:
					common.PrintStdout("Engine is STARTING (Windows Service %s)\n", WindowsServiceName)
					return nil
				case svc.StopPending:
					common.PrintStdout("Engine is STOPPING (Windows Service %s)\n", WindowsServiceName)
					return nil
				case svc.Stopped:
					common.PrintlnStdout("Engine is NOT running (Service stopped)")
					return fmt.Errorf("service stopped")
				}
			}
		}
	}

	// 2. Query PID file and process lock
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

	if starttime > 0 {
		st, err := ProcessStartTime(pid)
		if err == nil && st != starttime {
			common.PrintlnStdout("Engine is NOT running (PID was reused)")
			return fmt.Errorf("process not running")
		}
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		common.PrintStdout("Engine is RUNNING (PID %d, active exclusive lock held)\n", pid)
		return nil
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err == nil && exitCode == stillActive {
		common.PrintStdout("Engine is RUNNING (PID %d)\n", pid)
		return nil
	}

	common.PrintlnStdout("Engine is NOT running")
	return fmt.Errorf("process not running")
}

func runWindowsServiceStop() error {
	// Try SCM stop first if installed
	if m, err := mgr.Connect(); err == nil {
		defer m.Disconnect()
		if s, err := m.OpenService(WindowsServiceName); err == nil {
			defer s.Close()
			if st, err := s.Control(svc.Stop); err == nil && (st.State == svc.StopPending || st.State == svc.Stopped) {
				common.PrintStdout("Sent stop command to Windows Service %s\n", WindowsServiceName)
				return nil
			}
		}
	}

	// Fallback to PID file termination
	pid, starttime, err := readPIDRecord()
	if err != nil {
		common.PrintStdout("Engine is not running\n")
		return nil
	}

	if starttime > 0 {
		st, err := ProcessStartTime(pid)
		if err == nil && st != starttime {
			common.PrintStdout("Engine is not running (PID was reused by another process)\n")
			return nil
		}
	}

	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		common.PrintStdout("Engine is not running\n")
		return nil
	}
	defer windows.CloseHandle(h)

	_ = windows.TerminateProcess(h, 1)
	common.PrintStdout("Stopped process %d\n", pid)
	return nil
}
