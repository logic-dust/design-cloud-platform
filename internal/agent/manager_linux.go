package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"design-cloud-engine/internal/agent/executor"
	"design-cloud-engine/internal/agent/executor/launcher"
	"design-cloud-engine/internal/execution"
)

func initLauncher(logger *slog.Logger) error {
	ensureMockLauncher(logger)
	if err := launcher.InitToken(logger); err != nil {
		return fmt.Errorf("failed to initialize launcher token: %w", err)
	}
	return nil
}

func createExecutorService(
	settingsMgr *SettingsManager,
	directExec execution.Executor,
	agentLog *slog.Logger,
	onAudit func(ctx context.Context, actionName string, result string, targetUser string, traceID string, level slog.Level, details map[string]any, duration time.Duration),
) *executor.Service {
	privilegedExec := launcher.NewExecutor(launcher.Config{
		GetLauncherPath: func() string { return settingsMgr.Get().LauncherPath },
	}, agentLog, onAudit)

	return executor.NewService(privilegedExec, directExec, agentLog)
}

func ensureMockLauncher(logger *slog.Logger) {
	if os.Getenv("DCE_DEV_MOCK_LAUNCHER") != "true" {
		return
	}

	launcherPath := filepath.Join(os.TempDir(), "dce-mock-launcher")
	if _, err := os.Stat(launcherPath); err == nil {
		return // already exists
	}

	logger.Debug("Creating mock launcher because the binary does not exist")

	content := `#!/bin/sh
echo '{"success": true, "output": "mock launcher execution"}'
`
	if err := os.WriteFile(launcherPath, []byte(content), 0750); err != nil {
		logger.Warn("Failed to create mock launcher", "error", err)
	}
}
