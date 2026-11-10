//go:build linux

package probe

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/platform/procfs"
)

func enrichXRDPSeatsSessionEnding(ctx context.Context, seats []XRDPSeatSnapshot) []XRDPSeatSnapshot {
	if len(seats) == 0 {
		return seats
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	out := make([]XRDPSeatSnapshot, len(seats))
	for i, seat := range seats {
		seat.SessionEndingConfirmed = isXRDPSeatSessionEnding(ctx, seat.PID, seat.Display)
		out[i] = seat
	}
	return out
}

// isXRDPSeatSessionEnding: X 席位仍在，但 logind 已无对应会话或会话处于 closing。
// 断线保活时 session 仍在且非 closing，不会命中。loginctl 不可用时不判定。
func isXRDPSeatSessionEnding(ctx context.Context, pid string, display int) bool {
	pid = strings.TrimSpace(pid)
	if pid == "" || display < 0 {
		return false
	}

	if sid := sessionIDFromPIDCgroup(pid); sid != "" {
		state, ok := loginctlSessionState(ctx, sid)
		if !ok {
			return true
		}
		return strings.EqualFold(strings.TrimSpace(state), "closing")
	}

	sid, listOK := sessionIDByLoginctlDisplay(ctx, display, pid)
	if !listOK {
		return false
	}
	if sid == "" {
		// X 还在，loginctl 能列会话但对不上这个席位 → Logout 残留孤儿
		return true
	}
	state, ok := loginctlSessionState(ctx, sid)
	if !ok {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(state), "closing")
}

func sessionIDFromPIDCgroup(pid string) string {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return ""
	}
	return procfs.SessionIDFromPID(n)
}

// sessionIDByLoginctlDisplay returns (sid, listOK). listOK=false means loginctl failed.
func sessionIDByLoginctlDisplay(ctx context.Context, display int, pid string) (string, bool) {
	out, err := exec.CommandContext(ctx, "loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return "", false
	}
	displayStr := fmt.Sprintf(":%d", display)
	pidStr := strings.TrimSpace(pid)
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		sid := fields[0]
		show, err := exec.CommandContext(ctx, "loginctl", "show-session", sid,
			"-p", "Display", "-p", "Leader").Output()
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
				if v == displayStr {
					matchedDisplay = true
				}
			case "Leader":
				if pidStr != "" && v == pidStr {
					matchedLeader = true
				}
			}
		}
		if matchedDisplay || matchedLeader {
			return sid, true
		}
	}
	return "", true
}

func loginctlSessionState(ctx context.Context, sid string) (state string, ok bool) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return "", false
	}
	out, err := exec.CommandContext(ctx, "loginctl", "show-session", sid, "-p", "State").Output()
	if err != nil {
		return "", false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		k, v, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && k == "State" {
			return v, true
		}
	}
	return "", false
}
