package execution

import "syscall"

// DcExecKilledBySeccomp always returns false on Windows: PrivilegedExec is Linux-only, and
// Windows has no POSIX signal termination semantics.
func DcExecKilledBySeccomp(_ error) bool {
	return false
}

// ExitErrorSignaled always returns false on Windows.
// Windows processes do not have POSIX signal termination semantics.
func ExitErrorSignaled(_ error, _ syscall.Signal) bool {
	return false
}
