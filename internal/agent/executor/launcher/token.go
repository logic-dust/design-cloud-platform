//go:build !windows

package launcher

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/common/fs"
)

func InitToken(logger *slog.Logger) error {
	tokenFile := fs.LauncherTokenPath()

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
	logger.Debug("Launcher token initialized", "token_file", tokenFile)
	return nil
}
