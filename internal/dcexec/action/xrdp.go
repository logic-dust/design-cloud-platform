//go:build !windows

package action

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/platform/procfs"
)

// HandleXRDPControl stops an XRDP user X seat as the already-dropped target user.
// Ladder: loginctl terminate-session (own session) → SIGTERM → SIGKILL on the
// user's Xorg/xorgxrdp for the given display. Never touches xrdp/xrdp-sesman.
func HandleXRDPControl(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.XRDPControl](req)
	if err != nil {
		return "", fmt.Errorf("failed to parse XRDP control payload: %w", err)
	}
	if p.Operation != "stop" {
		return "", fmt.Errorf("unsupported XRDP operation %q (only stop)", p.Operation)
	}
	if p.Display == nil {
		return "", fmt.Errorf("display is required for XRDP stop")
	}
	display := *p.Display
	if display < 0 {
		return "", fmt.Errorf("invalid display %d", display)
	}

	pid, err := stopXRDPSeat(ctx, display, p.PID)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(payload.XRDPControlResult{Display: display, PID: pid})
	if err != nil {
		return "", fmt.Errorf("marshal XRDP control result: %w", err)
	}
	return string(out), nil
}

func stopXRDPSeat(ctx context.Context, display int, hintPID uint) (int, error) {
	pid, cmdline, err := findXRDPSeatPID(display, hintPID)
	if err != nil {
		// Already gone — treat as success (idempotent stop/delete).
		if os.IsNotExist(err) || strings.Contains(err.Error(), "not found") {
			return 0, nil
		}
		return 0, err
	}

	tryLoginctlTerminate(ctx, pid, display)

	if err := signalPID(pid, syscall.SIGTERM); err != nil && !isProcessGone(err) {
		return pid, fmt.Errorf("SIGTERM xrdp seat pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return pid, nil
		}
		select {
		case <-ctx.Done():
			return pid, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	if pidAlive(pid) {
		if err := signalPID(pid, syscall.SIGKILL); err != nil && !isProcessGone(err) {
			return pid, fmt.Errorf("SIGKILL xrdp seat pid %d (%s): %w", pid, summarizeCmdline(cmdline), err)
		}
	}
	return pid, nil
}

func findXRDPSeatPID(display int, hintPID uint) (int, string, error) {
	want := fmt.Sprintf(":%d", display)
	if hintPID > 0 {
		pidStr := strconv.FormatUint(uint64(hintPID), 10)
		raw, err := os.ReadFile(filepath.Join("/proc", pidStr, "cmdline"))
		if err == nil && len(raw) > 0 {
			args := strings.ReplaceAll(string(raw), "\x00", " ")
			if isXRDPSeatCmdline(args) && strings.Contains(args, want) {
				if ok, _ := verifyProcOwner(pidStr, ""); ok {
					return int(hintPID), args, nil
				}
			}
		}
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, "", err
	}
	expectedUID := currentUID()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pidStr := entry.Name()
		if _, err := strconv.Atoi(pidStr); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", pidStr, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.ReplaceAll(string(raw), "\x00", " ")
		if !isXRDPSeatCmdline(args) || !cmdlineHasDisplay(args, want) {
			continue
		}
		uid := readProcUID(pidStr)
		if uid != "" && uid != expectedUID {
			continue
		}
		pid, _ := strconv.Atoi(pidStr)
		return pid, args, nil
	}
	return 0, "", fmt.Errorf("xrdp seat for display %s not found", want)
}

func isXRDPSeatCmdline(args string) bool {
	lower := strings.ToLower(args)
	if strings.Contains(lower, "xorgxrdp") {
		return true
	}
	if (strings.Contains(args, "Xorg") || strings.Contains(lower, "/xorg ")) && strings.Contains(lower, "xrdp") {
		return true
	}
	return false
}

func cmdlineHasDisplay(args, want string) bool {
	fields := strings.Fields(args)
	for i, f := range fields {
		base := filepath.Base(f)
		if base != "Xorg" && base != "xorgxrdp" && !strings.HasSuffix(f, "Xorg") && !strings.HasSuffix(f, "xorgxrdp") {
			continue
		}
		if i+1 < len(fields) && fields[i+1] == want {
			return true
		}
	}
	// Fallback: any token equals :N (some wrappers rearrange args).
	return slices.Contains(fields, want)
}

func readProcUID(pid string) string {
	data, err := os.ReadFile(filepath.Join("/proc", pid, "status"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

func tryLoginctlTerminate(ctx context.Context, pid, display int) {
	sessionID := sessionIDForSeat(ctx, pid, display)
	if sessionID == "" {
		return
	}
	cmd := exec.CommandContext(ctx, "loginctl", "terminate-session", sessionID)
	_ = cmd.Run()
}

func sessionIDForSeat(ctx context.Context, pid, display int) string {
	if pid > 0 {
		if sid := procfs.SessionIDFromPID(pid); sid != "" {
			return sid
		}
	}
	return sessionIDByLoginctl(ctx, pid, display)
}

func sessionIDByLoginctl(ctx context.Context, pid, display int) string {
	out, err := exec.CommandContext(ctx, "loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return ""
	}
	expectedUID := currentUID()
	displayStr := fmt.Sprintf(":%d", display)
	pidStr := strconv.Itoa(pid)

	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		sid := fields[0]
		uid := fields[1]
		if uid != expectedUID {
			continue
		}

		show, err := exec.CommandContext(ctx, "loginctl", "show-session", sid, "-p", "Display", "-p", "Leader").Output()
		if err != nil {
			continue
		}

		matchedDisplay := false
		matchedLeader := false
		for propLine := range strings.SplitSeq(string(show), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(propLine), "=")
			if !ok {
				continue
			}
			switch k {
			case "Display":
				if display >= 0 && v == displayStr {
					matchedDisplay = true
				}
			case "Leader":
				if pid > 0 && v == pidStr {
					matchedLeader = true
				}
			}
		}
		if matchedDisplay || matchedLeader {
			return sid
		}
	}
	return ""
}

func signalPID(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

func pidAlive(pid int) bool {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}

func isProcessGone(err error) bool {
	return err != nil && (os.IsNotExist(err) || strings.Contains(err.Error(), "process already finished") || strings.Contains(err.Error(), "no such process"))
}

func summarizeCmdline(args string) string {
	fields := strings.Fields(args)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	return strings.Join(fields, " ")
}
