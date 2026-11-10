package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NewLogWriter returns the writer for a log stream.
// Empty path writes to stdout (journald / container runtime).
// Non-empty path appends to a plain file (used by audit/security so Portal
// can tail/stream); rotation is left to the OS (journald / logrotate).
func NewLogWriter(logFilePath string) (io.Writer, error) {
	if logFilePath == "" {
		return os.Stdout, nil
	}

	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640) // #nosec G304 — path from config
	if err != nil {
		return nil, err
	}
	return f, nil
}

// MaskAttr includes common log transformations:
// 1. Masking sensitive keys (password, token, etc.)
// 2. Converting errors to strings
var maskKeywords = []string{"password", "token", "secret", "otp"}

func MaskAttr(_ []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)
	if slices.ContainsFunc(maskKeywords, func(k string) bool { return strings.Contains(key, k) }) {
		return slog.String(a.Key, "***")
	}

	if err, ok := a.Value.Any().(error); ok {
		return slog.String(a.Key, err.Error())
	}

	return a
}

// WithGlobalAttrs adds node ID to the logger
func WithGlobalAttrs(l *slog.Logger, hostName string) *slog.Logger {
	if l == nil {
		return nil
	}
	return l.With(
		slog.String("host", hostName),
	)
}

// LogConfig holds all parameters needed to construct a structured JSON logger.
// It is intentionally decoupled from the config package to keep logger a leaf package.
type LogConfig struct {
	// Path is the log file path. Empty string writes to stdout.
	Path string
	// Host is the unique identifier of the running instance (e.g. hostname).
	// Attached to every log entry as the "host" attribute.
	Host string
	// Level controls the minimum log level.
	Level slog.Level
}

// NewLogger creates a structured JSON logger from LogConfig.
// Returns the logger, an io.Closer (non-nil only when writing to a file), and any error.
// Callers writing to a file should defer closer.Close() to flush and release resources.
func NewLogger(cfg LogConfig) (*slog.Logger, io.Closer, error) {
	w, err := NewLogWriter(cfg.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("create log writer: %w", err)
	}

	var closer io.Closer
	if c, ok := w.(io.Closer); ok {
		closer = c
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(cfg.Level)

	return createLogHandler(w, levelVar, cfg.Host), closer, nil
}
