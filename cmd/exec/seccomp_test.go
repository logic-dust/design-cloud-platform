//go:build linux && amd64

package main

import (
	"testing"

	"golang.org/x/sys/unix"

	"design-cloud-engine/internal/execution"
)

// allowedSyscalls extracts the syscall numbers from the allow pairs emitted by buildAllowlistBPF.
func allowedSyscalls(filter []unix.SockFilter) map[uint32]bool {
	allowed := make(map[uint32]bool)
	for i := 0; i+1 < len(filter); i++ {
		jeq := filter[i]
		ret := filter[i+1]
		if jeq.Code == unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K && jeq.Jt == 0 && jeq.Jf == 1 &&
			ret.Code == unix.BPF_RET|unix.BPF_K && ret.K == unix.SECCOMP_RET_ALLOW {
			allowed[jeq.K] = true
		}
	}
	return allowed
}

var identitySyscalls = map[string]uint32{
	"setuid":    unix.SYS_SETUID,
	"setgid":    unix.SYS_SETGID,
	"setgroups": unix.SYS_SETGROUPS,
	"setreuid":  unix.SYS_SETREUID,
	"setregid":  unix.SYS_SETREGID,
	"setresuid": unix.SYS_SETRESUID,
	"setresgid": unix.SYS_SETRESGID,
	"setfsuid":  unix.SYS_SETFSUID,
	"setfsgid":  unix.SYS_SETFSGID,
}

func TestBuildFilterTargetUserDeniesIdentityChanges(t *testing.T) {
	allowed := allowedSyscalls(buildFilter(&execution.PrivilegedExecSpec{Privilege: execution.PrivilegeTargetUser}))
	if !allowed[unix.SYS_EXECVE] || !allowed[unix.SYS_OPENAT] {
		t.Fatal("target-user filter must keep exec and file syscalls")
	}
	for name, nr := range identitySyscalls {
		if allowed[nr] {
			t.Errorf("target-user filter allows %s", name)
		}
	}
}

func TestBuildFilterRootKeepsIdentityChanges(t *testing.T) {
	allowed := allowedSyscalls(buildFilter(&execution.PrivilegedExecSpec{Privilege: execution.PrivilegeRoot}))
	for _, nr := range []uint32{unix.SYS_SETUID, unix.SYS_SETGID, unix.SYS_SETGROUPS, unix.SYS_LSEEK, unix.SYS_FSYNC} {
		if !allowed[nr] {
			t.Errorf("root filter must allow syscall %d", nr)
		}
	}
}

func TestBuildFilterEndsWithKill(t *testing.T) {
	filter := buildFilter(&execution.PrivilegedExecSpec{Privilege: execution.PrivilegeTargetUser})
	last := filter[len(filter)-1]
	if last.Code != unix.BPF_RET|unix.BPF_K || last.K != unix.SECCOMP_RET_KILL_PROCESS {
		t.Fatalf("last instruction = %+v, want RET KILL_PROCESS", last)
	}
}
