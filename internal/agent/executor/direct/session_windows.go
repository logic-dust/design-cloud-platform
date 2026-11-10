//go:build windows

package direct

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/platform/winsession"
)

func handleSessionControl(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.SessionControl](req)
	if err != nil {
		return "", fmt.Errorf("parse session control payload: %w", err)
	}
	if p.Operation != "stop" {
		return "", fmt.Errorf("unsupported session operation %q (only stop)", p.Operation)
	}
	proto := strings.ToUpper(strings.TrimSpace(p.Protocol))
	user := strings.TrimSpace(p.Username)
	if user == "" {
		return "", fmt.Errorf("username is required")
	}
	if proto != "RDP" {
		return "", fmt.Errorf("unsupported session protocol %q on Windows (only RDP)", p.Protocol)
	}
	n, err := logoffDesktopRDPSessions(ctx, user)
	if err != nil {
		return "", err
	}
	out, mErr := json.Marshal(payload.SessionControlResult{Terminated: n})
	if mErr != nil {
		return "", mErr
	}
	return string(out), nil
}

// logoffDesktopRDPSessions logoffs only full-desktop RDP sessions for username.
// Skips console and RemoteApp/RAIL (rdpshell without explorer).
func logoffDesktopRDPSessions(ctx context.Context, username string) (int, error) {
	sessions, err := listUserSessions(ctx, username)
	if err != nil {
		return 0, err
	}
	var desktop []winsession.Session
	for _, s := range sessions {
		if !winsession.IsRemoteDesktopSessionName(s.Name) {
			continue
		}
		if s.ID == 0 {
			continue
		}
		ok, err := winsession.IsFullDesktop(ctx, s.ID)
		if err != nil || !ok {
			continue
		}
		desktop = append(desktop, s)
	}
	if len(desktop) == 0 {
		return 0, nil
	}
	n := 0
	for _, s := range desktop {
		if err := exec.CommandContext(ctx, "logoff", strconv.Itoa(s.ID)).Run(); err != nil {
			continue
		}
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("logoff failed for %d desktop RDP session(s) of %s", len(desktop), username)
	}
	if n < len(desktop) {
		return n, fmt.Errorf("logoff partially failed for %s: %d/%d desktop sessions", username, n, len(desktop))
	}
	return n, nil
}

func listUserSessions(ctx context.Context, username string) ([]winsession.Session, error) {
	out, err := exec.CommandContext(ctx, "query", "user", username).CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "no user exists") || strings.Contains(lower, "not found") {
			return nil, nil
		}
		out, err = exec.CommandContext(ctx, "query", "user").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("query user: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	want := strings.ToLower(strings.TrimSpace(username))
	var matched []winsession.Session
	for _, s := range winsession.ParseQueryUserSessions(string(out)) {
		if strings.ToLower(s.Username) != want {
			continue
		}
		matched = append(matched, s)
	}
	return matched, nil
}
