//go:build !windows

package verifier

import (
	"fmt"
	"strconv"
	"strings"
)

// Linux capability bits (linux/capability.h). PrivilegedExec only ever needs these two:
// it runs as the service account and uses them to switch to the target user's identity.
const (
	capSetGID = 6
	capSetUID = 7
)

// capabilityStatusPath is a package variable so tests can substitute a synthetic
// /proc/self/status. Reading /proc avoids both cgo and an extra syscall dependency.
var capabilityStatusPath = "/proc/self/status"

// Capabilities performs the L0 startup verification: PrivilegedExec must actually hold
// CAP_SETUID and CAP_SETGID in its effective set.
//
// Why this exists: capabilities are stored in the `security.capability` extended
// attribute, and setcap writes nothing else. If the binary is installed on a filesystem
// that cannot carry that xattr (NFS being the common case), setcap either fails outright
// or the attribute is silently dropped — the file still looks perfectly normal
// (mode 0700, correct owner), every other L1 check passes, and the failure only surfaces
// later as an opaque permission error when the PrivilegedExec tries to switch identity.
// Checking up front turns that late, confusing failure into an immediate, actionable one.
//
// Note this cannot be inferred from file mode: PrivilegedExec deliberately does NOT use
// setuid (which would grant full root), only these two narrow capabilities.
func Capabilities() error {
	data, err := readFile(capabilityStatusPath)
	if err != nil {
		return fmt.Errorf("failed to read process capabilities from %s: %w", capabilityStatusPath, err)
	}

	effective, err := parseEffectiveCapabilities(string(data))
	if err != nil {
		return err
	}

	var missing []string
	if effective&(1<<capSetUID) == 0 {
		missing = append(missing, "CAP_SETUID")
	}
	if effective&(1<<capSetGID) == 0 {
		missing = append(missing, "CAP_SETGID")
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"missing required capability(ies) %s (CapEff=%#016x); PrivilegedExec must be installed on a "+
				"local filesystem and granted capabilities with "+
				"`setcap cap_setuid,cap_setgid+ep <binary>` — capabilities live in the "+
				"security.capability xattr, which NFS cannot carry",
			strings.Join(missing, ", "), effective)
	}

	requiredMask := uint64((1 << capSetGID) | (1 << capSetUID))
	if extra := effective & ^requiredMask; extra != 0 {
		return fmt.Errorf(
			"unexpected effective capability(ies) in CapEff=%#016x; PrivilegedExec must hold only "+
				"CAP_SETUID and CAP_SETGID (install with `setcap cap_setuid,cap_setgid+ep` only)",
			effective)
	}

	return nil
}

// parseEffectiveCapabilities extracts the CapEff mask from /proc/self/status content.
func parseEffectiveCapabilities(status string) (uint64, error) {
	for line := range strings.SplitSeq(status, "\n") {
		value, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse CapEff value %q: %w", strings.TrimSpace(value), err)
		}
		return mask, nil
	}
	return 0, fmt.Errorf("CapEff not found in %s (unsupported kernel or unexpected /proc layout)", capabilityStatusPath)
}
