package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// systemRuntimeDir is the FHS runtime directory created by the RPM tmpfiles.d /
// systemd RuntimeDirectory= for the design-cloud service account.
const systemRuntimeDir = "/run/design-cloud"

// MkdirPrivate creates a directory (and any missing parents) with 0700 permissions.
// The Chmod call enforces 0700 even if the directory already exists with wrong permissions.
func MkdirPrivate(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	// Chmod covers the repair case: MkdirAll skips permission update on existing dirs.
	if err := os.Chmod(path, 0700); err != nil {
		return fmt.Errorf("failed to set directory permissions %s: %w", path, err)
	}
	return nil
}

// MkdirShared creates a directory (and any missing parents) with 0755 permissions.
// Semantics: a "public asset directory" on shared filesystems that any node can
// traverse (o+x) and list (o+r). Chmod repairs directories created with wrong perms.
//
// Runtime red line: certs/cluster must always be created through this function.
// MkdirPrivate forces 0700 and would break o+x traversal for non-owner nodes.
func MkdirShared(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	// Chmod covers the repair case: MkdirAll skips permission update on existing dirs.
	if err := os.Chmod(path, 0755); err != nil {
		return fmt.Errorf("failed to set directory permissions %s: %w", path, err)
	}
	return nil
}

// LauncherTokenPath returns the runtime path of the launcher authentication token.
func LauncherTokenPath() string {
	return filepath.Join(RuntimeDir(), launcherTokenFn)
}

// PIDFilePath returns the runtime path of the engine process lock file.
func PIDFilePath() string {
	return filepath.Join(RuntimeDir(), pidFileName)
}

// RuntimeDir returns the private node-local runtime directory used to store
// agent/launcher coordination state such as tokens and pid files.
//
// Resolution (first match wins):
//  1. $RUNTIME_DIRECTORY              (systemd RuntimeDirectory=; unit also sets explicitly for CentOS 7)
//  2. /run/design-cloud                (if owned by the current user)
//  3. /run/user/<uid>/design-cloud-engine  (interactive / logind session)
//  4. /tmp/design-cloud-engine-<uid>   (last-resort fallback)
func RuntimeDir() string {
	if dir := os.Getenv("RUNTIME_DIRECTORY"); dir != "" {
		return dir
	}

	if ownedBySelf(systemRuntimeDir) {
		return systemRuntimeDir
	}

	uid := strconv.Itoa(os.Getuid())
	userRun := filepath.Join("/run/user", uid)
	if info, err := os.Stat(userRun); err == nil && info.IsDir() {
		return filepath.Join(userRun, runtimeDirPrefix)
	}

	return filepath.Join("/tmp", fmt.Sprintf("%s-%s", runtimeDirPrefix, uid))
}

func ownedBySelf(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return false
	}
	return int(stat.Uid) == os.Getuid()
}

// VerifyPrivateDir checks that path is an existing directory with exactly 0700
// permissions and is owned by the current user.
func VerifyPrivateDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return verifyOwnerAndPerm(path, info, 0700)
}

// VerifyPrivateFileDescriptor checks that f is an open file with exactly 0600
// permissions and is owned by the current user.
func VerifyPrivateFileDescriptor(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat open file %s: %w", f.Name(), err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", f.Name())
	}
	return verifyOwnerAndPerm(f.Name(), info, 0600)
}

// verifyOwnerAndPerm is the shared owner/permission check for secure
// runtime filesystem helpers.
func verifyOwnerAndPerm(path string, info os.FileInfo, expectedPerm os.FileMode) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get system info for %s", path)
	}

	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("security violation: %s is not owned by current user (got uid %d, expected %d)",
			path, stat.Uid, os.Getuid())
	}

	actualPerm := info.Mode().Perm()
	if actualPerm != expectedPerm {
		return fmt.Errorf("security violation: %s has insecure permissions %04o (expected %04o)",
			path, actualPerm, expectedPerm)
	}

	return nil
}

// WritePrivateFile atomically writes data to a file with 0600 permissions,
// protecting against pre-creation attacks and mid-write permission windows.
// Uses a temp file + rename pattern to ensure atomicity and correct perms.
func WritePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file := filepath.Base(path)

	// Create temp file in the same directory to guarantee same filesystem.
	tmpFile, err := os.CreateTemp(dir, "."+file+".tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }() // Clean up on error

	// Write data to temp file.
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set strict permissions before rename (prevent window attack).
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}

	// Atomic rename to target.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}

	return nil
}

// WriteSharedFile atomically writes a "public read-only asset" (ca.crt, node
// certificates) with 0644 permissions. Symmetric to WritePrivateFile: same-dir
// temp file → Chmod 0644 → Rename. Private assets (keys) must keep using
// WritePrivateFile (0600).
func WriteSharedFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file := filepath.Base(path)

	tmpFile, err := os.CreateTemp(dir, "."+file+".tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0644); err != nil {
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}

	return nil
}
