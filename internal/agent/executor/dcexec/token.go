//go:build !windows

package dcexec

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"design-cloud-engine/internal/platform/crypto"
	"design-cloud-engine/internal/platform/fs"
)

func InitToken(logger *slog.Logger) error {
	tokenFile := fs.DcExecTokenPath()

	token := crypto.GenSecret(24)

	tokenDir := filepath.Dir(tokenFile)

	// Create directory if not exists and ensure strict permissions
	if err := fs.MkdirPrivate(tokenDir); err != nil {
		return err
	}

	// Write token file atomically with restrictive permissions
	if err := fs.WritePrivateFile(tokenFile, []byte(token)); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}
	logger.Debug("PrivilegedExec token initialized", "token_file", tokenFile)
	return nil
}
