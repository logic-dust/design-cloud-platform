package controlplane

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
)

type countingConnectionReportHandler struct {
	mu    sync.Mutex
	calls []struct {
		nodeID string
		at     int64
	}
	block chan struct{}
}

func (h *countingConnectionReportHandler) SyncConnectionReport(_ context.Context, nodeID string, report contract.ConnectionReport) error {
	if h.block != nil {
		<-h.block
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, struct {
		nodeID string
		at     int64
	}{nodeID: nodeID, at: report.ReportedAt})
	return nil
}

func TestConnectionReconcileWorker_CoalescesLatestPerNode(t *testing.T) {
	t.Parallel()

	handler := &countingConnectionReportHandler{block: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := newConnectionReconcileWorker(ctx, handler, slog.Default())

	// First enqueue starts flush but Sync is blocked; further enqueues coalesce.
	w.Enqueue("n1", contract.ConnectionReport{ReportedAt: 1})
	time.Sleep(20 * time.Millisecond)
	w.Enqueue("n1", contract.ConnectionReport{ReportedAt: 2})
	w.Enqueue("n1", contract.ConnectionReport{ReportedAt: 3})
	w.Enqueue("n2", contract.ConnectionReport{ReportedAt: 10})

	close(handler.block)

	deadline := time.Now().Add(2 * time.Second)
	for {
		handler.mu.Lock()
		n := len(handler.calls)
		handler.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for reconcile; calls=%d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	byNode := map[string][]int64{}
	for _, c := range handler.calls {
		byNode[c.nodeID] = append(byNode[c.nodeID], c.at)
	}
	if len(byNode["n1"]) == 0 || byNode["n1"][len(byNode["n1"])-1] != 3 {
		t.Fatalf("n1 calls = %v, want last ReportedAt=3", byNode["n1"])
	}
	if len(byNode["n2"]) == 0 || byNode["n2"][len(byNode["n2"])-1] != 10 {
		t.Fatalf("n2 calls = %v, want last ReportedAt=10", byNode["n2"])
	}
	// Coalesce: n1 should not apply every intermediate report after the blocked first.
	if len(byNode["n1"]) > 2 {
		t.Fatalf("n1 over-applied: %v (expected ≤2 with coalesce)", byNode["n1"])
	}
}

type panicOnceHandler struct {
	mu    sync.Mutex
	calls int
}

func (h *panicOnceHandler) SyncConnectionReport(_ context.Context, _ string, _ contract.ConnectionReport) error {
	h.mu.Lock()
	h.calls++
	n := h.calls
	h.mu.Unlock()
	if n == 1 {
		panic("desktop reconcile handler panic")
	}
	return nil
}

func TestConnectionReconcileWorker_RecoversHandlerPanic(t *testing.T) {
	t.Parallel()

	handler := &panicOnceHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newConnectionReconcileWorker(ctx, handler, slog.Default())

	w.Enqueue("n1", contract.ConnectionReport{ReportedAt: 1})
	deadline := time.Now().Add(2 * time.Second)
	for {
		handler.mu.Lock()
		n := handler.calls
		handler.mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first handler call did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The worker must survive the panic and process the next report.
	w.Enqueue("n2", contract.ConnectionReport{ReportedAt: 2})
	deadline = time.Now().Add(2 * time.Second)
	for {
		handler.mu.Lock()
		n := handler.calls
		handler.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not process report after panic")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}
