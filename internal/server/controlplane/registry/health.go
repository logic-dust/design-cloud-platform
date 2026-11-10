package registry

import (
	"fmt"
	"runtime/debug"
	"time"
)

// Stop signals every managed worker and waits for it to finish.
func (r *Registry) Stop() {
	r.FailAllPendingRequests(errRegistryStopped)
	close(r.stopCh)
	r.wg.Wait()
}

// StartWorker runs a control-owned worker under the registry lifecycle.
func (r *Registry) StartWorker(worker func(done <-chan struct{})) {
	r.wg.Go(func() {
		worker(r.stopCh)
	})
}

func (r *Registry) StartPendingRequestCleanup(interval, ttl time.Duration) {
	r.StartWorker(func(done <-chan struct{}) {
		r.pendingRequestCleanup(done, interval, ttl)
	})
}

func (r *Registry) pendingRequestCleanup(done <-chan struct{}, interval, ttl time.Duration) {
	r.logger.Info("Starting pending request cleanup", "interval", interval, "ttl", ttl)
	defer r.recoverWorkerPanic("StartPendingRequestCleanup")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.cleanupStalePendingRequests(ttl)
		case <-done:
			return
		}
	}
}

func (r *Registry) cleanupStalePendingRequests(ttl time.Duration) {
	defer r.recoverWorkerPanic("pending request cleanup tick")

	now := time.Now()
	var stale []string
	r.pendingRequests.Range(func(key, value any) bool {
		request := value.(*pendingRequest)
		if now.Sub(request.createdAt) > ttl {
			stale = append(stale, key.(string))
		}
		return true
	})
	for _, traceID := range stale {
		r.failPendingRequest(traceID, fmt.Errorf("pending request expired after %s", ttl))
	}
	if len(stale) > 0 {
		r.logger.Debug("Cleaned up stale pending requests", "count", len(stale))
	}
}

func (r *Registry) StartHealthChecker(checkInterval, pingInterval, staleTimeout time.Duration) {
	r.StartWorker(func(done <-chan struct{}) {
		r.healthCheckLoop(done, checkInterval, pingInterval, staleTimeout)
	})
}

func (r *Registry) healthCheckLoop(done <-chan struct{}, checkInterval, pingInterval, staleTimeout time.Duration) {
	r.logger.Info("Starting connection health checker",
		"check_interval", checkInterval,
		"ping_interval", pingInterval,
		"stale_timeout", staleTimeout)
	defer r.recoverWorkerPanic("StartHealthChecker")

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			func() {
				defer r.recoverWorkerPanic("health check tick")
				r.checkHealth(pingInterval, staleTimeout)
			}()
		case <-done:
			return
		}
	}
}

func (r *Registry) checkHealth(pingInterval, staleTimeout time.Duration) {
	r.mu.RLock()
	var staleConnections []*Connection
	var idleConnections []*Connection
	now := time.Now()
	for _, conn := range r.connections {
		sinceLastSeen := now.Sub(conn.LastSeen())
		if sinceLastSeen > staleTimeout {
			staleConnections = append(staleConnections, conn)
		} else if sinceLastSeen > pingInterval {
			idleConnections = append(idleConnections, conn)
		}
	}
	r.mu.RUnlock()

	if r.afterHealthScan != nil {
		r.afterHealthScan()
	}

	// Unregister the exact connection judged stale: if the Agent re-registered after the scan,
	// the new connection is a different object and stays registered.
	for _, conn := range staleConnections {
		r.logger.Warn("Agent connection is stale, removing", "nodeID", conn.NodeID)
		r.Unregister(conn.NodeID, conn)
	}
	for _, conn := range idleConnections {
		r.logger.Debug("Agent connection idle, relying on gRPC keepalive", "nodeID", conn.NodeID)
	}
}

func (r *Registry) recoverWorkerPanic(name string) {
	if recovered := recover(); recovered != nil {
		r.logger.Error("panic recovered in registry worker",
			"worker", name,
			"panic", recovered,
			"stack", string(debug.Stack()))
	}
}
