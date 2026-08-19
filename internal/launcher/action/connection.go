//go:build !windows

package action

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

// currentUID returns the real UID of the current process as a decimal string.
// The launcher has already switched to the target user via Setuid before
// handlers are called, so this reflects the target user identity.
func currentUID() string {
	return strconv.Itoa(os.Getuid())
}

// HandleConnectionCheck checks the liveness of connections.
// Currently, it verifies VNC connections by inspecting PIDs and command-lines via /proc.
// TODO: Extend with multi-protocol probes (e.g. RDP/SSH socket or daemon probing) in future evolution.
// It runs as the target user (PrivilegeTargetUser).
func HandleConnectionCheck(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.ConnectionCheck](req)
	if err != nil {
		return "", fmt.Errorf("failed to parse connection check payload: %w", err)
	}

	if len(p.Connections) == 0 {
		out, err := json.Marshal(payload.ConnectionCheckResult{Connections: []payload.ConnectionCheckItemResult{}})
		if err != nil {
			return "", fmt.Errorf("failed to marshal connection check result: %w", err)
		}
		return string(out), nil
	}

	results := make([]payload.ConnectionCheckItemResult, 0, len(p.Connections))
	for _, item := range p.Connections {
		result := checkConnection(item)
		results = append(results, result)
	}

	out, err := json.Marshal(payload.ConnectionCheckResult{Connections: results})
	if err != nil {
		return "", fmt.Errorf("failed to marshal connection check result: %w", err)
	}
	return string(out), nil
}

func checkConnection(item payload.ConnectionCheckItem) payload.ConnectionCheckItemResult {
	result := payload.ConnectionCheckItemResult{
		ConnectionID: item.ConnectionID,
		State:        payload.ConnectionCheckStateUnknown,
	}

	if item.PID == 0 {
		result.State = payload.ConnectionCheckStateUnknown
		result.Message = "no pid available"
		return result
	}

	pidStr := strconv.FormatUint(uint64(item.PID), 10)
	state, msg := verifyPID(pidStr, item.Username, item.ConnectionID)
	result.State = state
	result.Message = msg

	if state == payload.ConnectionCheckStateRunning {
		result.Info = &payload.ConnectionCheckItem{
			ConnectionID: item.ConnectionID,
			Username:     item.Username,
			Host:         item.Host,
			Port:         item.Port,
			Display:      item.Display,
			Protocol:     item.Protocol,
			PID:          item.PID,
			Name:         item.Name,
		}
	}

	return result
}

// verifyPID checks /proc/<pid> to determine if the process is alive, belongs
// to the expected user, and is a VNC-related process.
func verifyPID(pid, username string, connectionID uint) (payload.ConnectionCheckState, string) {
	procDir := filepath.Join("/proc", pid)
	if _, err := os.Stat(procDir); os.IsNotExist(err) {
		return payload.ConnectionCheckStateStopped, "process not found"
	}

	// Verify owner via /proc/<pid>/status.
	if username != "" {
		if ok, msg := verifyProcOwner(pid, username); !ok {
			return payload.ConnectionCheckStateUnknown, msg
		}
	}

	// Verify process is VNC-related via /proc/<pid>/comm or cmdline.
	if ok, msg := verifyProcIsVNC(pid); !ok {
		return payload.ConnectionCheckStateUnknown, msg
	}

	// Optional: check that DCE_CONNECTION_ID in process env matches.
	if connectionID != 0 {
		if conflict, msg := checkConnectionIDConflict(pid, connectionID); conflict {
			return payload.ConnectionCheckStateUnknown, msg
		}
	}

	return payload.ConnectionCheckStateRunning, ""
}

// verifyProcOwner reads /proc/<pid>/status and checks the Uid field.
func verifyProcOwner(pid, username string) (bool, string) {
	statusPath := filepath.Join("/proc", pid, "status")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return false, fmt.Sprintf("cannot read /proc/%s/status: %v", pid, err)
	}

	// Resolve expected UID from the current process (the launcher is already
	// running as the target user after Setuid).
	expectedUID := currentUID()

	for line := range strings.Lines(string(data)) {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		// Uid: real  effective  saved  fs
		if len(fields) < 2 {
			return false, "malformed /proc status Uid line"
		}
		realUID := strings.TrimSpace(fields[1])
		if realUID != expectedUID {
			return false, fmt.Sprintf("process owner uid %s does not match expected uid %s for user %s", realUID, expectedUID, username)
		}
		return true, ""
	}
	return false, "Uid line not found in /proc status"
}

// verifyProcIsVNC checks /proc/<pid>/comm for a known VNC binary name.
func verifyProcIsVNC(pid string) (bool, string) {
	commPath := filepath.Join("/proc", pid, "comm")
	data, err := os.ReadFile(commPath)
	if err != nil {
		return false, fmt.Sprintf("cannot read /proc/%s/comm: %v", pid, err)
	}
	comm := strings.TrimSpace(string(data))
	vncBinaries := []string{"Xvnc", "Xtigervnc", "Xorg", "xorgxrdp"}
	if slices.ContainsFunc(vncBinaries, func(name string) bool { return strings.EqualFold(comm, name) }) {
		return true, ""
	}
	return false, fmt.Sprintf("process comm %q is not a known VNC binary", comm)
}

// checkConnectionIDConflict reads DCE_CONNECTION_ID from /proc/<pid>/environ.
// If the env variable is present and doesn't match, it's a conflict.
func checkConnectionIDConflict(pid string, connectionID uint) (bool, string) {
	environPath := filepath.Join("/proc", pid, "environ")
	data, err := os.ReadFile(environPath)
	if err != nil {
		// Permission denied is acceptable - don't fail the check.
		return false, ""
	}
	for entry := range strings.SplitSeq(string(data), "\x00") {
		if !strings.HasPrefix(entry, "DCE_CONNECTION_ID=") {
			continue
		}
		rawID := strings.TrimPrefix(entry, "DCE_CONNECTION_ID=")
		parsed, err := strconv.ParseUint(strings.TrimSpace(rawID), 10, 64)
		if err != nil {
			continue
		}
		if uint(parsed) != connectionID {
			return true, fmt.Sprintf("DCE_CONNECTION_ID=%d in process env conflicts with expected connectionId=%d", parsed, connectionID)
		}
		return false, ""
	}
	return false, ""
}
