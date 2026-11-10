//go:build !windows

package command

import (
	"fmt"
	"os"
	"slices"
	"syscall"
)

// ensureSetPasswordPrivilege restricts offline app-user password updates to root
// or the owner of the config file / engine executable.
func ensureSetPasswordPrivilege(configPath string) error {
	euid := os.Geteuid()
	if euid == 0 {
		return nil
	}

	var ownerUIDs []uint32
	for _, p := range []string{configPath, executablePath()} {
		if uid, ok := fileOwnerUID(p); ok {
			ownerUIDs = append(ownerUIDs, uid)
		}
	}
	if callerMatchesOwner(euid, ownerUIDs) {
		return nil
	}
	return fmt.Errorf("permission denied: only root or the program/config owner may set application user passwords")
}

func callerMatchesOwner(euid int, ownerUIDs []uint32) bool {
	if euid == 0 {
		return true
	}
	uid := uint32(euid)
	return slices.Contains(ownerUIDs, uid)
}

func executablePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

func fileOwnerUID(path string) (uint32, bool) {
	if path == "" {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return st.Uid, true
}
