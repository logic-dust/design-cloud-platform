//go:build linux

package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

const zeroCapStatus = "Name:\tdc-exec\nCapInh:\t0000000000000000\nCapPrm:\t0000000000000000\nCapEff:\t0000000000000000\nCapBnd:\t000001ffffffffff\nCapAmb:\t0000000000000000\n"

type allThreadsCall struct{ trap, a1, a2 uintptr }

func stubPrivilegeSyscalls(t *testing.T, status map[string]string, result func(call allThreadsCall) syscall.Errno) *[]allThreadsCall {
	t.Helper()
	oldAll, oldRead, oldTid, oldAmb := allThreadsSyscallFn, readProcStatusFn, gettidFn, clearAmbientCapsFn
	t.Cleanup(func() {
		allThreadsSyscallFn, readProcStatusFn, gettidFn, clearAmbientCapsFn = oldAll, oldRead, oldTid, oldAmb
	})
	var calls []allThreadsCall
	allThreadsSyscallFn = func(trap, a1, a2, _ uintptr) (uintptr, uintptr, syscall.Errno) {
		call := allThreadsCall{trap, a1, a2}
		calls = append(calls, call)
		if result != nil {
			return 0, 0, result(call)
		}
		return 0, 0, 0
	}
	gettidFn = func() int { return 4242 }
	readProcStatusFn = func(path string) ([]byte, error) {
		if s, ok := status[path]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("unexpected path " + path)
	}
	return &calls
}

func bothStatus(s string) map[string]string {
	return map[string]string{"/proc/self/status": s, "/proc/self/task/4242/status": s}
}

func TestApplyNoNewPrivsUsesAllThreads(t *testing.T) {
	calls := stubPrivilegeSyscalls(t, nil, nil)
	if err := applyNoNewPrivs(nil); err != nil {
		t.Fatalf("applyNoNewPrivs: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != (allThreadsCall{unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1}) {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestApplyNoNewPrivsFailsClosed(t *testing.T) {
	stubPrivilegeSyscalls(t, nil, func(allThreadsCall) syscall.Errno { return syscall.ENOTSUP })
	if err := applyNoNewPrivs(nil); err == nil {
		t.Fatal("applyNoNewPrivs should fail when AllThreadsSyscall is unavailable (cgo build)")
	}
}

func TestClearCapabilitiesSuccess(t *testing.T) {
	calls := stubPrivilegeSyscalls(t, bothStatus(zeroCapStatus), nil)
	if err := clearCapabilities(); err != nil {
		t.Fatalf("clearCapabilities: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %+v, want capset + ambient clear", *calls)
	}
	if (*calls)[0].trap != unix.SYS_CAPSET {
		t.Fatalf("first call trap = %d, want SYS_CAPSET", (*calls)[0].trap)
	}
	if capsetHeader.Version != unix.LINUX_CAPABILITY_VERSION_3 {
		t.Fatalf("capset header version = %#x", capsetHeader.Version)
	}
	for i, d := range capsetData {
		if d != (unix.CapUserData{}) {
			t.Fatalf("capset data[%d] = %+v, want all zero", i, d)
		}
	}
	if (*calls)[1] != (allThreadsCall{unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL}) {
		t.Fatalf("ambient call = %+v", (*calls)[1])
	}
}

func TestClearCapabilitiesFailsClosed(t *testing.T) {
	leftover := strings.Replace(zeroCapStatus, "CapEff:\t0000000000000000", "CapEff:\t00000000000000c0", 1)
	tests := []struct {
		name   string
		status map[string]string
		result func(allThreadsCall) syscall.Errno
	}{
		{
			name:   "capset error",
			status: bothStatus(zeroCapStatus),
			result: func(c allThreadsCall) syscall.Errno {
				if c.trap == unix.SYS_CAPSET {
					return syscall.EPERM
				}
				return 0
			},
		},
		{
			name:   "ambient clear error other than EINVAL",
			status: bothStatus(zeroCapStatus),
			result: func(c allThreadsCall) syscall.Errno {
				if c.trap == unix.SYS_PRCTL {
					return syscall.EPERM
				}
				return 0
			},
		},
		{name: "main thread keeps CapEff", status: map[string]string{"/proc/self/status": leftover, "/proc/self/task/4242/status": zeroCapStatus}},
		{name: "current thread keeps CapEff", status: map[string]string{"/proc/self/status": zeroCapStatus, "/proc/self/task/4242/status": leftover}},
		{name: "status unreadable", status: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubPrivilegeSyscalls(t, tt.status, tt.result)
			if err := clearCapabilities(); err == nil {
				t.Fatal("clearCapabilities should fail")
			}
		})
	}
}

func TestClearCapabilitiesToleratesKernelWithoutAmbient(t *testing.T) {
	stubPrivilegeSyscalls(t, bothStatus(zeroCapStatus), func(c allThreadsCall) syscall.Errno {
		if c.trap == unix.SYS_PRCTL {
			return syscall.EINVAL
		}
		return 0
	})
	if err := clearCapabilities(); err != nil {
		t.Fatalf("EINVAL from PR_CAP_AMBIENT must be tolerated: %v", err)
	}
}

func TestCheckCapabilityFieldsZero(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "all zero", status: zeroCapStatus},
		{name: "kernel without CapAmb", status: strings.Replace(zeroCapStatus, "CapAmb:\t0000000000000000\n", "", 1)},
		{name: "CapPrm set", status: strings.Replace(zeroCapStatus, "CapPrm:\t0000000000000000", "CapPrm:\t0000000000000080", 1), wantErr: true},
		{name: "CapInh set", status: strings.Replace(zeroCapStatus, "CapInh:\t0000000000000000", "CapInh:\t0000000000000040", 1), wantErr: true},
		{name: "CapAmb set", status: strings.Replace(zeroCapStatus, "CapAmb:\t0000000000000000", "CapAmb:\t0000000000000040", 1), wantErr: true},
		{name: "CapEff missing", status: strings.Replace(zeroCapStatus, "CapEff:\t0000000000000000\n", "", 1), wantErr: true},
		{name: "garbage value", status: strings.Replace(zeroCapStatus, "CapEff:\t0000000000000000", "CapEff:\tzz", 1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCapabilityFieldsZero(tt.status)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
