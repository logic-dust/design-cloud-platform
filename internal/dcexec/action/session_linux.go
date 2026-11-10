//go:build !windows

package action

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/platform/osuser"
)

// HandleSessionControl stops the already-dropped target user's own SSH login
// sessions. PrivilegeTargetUser — never root scan/kill of other accounts.
// Only Service=sshd/ssh loginctl sessions; never terminate-user (would clobber XRDP).
func HandleSessionControl(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
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
	if err := assertSessionControlSelf(req, user); err != nil {
		return "", err
	}
	switch proto {
	case "SSH":
		n, err := terminateOwnSSHSessions(ctx, user)
		if err != nil {
			return "", err
		}
		out, mErr := json.Marshal(payload.SessionControlResult{Terminated: n})
		if mErr != nil {
			return "", mErr
		}
		return string(out), nil
	case "RDP":
		return "", fmt.Errorf("RDP session stop is not supported on Linux PrivilegedExec (use Windows agent)")
	default:
		return "", fmt.Errorf("unsupported session protocol %q", p.Protocol)
	}
}

func assertSessionControlSelf(req *execution.ExecuteRequest, username string) error {
	target := strings.TrimSpace(req.TargetUser)
	if target == "" {
		return fmt.Errorf("targetUser is required for SESSION_CONTROL")
	}
	if !strings.EqualFold(target, username) {
		return fmt.Errorf("SESSION_CONTROL username %q must match targetUser %q", username, target)
	}

	// Verify dropped identity against target user via osuser (NSS / SSSD compatible)
	identity, err := osuser.ResolveUserIdentity(username)
	if err == nil {
		if curUID := os.Geteuid(); curUID != identity.UID {
			return fmt.Errorf("SESSION_CONTROL must run as %s (uid %d), current euid is %d", username, identity.UID, curUID)
		}
	}
	return nil
}

func terminateOwnSSHSessions(ctx context.Context, username string) (int, error) {
	out, err := exec.CommandContext(ctx, "loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		// Without loginctl, a non-root user cannot reliably kill sshd leaders; succeed as no-op
		// only when we cannot list (tool missing). Prefer explicit empty.
		return 0, nil
	}
	matched := 0
	terminated := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		sid := fields[0]
		user := fields[2]
		if !strings.EqualFold(user, username) {
			continue
		}
		if !sessionLooksLikeSSH(ctx, sid) {
			continue
		}
		matched++
		if err := exec.CommandContext(ctx, "loginctl", "terminate-session", sid).Run(); err != nil {
			continue
		}
		terminated++
	}
	if matched > 0 && terminated == 0 {
		return 0, fmt.Errorf("failed to terminate %d ssh session(s) for %s", matched, username)
	}
	return terminated, nil
}

func sessionLooksLikeSSH(ctx context.Context, sid string) bool {
	show, err := exec.CommandContext(ctx, "loginctl", "show-session", sid, "-p", "Service").Output()
	if err != nil {
		return false
	}
	service := ""
	for line := range strings.SplitSeq(string(show), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if k == "Service" {
			service = strings.ToLower(v)
		}
	}
	return strings.Contains(service, "sshd") || strings.Contains(service, "ssh")
}
