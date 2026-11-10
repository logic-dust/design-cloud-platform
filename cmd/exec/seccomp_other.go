//go:build linux && !amd64

package main

import (
	"design-cloud-engine/internal/execution"
)

// Seccomp-BPF allowlists are maintained for the production amd64 target only.
// On other Linux arches (e.g. arm64 developer hosts), L4 is a no-op; L0–L3 and
// no_new_privs still apply.

func applySeccompFilter(_ *execution.PrivilegedExecSpec) error { return nil }
