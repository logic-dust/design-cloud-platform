// connection_linux.go collects VNC and XRDP connection seats on Linux by scanning /proc.
//
// Collect does NOT perform business-logic reconciliation (that belongs in
// service/connection). It is the agent's "self-awareness" layer, analogous
// to heartbeat or health probes.
package probe

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var procFSRoot = "/proc"

const uidCacheMaxEntries = 4096

var (
	uidCacheMu sync.RWMutex
	uidCache   = make(map[string]string)
)

func lookupUsernameByUID(uid string) string {
	if uid == "" {
		return ""
	}
	uidCacheMu.RLock()
	name, ok := uidCache[uid]
	uidCacheMu.RUnlock()
	if ok {
		return name
	}

	// Always resolve via system `id -un <uid>` (NSS / SSSD / LDAP compatible)
	if resolved := lookupUsernameViaID(uid); resolved != "" {
		uidCacheMu.Lock()
		if len(uidCache) >= uidCacheMaxEntries {
			// Bound growth under long-lived agents; NSS lookup is cheap enough to refill.
			clear(uidCache)
		}
		uidCache[uid] = resolved
		uidCacheMu.Unlock()
		return resolved
	}

	return uid
}

func lookupUsernameViaID(uid string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	idPath := "/usr/bin/id"
	if _, err := os.Stat(idPath); err != nil {
		idPath = "/bin/id"
		if _, err := os.Stat(idPath); err != nil {
			idPath = "id"
		}
	}

	cmd := exec.CommandContext(ctx, idPath, "-un", uid)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	name := strings.TrimSpace(string(out))
	if name != "" && !strings.ContainsAny(name, " \t\r\n") {
		return name
	}
	return ""
}

func readUIDFromStatus(statusPath string) string {
	data, err := os.ReadFile(statusPath)
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

// CollectConnectionRuntime traverses /proc once to collect active VNC desktops
// and XRDP X seats.
func CollectConnectionRuntime(ctx context.Context) (ConnectionSnapshot, error) {
	entries, err := os.ReadDir(procFSRoot)
	if err != nil {
		return ConnectionSnapshot{}, fmt.Errorf("read %s: %w", procFSRoot, err)
	}

	vncSeats := make([]VNCSeatSnapshot, 0)
	xrdpSeats := make([]XRDPSeatSnapshot, 0)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid := entry.Name()
		if len(pid) == 0 || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		isNum := true
		for i := 1; i < len(pid); i++ {
			if pid[i] < '0' || pid[i] > '9' {
				isNum = false
				break
			}
		}
		if !isNum {
			continue
		}

		pidDir := filepath.Join(procFSRoot, pid)
		cmdlineData, err := os.ReadFile(filepath.Join(pidDir, "cmdline"))
		if err != nil || len(cmdlineData) == 0 {
			continue
		}

		tokens := bytes.Split(cmdlineData, []byte{0})
		args := make([]string, 0, len(tokens))
		for _, token := range tokens {
			if len(token) > 0 {
				args = append(args, string(token))
			}
		}
		if len(args) == 0 {
			continue
		}

		argsStr := strings.Join(args, " ")
		uid := readUIDFromStatus(filepath.Join(pidDir, "status"))
		username := lookupUsernameByUID(uid)

		if isVNCProcess(argsStr) {
			vncSnapshot, ok := parseVNCSeatDetails(pid, username, argsStr)
			if !ok {
				continue
			}
			if shouldSkipConnectionSeat(pid, vncSnapshot.ConnectionName) {
				continue
			}
			vncSeats = append(vncSeats, vncSnapshot)
			continue
		}

		if isXRDPProcess(argsStr) {
			xrdpSnapshot, ok := parseXRDPSeatDetails(pid, username, argsStr)
			if !ok {
				continue
			}
			if shouldSkipConnectionSeat(pid, xrdpSnapshot.ConnectionName) {
				continue
			}
			xrdpSeats = append(xrdpSeats, xrdpSnapshot)
		}
	}

	xrdpSeats = enrichXRDPSeatsSessionEnding(ctx, xrdpSeats)

	return ConnectionSnapshot{
		VNCSeats:    vncSeats,
		XRDPSeats:   xrdpSeats,
		RDPSessions: collectRDPDesktopSessions(ctx),
	}, nil
}

func isVNCProcess(argsStr string) bool {
	return strings.Contains(argsStr, "Xvnc") || strings.Contains(argsStr, "Xtigervnc")
}

// isXRDPProcess detects xrdp-managed X servers. Never matches the system
// xrdp/xrdp-sesman daemons — only the per-user Xorg/xorgxrdp seat.
func isXRDPProcess(argsStr string) bool {
	lower := strings.ToLower(argsStr)
	if strings.Contains(lower, "xorgxrdp") {
		return true
	}
	// Typical: /usr/libexec/Xorg :10 -config xrdp/xorg.conf ...
	if (strings.Contains(argsStr, "Xorg") || strings.Contains(lower, "/xorg ")) && strings.Contains(lower, "xrdp") {
		return true
	}
	return false
}

func parseXRDPSeatDetails(pid, username, args string) (XRDPSeatSnapshot, bool) {
	display, ok := parseDisplayNumber(args)
	if !ok {
		return XRDPSeatSnapshot{}, false
	}
	return XRDPSeatSnapshot{
		PID:            pid,
		Username:       username,
		ConnectionName: generatedXRDPConnectionName(username, display),
		Display:        display,
		StartedAt:      readProcessStartTime(pid),
	}, true
}

func readProcessStartTime(pid string) int64 {
	fi, err := os.Stat(filepath.Join(procFSRoot, pid))
	if err != nil {
		return 0
	}
	return fi.ModTime().Unix()
}

func generatedXRDPConnectionName(username string, display int) string {
	username = strings.TrimSpace(username)
	if username == "" || display < 0 {
		return ""
	}
	return fmt.Sprintf("xrdp-%s-%d", username, display)
}

func parseVNCSeatDetails(pid, username, args string) (VNCSeatSnapshot, bool) {
	connSnapshot := VNCSeatSnapshot{
		PID:       pid,
		Username:  username,
		StartedAt: readProcessStartTime(pid),
	}
	display, ok := parseDisplayNumber(args)
	if !ok {
		return VNCSeatSnapshot{}, false
	}
	connSnapshot.Display = display
	connSnapshot.Port = parseVNCPort(args, connSnapshot.Display)
	connSnapshot.ConnectionName = cmp.Or(
		parseConnectionNameFromArgs(args),
		generatedVNCConnectionName(connSnapshot.Username, connSnapshot.Display),
	)
	return connSnapshot, true
}

// parseVNCPort 优先解析命令行上的 -rfbport；未显式携带时按 5900+display 计算。
func parseVNCPort(args string, display int) int {
	fields := strings.Fields(args)
	for idx, token := range fields {
		token = strings.TrimSpace(token)
		if token == "-rfbport" && idx+1 < len(fields) {
			if port := parseTCPPort(fields[idx+1]); port > 0 {
				return port
			}
			continue
		}
		if after, ok := strings.CutPrefix(token, "-rfbport="); ok {
			if port := parseTCPPort(after); port > 0 {
				return port
			}
		}
	}
	if display >= 0 {
		return 5900 + display
	}
	return 0
}

// shouldSkipConnectionSeat excludes seats that must not enter connections:
// - dce-app* name (VNC -desktop) — app / job GUI seats
// plus heuristic job runtime markers (Slurm/LSF env/cgroup) when the process is
// clearly under a batch step even without the name prefix.
func shouldSkipConnectionSeat(pid, connectionName string) bool {
	if isSkippedConnectionName(connectionName) {
		return true
	}
	env := readProcEnviron(pid)
	if strings.TrimSpace(env["LSB_JOBID"]) != "" || strings.TrimSpace(env["SLURM_JOB_ID"]) != "" {
		return true
	}
	switch readParentComm(pid) {
	case "slurmstepd", "slurmd", "slurmctld", "sbatchd", "mbatchd", "pim":
		return true
	}
	cg := strings.ToLower(readCgroup(pid))
	return strings.Contains(cg, "slurm") || strings.Contains(cg, "lsf")
}

// isSkippedConnectionName is the explicit naming contract: desktop / connection
// name starting with dce-app is never adopted into portal connections.
func isSkippedConnectionName(name string) bool {
	return strings.HasPrefix(name, "dce-app")
}

func readParentComm(pid string) string {
	data, err := os.ReadFile(filepath.Join(procFSRoot, pid, "status"))
	if err != nil {
		return ""
	}
	ppid := ""
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ppid = fields[1]
			}
			break
		}
	}
	if ppid == "" || ppid == "0" {
		return ""
	}
	comm, err := os.ReadFile(filepath.Join(procFSRoot, ppid, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}

func readCgroup(pid string) string {
	data, err := os.ReadFile(filepath.Join(procFSRoot, pid, "cgroup"))
	if err != nil {
		return ""
	}
	return string(data)
}

func parseTCPPort(raw string) int {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// parseDisplayNumber extracts the X display number from a Xvnc/Xtigervnc/Xorg/xorgxrdp command line.
func parseDisplayNumber(args string) (int, bool) {
	fields := strings.Fields(args)
	for i, f := range fields {
		if !strings.HasSuffix(f, "Xvnc") && !strings.HasSuffix(f, "Xtigervnc") && !strings.HasSuffix(f, "Xorg") && !strings.HasSuffix(f, "xorgxrdp") {
			continue
		}
		if i+1 >= len(fields) {
			return 0, false
		}
		raw := strings.TrimPrefix(fields[i+1], ":")
		if raw == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func parseConnectionNameFromArgs(args string) string {
	fields := strings.Fields(args)
	for idx, token := range fields {
		if token != "-rfbauth" || idx+1 >= len(fields) {
			continue
		}
		authPath := fields[idx+1]
		if filepath.Base(authPath) != "passwd" {
			continue
		}
		name := filepath.Base(filepath.Dir(authPath))
		if name != "" && name != ".vnc" {
			return name
		}
	}
	if desktop := parseDesktopName(args); desktop != "" {
		return desktop
	}
	return ""
}

func parseDesktopName(args string) string {
	fields := strings.Fields(args)
	for idx, token := range fields {
		if token != "-desktop" || idx+1 >= len(fields) {
			continue
		}
		values := make([]string, 0, 4)
		for _, part := range fields[idx+1:] {
			if strings.HasPrefix(part, "-") {
				break
			}
			values = append(values, part)
		}
		return strings.TrimSpace(strings.Join(values, " "))
	}
	return ""
}

func generatedVNCConnectionName(username string, display int) string {
	username = strings.TrimSpace(username)
	if username == "" || display < 0 {
		return ""
	}
	return fmt.Sprintf("vnc-%s-%d", username, display)
}

func readProcEnviron(pid string) map[string]string {
	path := filepath.Join(procFSRoot, pid, "environ")
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}

	env := make(map[string]string)
	for entry := range bytes.SplitSeq(data, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, ok := bytes.Cut(entry, []byte("="))
		if !ok {
			continue
		}
		env[string(key)] = string(value)
	}
	return env
}
