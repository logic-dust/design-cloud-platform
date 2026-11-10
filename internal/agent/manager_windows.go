package agent

import (
	"context"
	"log/slog"
	"time"

	"design-cloud-engine/internal/agent/executor"
	"design-cloud-engine/internal/execution"
)

func initExecBin(_ *slog.Logger) error {
	// Windows does not use PrivilegedExec.
	return nil
}

func createExecutorService(
	_ *SettingsManager,
	directExec execution.Executor,
	agentLog *slog.Logger,
	_ func(ctx context.Context, actionName string, result string, targetUser string, traceID string, level slog.Level, details map[string]any, duration time.Duration),
) *executor.Service {
	// On Windows, executor.NewService takes (directExec, logger) only.
	return executor.NewService(directExec, agentLog)
}
