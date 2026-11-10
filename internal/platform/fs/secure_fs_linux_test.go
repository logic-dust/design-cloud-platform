//go:build !windows

package fs

import (
	"os"
	"path/filepath"
	"testing"
)

// POSIX 侧的私有性由权限位保证，故在非 Windows 平台上单独断言 0700/0600。
func TestPrivatePermissions_POSIXModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat private dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %04o, want 0700", got)
	}

	path := filepath.Join(dir, "secret")
	if err := WritePrivateFile(path, []byte("payload")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %04o, want 0600", got)
	}
}

// 公共资产走独立语义：目录 0755（可穿透）、文件 0644。
// 且 MkdirShared 必须能把历史遗留的 0700 目录修复回 0755（NFS 死锁修复路径）。
func TestSharedPermissions_POSIXModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := MkdirShared(dir); err != nil {
		t.Fatalf("MkdirShared: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat shared dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("dir mode = %04o, want 0755", got)
	}

	// 修复路径：0700 目录应被 MkdirShared 拉回 0755。
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod setup: %v", err)
	}
	if err := MkdirShared(dir); err != nil {
		t.Fatalf("MkdirShared repair: %v", err)
	}
	info, err = os.Stat(dir)
	if err != nil {
		t.Fatalf("stat repaired dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("repaired dir mode = %04o, want 0755", got)
	}

	path := filepath.Join(dir, "ca.crt")
	if err := WriteSharedFile(path, []byte("cert")); err != nil {
		t.Fatalf("WriteSharedFile: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat shared file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("file mode = %04o, want 0644", got)
	}
}

func TestRuntimeDir_PrefersRuntimeDirectoryEnv(t *testing.T) {
	want := t.TempDir()
	t.Setenv("RUNTIME_DIRECTORY", want)
	if got := RuntimeDir(); got != want {
		t.Fatalf("RuntimeDir() = %q, want %q", got, want)
	}
}
