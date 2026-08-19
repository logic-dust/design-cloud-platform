package config

import (
	"fmt"
	"os"
)

func checkSecretFilePermissions(label, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s not accessible: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", label)
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return fmt.Errorf("%s must not grant any permissions to group or others", label)
	}
	if mode&0o400 == 0 {
		return fmt.Errorf("%s must be owner-readable", label)
	}
	return nil
}

// validateConfigFilePermissions ensures the config file has safe permissions on Linux.
func validateConfigFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		fmt.Printf("⚠️  Config file '%s' has permissive permissions (%o); fixing to 0600...\n", path, mode)
		if err := os.Chmod(path, 0600); err != nil {
			return fmt.Errorf("failed to fix permissions for '%s': %w. Run 'chmod 600 %s' manually", path, err, path)
		}
	}
	return nil
}
