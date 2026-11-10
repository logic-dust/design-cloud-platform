package config

import (
	"fmt"
	"os"

	fsutil "design-cloud-engine/internal/platform/fs"
)

func checkSecretFilePermissions(label, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s not accessible: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", label)
	}
	if err := fsutil.VerifyPathACL(path); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// validateConfigFilePermissions enforces the Windows equivalent of the Linux 0600 rule:
// a config file whose DACL grants write access to anyone beyond SYSTEM / Administrators is
// hardened in place (the analogue of the Linux chmod repair), and reported if that fails.
func validateConfigFilePermissions(path string) error {
	if err := fsutil.VerifyPathACL(path); err == nil {
		return nil
	}

	fmt.Printf("⚠️  Config file '%s' has a permissive ACL; hardening to SYSTEM/Administrators only...\n", path)
	if err := fsutil.HardenPath(path, false); err != nil {
		return fmt.Errorf("failed to harden ACL for '%s': %w. Run 'icacls \"%s\" /inheritance:r /grant:r \"SYSTEM:(F)\" \"Administrators:(F)\"' manually", path, err, path)
	}
	return nil
}
