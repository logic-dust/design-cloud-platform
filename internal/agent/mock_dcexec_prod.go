//go:build !dev || !linux

package agent

import "log/slog"

func ensureMockDcExec(_ *slog.Logger) {}
