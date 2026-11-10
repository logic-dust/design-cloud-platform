// Package winsession parses Windows query-user session listings and identifies
// full-desktop Remote Desktop sessions.
package winsession

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Session is one row from `query user`.
type Session struct {
	Username string
	Name     string
	ID       int
}

// ParseQueryUserSessions extracts sessions from `query user` output.
func ParseQueryUserSessions(output string) []Session {
	var out []Session
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "username") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimPrefix(fields[0], ">")
		sessName := ""
		id := -1
		for i, cand := range fields[1:] {
			if n, err := strconv.Atoi(cand); err == nil && n >= 0 {
				id = n
				if i > 0 {
					sessName = fields[1]
				}
				break
			}
		}
		if id < 0 {
			continue
		}
		out = append(out, Session{Username: name, Name: sessName, ID: id})
	}
	return out
}

// IsRemoteDesktopSessionName reports whether name is an RDP session.
// A blank name is treated as a candidate because disconnected sessions
// in `query user` leave the SESSIONNAME column empty.
func IsRemoteDesktopSessionName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return true
	}
	return strings.HasPrefix(n, "rdp-tcp") || strings.HasPrefix(n, "rdp-udp")
}

// HasImage reports whether the session has a process with the given image
// name. A tasklist non-zero exit (no match) is treated as absent.
func HasImage(ctx context.Context, sessionID int, image string) (bool, error) {
	out, err := exec.CommandContext(ctx, "tasklist",
		"/FI", fmt.Sprintf("SESSION eq %d", sessionID),
		"/FI", fmt.Sprintf("IMAGENAME eq %s", image),
		"/NH",
	).CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(image)), nil
}

// IsFullDesktop reports whether the session runs explorer.exe (a full desktop)
// rather than RemoteApp (rdpshell/rdpinit without explorer). A session with
// neither image is not treated as a full desktop.
func IsFullDesktop(ctx context.Context, sessionID int) (bool, error) {
	hasExplorer, err := HasImage(ctx, sessionID, "explorer.exe")
	if err != nil {
		return false, err
	}
	if hasExplorer {
		return true, nil
	}
	hasShell, err := HasImage(ctx, sessionID, "rdpshell.exe")
	if err != nil {
		return false, err
	}
	hasInit, err := HasImage(ctx, sessionID, "rdpinit.exe")
	if err != nil {
		return false, err
	}
	if hasShell || hasInit {
		return false, nil
	}
	return false, nil
}
