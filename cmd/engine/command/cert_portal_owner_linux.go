//go:build linux

package command

import (
	"os"
	"os/user"
	"strconv"

	"design-cloud-engine/cmd/engine/console"
)

// adjustCertOwnershipIfRoot checks if running as root on Linux, and if so,
// attempts to chown the output directory and generated files to the target service user (design-cloud:design-cloud).
func adjustCertOwnershipIfRoot(res *PortalCertResult, outDir string) {
	if os.Geteuid() != 0 && os.Getuid() != 0 {
		return
	}

	targetUser := "design-cloud"
	u, err := user.Lookup(targetUser)
	if err != nil {
		console.PrintStderr("Note: Target service user %q not found; certificate files remain owned by root (ensure the service runner has read permission).\n", targetUser)
		return
	}

	uid, errUid := strconv.Atoi(u.Uid)
	gid, errGid := strconv.Atoi(u.Gid)
	if errUid != nil || errGid != nil {
		return
	}

	targets := []string{
		outDir,
		res.CACertPath,
		res.CAKeyPath,
		res.ServerCertPath,
		res.ServerKeyPath,
		res.FullchainPath,
	}

	for _, path := range targets {
		if path == "" {
			continue
		}
		if err := os.Chown(path, uid, gid); err != nil {
			console.PrintStderr("Warning: failed to chown %s to %s:%s: %v\n", path, targetUser, targetUser, err)
		}
	}

	console.PrintStdout("Notice: Ownership of generated certificate files automatically adjusted to %s:%s.\n\n", targetUser, targetUser)
}
