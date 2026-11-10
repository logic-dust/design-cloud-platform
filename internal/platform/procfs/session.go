// Package procfs reads /proc process metadata used by the agent and dc-exec.
package procfs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseSessionIDFromCgroup returns the systemd session id in a cgroup file,
// or an empty string when no session-*.scope entry is present.
func ParseSessionIDFromCgroup(content string) string {
	for line := range strings.SplitSeq(content, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		for elem := range strings.SplitSeq(parts[2], "/") {
			if s, ok := strings.CutPrefix(elem, "session-"); ok {
				if sid, ok := strings.CutSuffix(s, ".scope"); ok && sid != "" {
					return sid
				}
			}
		}
	}
	return ""
}

// SessionIDFromPID returns the systemd session id for pid, then for its
// parent when the process itself has no session scope (parent must be > 1).
func SessionIDFromPID(pid int) string {
	if sid := sessionIDFromProcCgroup(pid); sid != "" {
		return sid
	}
	if ppid := parentPID(pid); ppid > 1 {
		return sessionIDFromProcCgroup(ppid)
	}
	return ""
}

func sessionIDFromProcCgroup(pid int) string {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	return ParseSessionIDFromCgroup(string(raw))
}

func parentPID(pid int) int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ppid, _ := strconv.Atoi(fields[1])
				return ppid
			}
		}
	}
	return 0
}
