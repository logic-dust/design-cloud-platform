package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/agent/executor"
	"design-cloud-engine/internal/agent/executor/dcexec"
	"design-cloud-engine/internal/execution"
)

func initExecBin(logger *slog.Logger) error {
	ensureMockDcExec(logger)
	if err := dcexec.InitToken(logger); err != nil {
		return fmt.Errorf("failed to initialize dc-exec token: %w", err)
	}
	return nil
}

func createExecutorService(
	settingsMgr *SettingsManager,
	directExec execution.Executor,
	agentLog *slog.Logger,
	onAudit func(ctx context.Context, actionName string, result string, targetUser string, traceID string, level slog.Level, details map[string]any, duration time.Duration),
) *executor.Service {
	privilegedExec := dcexec.NewExecutor(dcexec.Config{
		GetExecBinPath: func() string { return settingsMgr.Get().ExecBinPath },
	}, agentLog, onAudit)

	return executor.NewService(privilegedExec, directExec, agentLog)
}
