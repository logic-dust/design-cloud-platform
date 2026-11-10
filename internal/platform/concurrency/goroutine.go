// Package concurrency provides goroutine helpers that make panic isolation and
// bounded fan-out the default rather than an afterthought.
//
// It is a leaf package: no business, cluster, or transport imports.
package concurrency

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

// BoundedGo acquires a slot from sem before starting fn, then releases it when
// fn returns. Callers block while all slots are busy, bounding concurrent work.
//
// A nil sem degrades to SafeGo (unbounded), which keeps call sites simple when
// the limit is configured away.
func BoundedGo(sem chan struct{}, logger *slog.Logger, name string, fn func()) {
	if sem == nil {
		SafeGo(logger, name, fn)
		return
	}
	sem <- struct{}{}
	go func() {
		defer func() { <-sem }()
		defer SafeRecover(logger, name)()
		fn()
	}()
}

// TryBoundedGo starts fn in a goroutine if a slot is immediately available.
// Unlike BoundedGo, it never blocks the caller; it returns false when the
// configured concurrency limit is saturated. This is useful for best-effort
// work such as notifications, where backpressure must not delay the request
// that produced the event.
func TryBoundedGo(sem chan struct{}, logger *slog.Logger, name string, fn func()) bool {
	if sem == nil {
		SafeGo(logger, name, fn)
		return true
	}
	select {
	case sem <- struct{}{}:
		go func() {
			defer func() { <-sem }()
			defer SafeRecover(logger, name)()
			fn()
		}()
		return true
	default:
		return false
	}
}

// SafeRecover returns a deferrable function that catches panics, logs stack traces with logger,
// and optionally invokes onPanic callbacks.
//
// Usage:
//
//	defer concurrency.SafeRecover(logger, "worker-name")()
//	defer concurrency.SafeRecover(logger, "task-name", func(r any) { /* cleanup */ })()
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
