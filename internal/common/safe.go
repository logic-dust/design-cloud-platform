package common

import (
	"log/slog"
	"runtime/debug"
)

// SafeGo executes fn in a new goroutine protected by a panic recover handler.
// If fn panics, the panic value and stack trace are logged using logger.
func SafeGo(logger *slog.Logger, name string, fn func()) {
	go func() {
		defer SafeRecover(logger, name)()
		fn()
	}()
}

// SafeRecover returns a deferrable function that catches panics, logs stack traces with logger,
// and optionally invokes onPanic callbacks.
//
// Usage:
//
//	defer common.SafeRecover(logger, "worker-name")()
//	defer common.SafeRecover(logger, "task-name", func(r any) { /* cleanup */ })()
func SafeRecover(logger *slog.Logger, name string, onPanic ...func(r any)) func() {
	return func() {
		if r := recover(); r != nil {
			if logger != nil {
				logger.Error("goroutine panic recovered",
					"goroutine", name,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
			for _, cb := range onPanic {
				if cb != nil {
					cb(r)
				}
			}
		}
	}
}
