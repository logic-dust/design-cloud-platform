package agent

import (
	"log/slog"

	"design-cloud-engine/internal/agent/executor"
	"design-cloud-engine/internal/execution"
)

func newTestExecutorService(mock execution.Executor) *executor.Service {
	return executor.NewService(mock, mock, slog.Default())
}
