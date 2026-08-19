package execution

import (
	"errors"
	"os/exec"
	"syscall"
)

// LauncherKilledBySeccomp reports whether err is a dc-launcher process that the kernel
// killed for a blocked system call (seccomp), which Linux surfaces as SIGSYS.
func LauncherKilledBySeccomp(err error) bool {
	return ExitErrorSignaled(err, syscall.SIGSYS)
}

// ExitErrorSignaled reports whether err is an exec.ExitError for a process
// terminated by sig.
func ExitErrorSignaled(err error, sig syscall.Signal) bool {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ProcessState != nil {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.Signaled() && status.Signal() == sig
		}
	}
	return false
}
