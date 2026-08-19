// connection_linux.go collects remote-desktop sessions on Linux by scanning
// /proc for VNC (Xvnc/Xtigervnc) and XRDP (xorgxrdp) processes.
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
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var procFSRoot = "/proc"

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
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		uidCacheMu.Lock()
		uidCache[uid] = u.Username
		uidCacheMu.Unlock()
		return u.Username
	}
	return uid
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

// CollectRemoteConnections 获取当前节点上所有活跃的远程桌面（VNC / RDP）连接信息。
// 纯 Go 直接遍历 /proc 读取 cmdline 与 status（零子进程 Fork，耗时 1~2ms）。
// （参考等价命令：ps -C Xvnc,Xtigervnc,xorgxrdp -o pid= -o user:24= -o args=）
func CollectRemoteConnections(ctx context.Context) ([]ConnectionSnapshot, error) {
	entries, err := os.ReadDir(procFSRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", procFSRoot, err)
	}

	connections := make([]ConnectionSnapshot, 0)
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
		isVNC := strings.Contains(argsStr, "Xvnc") || strings.Contains(argsStr, "Xtigervnc")
		isXRDP := strings.Contains(argsStr, "xorgxrdp")
		if !isVNC && !isXRDP {
			continue
		}

		uid := readUIDFromStatus(filepath.Join(pidDir, "status"))
		username := lookupUsernameByUID(uid)

		connSnapshot := parseVNCConnectionDetails(pid, username, argsStr, isXRDP)
		// 过滤由作业包装脚本启动的临时 GUI 仿真会话（以 dce-job 开头），
		// 该类会话生命周期随 Job 结束自动清理，不作为常驻桌面录入 Connections 库。
		if strings.HasPrefix(connSnapshot.ConnectionName, "dce-job") {
			continue
		}
		connections = append(connections, connSnapshot)
	}
	return connections, nil
}

func parseVNCConnectionDetails(pid, username, args string, isXRDP bool) ConnectionSnapshot {
	connSnapshot := ConnectionSnapshot{
		PID:      pid,
		Username: username,
		Protocol: "VNC",
	}
	if isXRDP {
		connSnapshot.Protocol = "RDP"
	}
	connSnapshot.Display = parseDisplayNumber(args)
	connSnapshot.Port = parseVNCPort(args, connSnapshot.Display)

	// Fallback to environ for missing fields (when available).
	env := readProcEnvironFn(connSnapshot.PID)
	connSnapshot.ConnectionName = cmp.Or(
		parseConnectionNameFromArgs(args),
		env["DCE_CONNECTION_NAME"],
		generatedVNCConnectionName(connSnapshot.Username, connSnapshot.Display),
	)
	return connSnapshot
}

// parseVNCPort 提取 VNC 会话的 TCP 端口。
// 优先解析 "-rfbport" 覆盖参数，无此参数时才 fallback 到 (5900 + Display)。
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
	if display > 0 {
		return 5900 + display
	}
	return 0
}

func parseTCPPort(raw string) int {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// parseDisplayNumber extracts the X display number from a Xvnc/Xtigervnc
// command-line string. It only inspects the token immediately following the
// binary name (e.g. ":1" in "Xvnc :1 ...") to avoid false positives from
// host:display strings that may appear in -desktop or other arguments.
func parseDisplayNumber(args string) int {
	fields := strings.Fields(args)
	for i, f := range fields {
		if !strings.HasSuffix(f, "Xvnc") && !strings.HasSuffix(f, "Xtigervnc") && !strings.HasSuffix(f, "Xorg") {
			continue
		}
		if i+1 >= len(fields) {
			return 0
		}
		raw := strings.TrimPrefix(fields[i+1], ":")
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
		return 0
	}
	return 0
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
	if username == "" || display <= 0 {
		return ""
	}
	return fmt.Sprintf("vnc-%s-%d", username, display)
}

var readProcEnvironFn = readProcEnviron

// readProcEnviron 读取进程的环境变量。
// 在direct模式下，如果当前进程没有足够权限读取其他用户的/proc/{pid}/environ，
// 将返回空map（这是正常情况，metadata收集使用分层fallback机制）。
// 注意：要获取完整的跨用户环境变量信息，需要以更高权限（root）执行。
func readProcEnviron(pid string) map[string]string {
	path := filepath.Join("/proc", pid, "environ")
	data, err := os.ReadFile(path)
	if err != nil {
		// 权限不足（direct模式）或文件不存在时返回空map
		// 不需要记录错误，这是预期的fallback行为
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

// CollectRDPReadiness on Linux always reports an indeterminate probe: remote desktops
// surface as VNC/xrdp connections (see CollectRemoteConnections), so there is no
// RDP-specific readiness state to report.
func CollectRDPReadiness() RDPReadiness {
	return RDPReadiness{Reason: "RDP readiness probe is not applicable on linux"}
}
