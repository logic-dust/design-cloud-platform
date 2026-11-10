package fs

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
)

// MkdirPrivate creates a directory (and any missing parents) and hardens its DACL so that
// only SYSTEM and Administrators can access it — the Windows analogue of 0700. The DACL is
// rewritten even when the directory already exists, repairing too-permissive directories.
func MkdirPrivate(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	if err := HardenPath(path, true); err != nil {
		return fmt.Errorf("failed to harden directory %s: %w", path, err)
	}
	return nil
}

// MkdirShared creates a directory (and any missing parents) without tightening its DACL.
// On Windows the ACL equivalent of "public read-only" is inherited from the parent
// (e.g. %PROGRAMDATA%); privacy for sensitive paths is enforced exclusively by HardenPath.
func MkdirShared(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

// DcExecTokenPath returns the runtime path of the PrivilegedExec authentication token.
func DcExecTokenPath() string {
	return filepath.Join(RuntimeDir(), dcExecTokenFn)
}

// PIDFilePath returns the runtime path of the engine process lock file.
func PIDFilePath() string {
	return filepath.Join(RuntimeDir(), pidFileName)
}

// RuntimeDir returns the private node-local runtime directory for Windows.
// Uses %PROGRAMDATA%\DesignCloudEngine as the standard location.
func RuntimeDir() string {
	base := cmp.Or(os.Getenv("PROGRAMDATA"), `C:\ProgramData`)
	return filepath.Join(base, "DesignCloudEngine")
}

// VerifyPrivateDir checks that path is an existing directory whose Owner and DACL keep
// read/write/control access limited to trusted principals (POSIX bits carry no meaning on Windows).
func VerifyPrivateDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return VerifyPathACL(path)
}

// VerifyPrivateFileDescriptor checks that f is an open file whose Owner and DACL keep
// read/write/control access limited to trusted principals.
func VerifyPrivateFileDescriptor(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat open file %s: %w", f.Name(), err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", f.Name())
	}
	return VerifyPathACL(f.Name())
}

// WritePrivateFile atomically writes data to a file on Windows using a temp file + rename
// pattern. The temp file is hardened before the rename, so the secret never exists with an
// inherited (potentially permissive) DACL.
func WritePrivateFile(path string, data []byte) error {
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

	if err := HardenPath(tmpPath, false); err != nil {
		return fmt.Errorf("failed to harden temp file for %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}
	return nil
}

// WriteSharedFile atomically writes a "public read-only asset" (ca.crt, node
// certificates) on Windows using a temp file + rename pattern. No DACL tightening
// is applied: public assets inherit the directory ACL, and privacy is enforced by
// HardenPath for the directories that need it.
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

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}
	return nil
}
