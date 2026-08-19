//go:build !windows

package verifier

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

var (
	getExecutablePath = os.Executable
	readFile          = os.ReadFile
	readLink          = os.Readlink
	statFile          = os.Stat
	getParentPID      = os.Getppid
)

var trustedParentExecutableNames = []string{"design-cloud-engine", "dc-engine", "runner"}

var trustedSystemBinDirs = []string{
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
	"/usr/sbin",
	"/sbin",
}

var knownShellParents = []string{"bash", "sh", "zsh", "ksh", "csh", "tcsh", "fish"}

// RuntimeEnvironment performs L1 startup verification (file permissions and caller identity).
// This is the first layer of the 4-layer defense-in-depth security model.
//
// L1 prevents unauthorized processes from invoking launcher by validating:
// - Capability presence (L0, checked first) - guarantees escalation can actually work
// - File permission check (700) - prevents tampering
// - Caller identity verification (UID matching) - prevents unauthorized users
// - Shell launch detection - prevents manual invocation
// - Environment safety check - prevents LD_PRELOAD injection
//
// L1 cannot be replaced by L4 (seccomp) because:
// - seccomp cannot check file permissions
// - seccomp cannot verify caller identity
// - seccomp cannot check parent process
// - seccomp is applied after process starts, L1 rejects before reading request
func RuntimeEnvironment() error {
	// L0 runs first: it is the cheapest check, and it is the one whose absence would
	// otherwise stay invisible until a privileged action fails much later.
	if err := Capabilities(); err != nil {
		return err
	}

	launcherPath, err := getExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to determine launcher path: %w", err)
	}

	launcherStat, err := verifyFilePermissions(launcherPath)
	if err != nil {
		return err
	}

	if err := checkCaller(launcherPath, launcherStat); err != nil {
		return err
	}

	return nil
}

// checkCaller verifies caller UID matches launcher owner
func checkCaller(launcherPath string, launcherStat os.FileInfo) error {
	ppid := getParentPID()

	stat, ok := launcherStat.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fmt.Errorf("failed to extract file ownership stats for launcher")
	}
	launcherOwnerUID := int(stat.Uid)

	ppInfo, err := readParentStatus(ppid)
	if err != nil {
		return err
	}

	if ppInfo.realUID != launcherOwnerUID {
		return fmt.Errorf("caller UID mismatch: got %d, expected %d (launcher owner)", ppInfo.realUID, launcherOwnerUID)
	}

	launcherDir := filepath.Dir(launcherPath)
	writable, err := isDirWritableByCurrentUser(launcherDir)
	if err != nil {
		return fmt.Errorf("failed to check launcher directory safety: %w", err)
	}
	if writable && stat.Nlink > 1 {
		return fmt.Errorf("security violation: launcher cannot be executed via hard link in user-writable directory %s (Nlink=%d)", launcherDir, stat.Nlink)
	}

	if slices.Contains(knownShellParents, ppInfo.comm) {
		return fmt.Errorf("launcher invoked from shell")
	}

	parentExePath, err := readParentExecutable(ppid)
	if err != nil {
		return err
	}

	if err := verifyTrustedParentExecutable(parentExePath, launcherStat); err != nil {
		return err
	}

	return nil
}

type parentStatus struct {
	realUID int
	comm    string
}

// readParentStatus reads /proc/<ppid>/status and extracts the process real UID
// and comm name in a single read, replacing the previous separate readParentRealUID
// + isShellParent pattern to reduce /proc reads from 2 to 1.
func readParentStatus(ppid int) (parentStatus, error) {
	statusPath := fmt.Sprintf("/proc/%d/status", ppid)
	data, err := readFile(statusPath)
	if err != nil {
		return parentStatus{}, fmt.Errorf("identity verification failed: %w", err)
	}

	var info parentStatus
	for line := range strings.Lines(string(data)) {
		if strings.HasPrefix(line, "Uid:") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			uid, err := strconv.Atoi(parts[1])
			if err != nil {
				return parentStatus{}, fmt.Errorf("identity verification failed: %w", err)
			}
			info.realUID = uid
		} else if after, ok := strings.CutPrefix(line, "Name:"); ok {
			info.comm = strings.TrimSpace(after)
		}
	}

	if info.comm == "" {
		return parentStatus{}, fmt.Errorf("cannot find parent process name in /proc/%d/status", ppid)
	}
	return info, nil
}

func readParentExecutable(ppid int) (string, error) {
	exePath := fmt.Sprintf("/proc/%d/exe", ppid)
	resolvedPath, err := readLink(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve parent executable: %w", err)
	}

	return filepath.Clean(resolvedPath), nil
}

func verifyTrustedParentExecutable(parentExePath string, launcherStat os.FileInfo) error {
	cleanParentPath := filepath.Clean(parentExePath)

	parentName := filepath.Base(cleanParentPath)
	parentDir := filepath.Dir(cleanParentPath)

	parentStat, err := statFile(cleanParentPath)
	if err != nil {
		return fmt.Errorf("failed to stat parent executable: %w", err)
	}

	// The parent executable is trusted if it shares the exact same owner as the launcher,
	// or if it resides in a standard trusted system bin directory (e.g., /usr/bin).
	launcherSys, ok1 := launcherStat.Sys().(*syscall.Stat_t)
	parentSys, ok2 := parentStat.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 || launcherSys == nil || parentSys == nil {
		return fmt.Errorf("failed to extract file ownership stats for parent verification")
	}
	launcherUID := launcherSys.Uid
	parentUID := parentSys.Uid

	isOwnerMatch := launcherUID == parentUID
	isInTrustedDir := slices.Contains(trustedSystemBinDirs, parentDir)

	if !isOwnerMatch && !isInTrustedDir {
		return fmt.Errorf("untrusted parent executable directory: %s", parentDir)
	}
	if !slices.Contains(trustedParentExecutableNames, parentName) {
		return fmt.Errorf("untrusted parent executable name: %s", parentName)
	}

	if parentStat.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("parent executable is group/other writable: %04o", parentStat.Mode().Perm())
	}

	return nil
}

// verifyFilePermissions checks launcher file permissions and capabilities (L1)
func verifyFilePermissions(launcherPath string) (os.FileInfo, error) {
	stat, err := statFile(launcherPath)
	if err != nil {
		return nil, fmt.Errorf("file permission check failed: %w", err)
	}

	// 检查文件权限（必须是 owner-only；group/other 不得有任何权限）
	mode := stat.Mode().Perm()
	if mode&0o077 != 0 {
		return nil, fmt.Errorf("launcher has excessive group/other permissions: %04o (expected 700)", mode)
	}

	return stat, nil
}

// isDirWritableByCurrentUser determines if a directory is writable by the current process user.
func isDirWritableByCurrentUser(dir string) (bool, error) {
	info, err := statFile(dir)
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("failed to get syscall stat for directory %s", dir)
	}

	uid := os.Getuid()
	if uid == 0 {
		// Root can write to almost anything
		return true, nil
	}

	// 1. Owner check
	if int(stat.Uid) == uid {
		return info.Mode().Perm()&0o200 != 0, nil
	}

	// 2. Group check
	gids, _ := os.Getgroups()
	gids = append(gids, os.Getgid())
	for _, gid := range gids {
		if int(stat.Gid) == gid {
			if info.Mode().Perm()&0o020 != 0 {
				return true, nil
			}
		}
	}

	// 3. Others check
	return info.Mode().Perm()&0o002 != 0, nil
}
