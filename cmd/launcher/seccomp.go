//go:build linux && amd64

// seccomp_linux.go applies a seccomp-BPF system-call filter to the current thread
// after privilege escalation/dropping and before the action handler runs.
// Only compiled on Linux (the only supported deployment platform).
//
// This implements L4 (System Call Isolation) of the 4-layer defense-in-depth security model:
//   - L1: Runtime environment verification (file permissions, caller identity)
//   - L2: Token verification (dynamic token matching)
//   - L3: Action allowlist (operation authorization, parameter validation)
//   - L4: System call isolation (seccomp-BPF allowlist) ← THIS FILE
//
// L4 provides kernel-level defense that L1-L3 cannot provide:
//   - Prevents exploitation of code injection vulnerabilities
//   - Prevents exploitation of dependency library vulnerabilities
//   - Prevents exploitation of handler logic vulnerabilities
//   - Prevents privilege abuse (ptrace, mount, reboot, etc.)
//
// Design goals:
//   - PrivilegeTargetUser  (FILE_*, JOB_*): allow file I/O + process creation (exec)
//   - PrivilegeRoot        (USER_PASSWORD_*): same as User plus shadow read
//   - A failed seccomp setup is logged to stderr but does NOT abort execution,
//     preserving the existing L1-L3 security layers.

package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"

	"design-cloud-engine/internal/execution"
)

// applySeccompFilter installs a BPF seccomp allowlist restricted to the syscalls
// needed by the given LauncherSpec. Must be called after setuid/setgid so that the
// privilege change has already taken effect on the current OS thread.
//
// For PrivilegeTargetUser the process has already been dropped to the target user's UID;
// PR_SET_NO_NEW_PRIVS is required before loading the filter when not running as root.
//
// For PrivilegeRoot the process is root (CAP_SYS_ADMIN), so no PR_SET_NO_NEW_PRIVS
// is needed, but we apply the same set of allowed syscalls to prevent misuse.
func applySeccompFilter(spec *execution.LauncherSpec) {
	// Lock the goroutine to the current OS thread so that the seccomp filter
	// installed by prctl(2) stays active for the entire handler execution.
	runtime.LockOSThread()

	if !applyNoNewPrivs(spec) {
		return
	}

	filter := buildFilter(spec)
	if err := loadFilter(filter); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "seccomp: failed to load filter: %v\n", err)
		// Non-fatal: other security layers (L1-L3, setuid) remain active.
	}
}

func applyNoNewPrivs(spec *execution.LauncherSpec) bool {
	if spec == nil || spec.Privilege == execution.PrivilegeRoot {
		return true
	}
	// Non-root: PR_SET_NO_NEW_PRIVS must be set before applying a seccomp filter
	// (kernel requirement for unprivileged processes, see seccomp(2)). Keep this
	// protection even for actions that opt out of the BPF allowlist.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "seccomp: failed to set no-new-privs: %v\n", err)
		return false
	}
	return true
}

// loadFilter installs a classic BPF seccomp filter on the current thread.
func loadFilter(filter []unix.SockFilter) error {
	prog := unix.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}
	return unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER,
		uintptr(unsafe.Pointer(&prog)), 0, 0)
}

// buildFilter constructs a BPF allowlist for the given LauncherSpec.
// Syscalls not in the allowlist will cause the process to receive SIGSYS (killed).
func buildFilter(spec *execution.LauncherSpec) []unix.SockFilter {
	if spec == nil {
		return nil
	}

	// Common syscalls required by the Go runtime and all action handlers.
	base := []uint32{
		unix.SYS_READ, unix.SYS_WRITE, unix.SYS_PREAD64, unix.SYS_PWRITE64,
		unix.SYS_READV, unix.SYS_WRITEV,
		unix.SYS_OPEN, unix.SYS_OPENAT, unix.SYS_CLOSE,
		unix.SYS_STAT, unix.SYS_FSTAT, unix.SYS_LSTAT, unix.SYS_NEWFSTATAT,
		unix.SYS_FCNTL, unix.SYS_IOCTL,
		unix.SYS_MMAP, unix.SYS_MUNMAP, unix.SYS_MPROTECT,
		unix.SYS_BRK,
		unix.SYS_EXIT, unix.SYS_EXIT_GROUP,
		unix.SYS_RT_SIGACTION, unix.SYS_RT_SIGPROCMASK, unix.SYS_RT_SIGRETURN,
		unix.SYS_SIGALTSTACK,
		unix.SYS_FUTEX, unix.SYS_NANOSLEEP,
		unix.SYS_CLOCK_GETTIME, unix.SYS_GETTIMEOFDAY,
		unix.SYS_GETPID, unix.SYS_GETPPID, unix.SYS_GETTID,
		unix.SYS_GETUID, unix.SYS_GETGID, unix.SYS_GETEUID, unix.SYS_GETEGID,
		unix.SYS_TGKILL,
		unix.SYS_PRCTL,
		unix.SYS_ARCH_PRCTL,
		unix.SYS_GETRANDOM,
		unix.SYS_SCHED_YIELD,
		unix.SYS_PIPE, unix.SYS_PIPE2,
		unix.SYS_SELECT, unix.SYS_PSELECT6,
		unix.SYS_EPOLL_CREATE1, unix.SYS_EPOLL_CTL, unix.SYS_EPOLL_WAIT, unix.SYS_EPOLL_PWAIT,
		// Go runtime may need these for goroutine scheduling / GC
		unix.SYS_CLONE, unix.SYS_CLONE3,
		unix.SYS_WAIT4,
		unix.SYS_SCHED_GETAFFINITY,
	}

	// File-operation syscalls (needed for FILE_* actions).
	fileOps := []uint32{
		unix.SYS_UNLINK, unix.SYS_UNLINKAT,
		unix.SYS_RENAME, unix.SYS_RENAMEAT, unix.SYS_RENAMEAT2,
		unix.SYS_MKDIR, unix.SYS_MKDIRAT,
		unix.SYS_RMDIR,
		unix.SYS_GETDENTS64,
		unix.SYS_READLINK, unix.SYS_READLINKAT,
		unix.SYS_SYMLINK, unix.SYS_SYMLINKAT,
		unix.SYS_FADVISE64,
		unix.SYS_FALLOCATE,
		unix.SYS_TRUNCATE, unix.SYS_FTRUNCATE,
		unix.SYS_CHMOD, unix.SYS_FCHMOD, unix.SYS_FCHMODAT,
		unix.SYS_CHOWN, unix.SYS_FCHOWN, unix.SYS_LCHOWN, unix.SYS_FCHOWNAT,
		unix.SYS_ACCESS, unix.SYS_FACCESSAT, unix.SYS_FACCESSAT2,
		unix.SYS_UTIME, unix.SYS_UTIMES, unix.SYS_UTIMENSAT,
		unix.SYS_STATX,
		unix.SYS_INOTIFY_INIT1, unix.SYS_INOTIFY_ADD_WATCH, unix.SYS_INOTIFY_RM_WATCH,
		unix.SYS_SENDFILE,
		unix.SYS_COPY_FILE_RANGE,
	}

	// Process-creation syscalls (needed for JOB_* and USER_PASSWORD_* actions which exec external tools).
	execOps := []uint32{
		unix.SYS_EXECVE, unix.SYS_EXECVEAT,
		unix.SYS_FORK, unix.SYS_VFORK,
		unix.SYS_DUP, unix.SYS_DUP2, unix.SYS_DUP3,
		unix.SYS_CHDIR, unix.SYS_FCHDIR,
		unix.SYS_SETPGID, unix.SYS_GETPGID,
		unix.SYS_SETSID,
		unix.SYS_KILL,
		unix.SYS_SOCKET, unix.SYS_CONNECT, // may be needed for some scheduler tools
		unix.SYS_SENDTO, unix.SYS_RECVFROM,
		unix.SYS_GETSOCKOPT, unix.SYS_SETSOCKOPT, unix.SYS_GETSOCKNAME,
		unix.SYS_SETUID, unix.SYS_SETGID, unix.SYS_SETGROUPS, // re-allow in case of sub-exec
	}

	allowed := make([]uint32, 0, len(base)+len(fileOps)+len(execOps)+3)
	allowed = append(allowed, base...)
	allowed = append(allowed, fileOps...)
	allowed = append(allowed, execOps...)

	switch spec.Privilege {
	case execution.PrivilegeRoot:
		// Additional syscalls for PAM / shadow file manipulation
		allowed = append(allowed, unix.SYS_LSEEK, unix.SYS_FSYNC, unix.SYS_FDATASYNC)
	default:
		// TargetUser standard RPC or Streaming: base + file ops + exec
	}

	return buildAllowlistBPF(allowed)
}

// buildAllowlistBPF constructs a classic BPF (cBPF) program that:
//  1. Validates the audit architecture is x86-64 (kill otherwise).
//  2. Allows any syscall whose number appears in the allowlist.
//  3. Kills the process for all other syscalls.
//
// Each allowlist entry requires 2 BPF instructions:
//
//	JEQ #nr, jt=0, jf=1   → if nr matches: fall through to RET_ALLOW; else skip it.
//	RET ALLOW
//
// This is the standard "allow-if-match" pattern for cBPF seccomp filters.
func buildAllowlistBPF(allowed []uint32) []unix.SockFilter {
	const (
		auditarchx8664 = 0xC000003E // AUDIT_ARCH_X86_64
		// Offsets within struct seccomp_data (see linux/seccomp.h)
		offsetNr   = 0 // uint32 nr
		offsetArch = 4 // uint32 arch
	)

	bpfLD := func(offset uint32) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: offset}
	}
	bpfJEQ := func(k uint32, jt, jf uint8) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: jt, Jf: jf, K: k}
	}
	bpfRET := func(k uint32) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: k}
	}

	var filter []unix.SockFilter

	// 1. Architecture check: load arch, compare to x86-64, kill if mismatched.
	filter = append(filter,
		bpfLD(offsetArch),
		bpfJEQ(auditarchx8664, 1, 0), // if arch==x86_64: skip next (continue); else: fall through to KILL
		bpfRET(unix.SECCOMP_RET_KILL_PROCESS),
	)

	// 2. Load syscall number once; all JEQ checks below operate on it.
	filter = append(filter, bpfLD(offsetNr))

	// 3. Emit per-syscall allow pairs.
	for _, nr := range allowed {
		filter = append(filter,
			bpfJEQ(nr, 0, 1),               // if nr==syscall: fall through (→ RET ALLOW); else skip 1
			bpfRET(unix.SECCOMP_RET_ALLOW), // allow this nr
		)
	}

	// 4. Default action: kill the process on any syscall not in the allowlist.
	filter = append(filter, bpfRET(unix.SECCOMP_RET_KILL_PROCESS))

	return filter
}
