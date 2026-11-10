//go:build !dev

package logger

import (
	"io"
	"log/slog"
)

func createLogHandler(logWriter io.Writer, level slog.Leveler, nodeID string) *slog.Logger {
	handler := slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: MaskAttr,
	})
	return WithGlobalAttrs(slog.New(handler), nodeID)
}
