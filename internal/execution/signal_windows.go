package execution

import "syscall"

// LauncherKilledBySeccomp always returns false on Windows: dc-launcher is Linux-only, and
// Windows has no POSIX signal termination semantics.
func LauncherKilledBySeccomp(_ error) bool {
	return false
}

// ExitErrorSignaled always returns false on Windows.
// Windows processes do not have POSIX signal termination semantics.
func ExitErrorSignaled(_ error, _ syscall.Signal) bool {
	return false
}
