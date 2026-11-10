package controlplane

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/platform/concurrency"
)

const (
	connectionReconcileShutdownTimeout = 5 * time.Second
	connectionReconcileQueueWarnDepth  = 32
)

// connectionReconcileWorker coalesces agent ConnectionReport messages per node and
// applies them off the Sync recv path. Latest report for a node wins.
type connectionReconcileWorker struct {
	handler ConnectionRuntimeHandler
	logger  *slog.Logger

	mu      sync.Mutex
	pending map[string]contract.ConnectionReport
	wake    chan struct{}
	ctx     context.Context
	done    chan struct{}
}

func newConnectionReconcileWorker(ctx context.Context, handler ConnectionRuntimeHandler, logger *slog.Logger) *connectionReconcileWorker {
	w := &connectionReconcileWorker{
		handler: handler,
		logger:  logger,
		pending: make(map[string]contract.ConnectionReport),
		wake:    make(chan struct{}, 1),
		ctx:     ctx,
		done:    make(chan struct{}),
	}
	go w.loop()
	return w
}

func (w *connectionReconcileWorker) Enqueue(nodeID string, report contract.ConnectionReport) {
	if w == nil || nodeID == "" {
		return
	}
	w.mu.Lock()
	w.pending[nodeID] = report
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *connectionReconcileWorker) loop() {
	defer close(w.done)
	for {
		select {
		case <-w.ctx.Done():
			ctx, cancel := context.WithTimeout(context.Background(), connectionReconcileShutdownTimeout)
			w.flush(ctx)
			cancel()
			return
		case <-w.wake:
			w.flush(w.ctx)
		}
	}
}

func (w *connectionReconcileWorker) flush(ctx context.Context) {
	for {
		batch := w.takeAll()
		if len(batch) == 0 {
			return
		}
		if len(batch) >= connectionReconcileQueueWarnDepth && w.logger != nil {
			w.logger.Warn("Connection reconcile queue depth high",
				"nodes", len(batch),
				"hint", "reconcile worker is lagging behind ConnectionReport rate")
		}
		for nodeID, report := range batch {
			w.apply(ctx, nodeID, report)
		}
	}
}

func (w *connectionReconcileWorker) apply(ctx context.Context, nodeID string, report contract.ConnectionReport) {
	defer concurrency.SafeRecover(w.logger, "connection-reconcile-worker")()
	if err := w.handler.SyncConnectionReport(ctx, nodeID, report); err != nil && w.logger != nil {
		w.logger.Error("Failed to sync connection runtime report",
			"nodeID", nodeID,
			"reported_at", report.ReportedAt,
			"vnc_seats", len(report.VNCSeats),
			"xrdp_seats", len(report.XRDPSeats),
			"error", err)
	}
}

func (w *connectionReconcileWorker) takeAll() map[string]contract.ConnectionReport {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	batch := w.pending
	w.pending = make(map[string]contract.ConnectionReport)
	return batch
}
