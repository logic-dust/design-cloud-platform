//go:build !windows

package verifier

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
)

type mockFileInfo struct {
	os.FileInfo
	name string
	mode os.FileMode
	sys  any
}

func (m mockFileInfo) Name() string      { return m.name }
func (m mockFileInfo) Mode() os.FileMode { return m.mode }
func (m mockFileInfo) IsDir() bool       { return m.mode.IsDir() }
func (m mockFileInfo) Sys() any          { return m.sys }

func TestIsDirWritableByCurrentUser(t *testing.T) {
	if os.Getuid() == 0 {
		// isDirWritableByCurrentUser 对 root 有意短路为 true（root 可写几乎所有目录），
		// 本用例的权限位语义只在非 root 身份下成立。
		t.Skip("permission-bit semantics are not observable as root")
	}

	origStatFile := statFile
	defer func() { statFile = origStatFile }()

	currentUID := os.Getuid()
	currentGID := os.Getgid()

	tests := []struct {
		name       string
		dirUID     int
		dirGID     int
		dirMode    os.FileMode
		wantResult bool
	}{
		{
			name:       "owned by user, writable",
			dirUID:     currentUID,
			dirGID:     currentGID,
			dirMode:    os.ModeDir | 0o700,
			wantResult: true,
		},
		{
			name:       "owned by user, not writable",
			dirUID:     currentUID,
			dirGID:     currentGID,
			dirMode:    os.ModeDir | 0o500,
			wantResult: false,
		},
		{
			name:       "owned by other, group writable (same group)",
			dirUID:     currentUID + 1,
			dirGID:     currentGID,
			dirMode:    os.ModeDir | 0o720,
			wantResult: true,
		},
		{
			name:       "owned by other, group not writable (same group)",
			dirUID:     currentUID + 1,
			dirGID:     currentGID,
			dirMode:    os.ModeDir | 0o750,
			wantResult: false,
		},
		{
			name:       "owned by other, others writable",
			dirUID:     currentUID + 1,
			dirGID:     currentGID + 1,
			dirMode:    os.ModeDir | 0o777,
			wantResult: true,
		},
		{
			name:       "owned by other, others not writable",
			dirUID:     currentUID + 1,
			dirGID:     currentGID + 1,
			dirMode:    os.ModeDir | 0o755,
			wantResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStat := &syscall.Stat_t{
				Uid: uint32(tc.dirUID),
				Gid: uint32(tc.dirGID),
			}
			statFile = func(name string) (os.FileInfo, error) {
				return mockFileInfo{
					name: name,
					mode: tc.dirMode,
					sys:  mockStat,
				}, nil
			}

			got, err := isDirWritableByCurrentUser("/dummy/dir")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantResult {
				t.Errorf("got %v, want %v", got, tc.wantResult)
			}
		})
	}
}

func TestCheckCaller_HardLink(t *testing.T) {
	origStatFile := statFile
	origReadFile := readFile
	origGetParentPID := getParentPID
	origReadLink := readLink
	defer func() {
		statFile = origStatFile
		readFile = origReadFile
		getParentPID = origGetParentPID
		readLink = origReadLink
	}()

	currentUID := os.Getuid()

	// Mock parent PID
	getParentPID = func() int { return 9999 }

	// Mock status file read for parent process (returns both Uid and Name)
	readFile = func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "status") {
			return fmt.Appendf(nil, "Name:\trunner\nUid:\t%d\t%d\t%d\t%d\n", currentUID, currentUID, currentUID, currentUID), nil
		}
		return nil, os.ErrNotExist
	}

	// Mock readLink for parent exe path
	readLink = func(name string) (string, error) {
		return "/usr/bin/runner", nil
	}

	// Mock statFile for directory and parent path
	statFile = func(name string) (os.FileInfo, error) {
		if name == "/usr/bin" {
			// Directory is writable by current user (insecure)
			return mockFileInfo{
				name: name,
				mode: os.ModeDir | 0o700,
				sys: &syscall.Stat_t{
					Uid: uint32(currentUID),
				},
			}, nil
		}
		if name == "/usr/bin/runner" {
			return mockFileInfo{
				name: name,
				mode: 0o755,
				sys: &syscall.Stat_t{
					Uid: uint32(currentUID),
				},
			}, nil
		}
		return nil, os.ErrNotExist
	}

	// Test Case 1: Hard link count = 1 (safe)
	{
		dcexecStat := mockFileInfo{
			name: "/usr/bin/dc-exec",
			mode: 0o700,
			sys: &syscall.Stat_t{
				Uid:   uint32(currentUID),
				Nlink: 1,
			},
		}
		err := checkCaller("/usr/bin/dc-exec", dcexecStat)
		if err != nil {
			t.Errorf("expected no error for Nlink=1, got: %v", err)
		}
	}

	// Test Case 2: Hard link count > 1 (unsafe)
	{
		dcexecStat := mockFileInfo{
			name: "/usr/bin/dc-exec",
			mode: 0o700,
			sys: &syscall.Stat_t{
				Uid:   uint32(currentUID),
				Nlink: 2,
			},
		}
		err := checkCaller("/usr/bin/dc-exec", dcexecStat)
		if err == nil {
			t.Error("expected error for Nlink=2 in user-writable directory, got nil")
		} else if !strings.Contains(err.Error(), "security violation: PrivilegedExec cannot be executed via hard link") {
			t.Errorf("unexpected error message: %v", err)
		}
	}
}
