//go:build dev && linux

package agent

import (
	"log/slog"
	"os"
	"path/filepath"
)

func ensureMockDcExec(logger *slog.Logger) {
	if os.Getenv("DCE_DEV_MOCK_EXEC_BIN") != "true" {
		return
	}

	execBinPath := filepath.Join(os.TempDir(), "dce-mock-dcexec")
	if _, err := os.Stat(execBinPath); err == nil {
		return // already exists
	}

	logger.Debug("Creating mock PrivilegedExec because the binary does not exist")

	content := `#!/bin/sh
echo '{"success": true, "output": "mock PrivilegedExec execution"}'
`
	if err := os.WriteFile(execBinPath, []byte(content), 0750); err != nil {
		logger.Warn("Failed to create mock dc-exec", "error", err)
	}
}
