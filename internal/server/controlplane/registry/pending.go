package registry

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type pendingRequest struct {
	traceID    string
	targetNode string
	ch         chan any
	createdAt  time.Time
	closed     bool
	mu         sync.Mutex
}

var errRegistryStopped = errors.New("agent registry stopped")

// CreatePendingRequest creates a response channel for a synchronous execution.
func (r *Registry) CreatePendingRequest(traceID, targetNode string) chan any {
	request := &pendingRequest{
		traceID:    traceID,
		targetNode: targetNode,
		ch:         make(chan any, 1),
		createdAt:  time.Now(),
	}
	r.pendingRequests.Store(traceID, request)
	return request.ch
}

// DeletePendingRequest removes a pending request without closing its channel.
func (r *Registry) DeletePendingRequest(traceID string) {
	value, ok := r.pendingRequests.Load(traceID)
	if ok {
		request := value.(*pendingRequest)
		request.mu.Lock()
		request.closed = true
		request.mu.Unlock()
	}
	r.pendingRequests.Delete(traceID)
}

func (r *Registry) DeliverResult(traceID string, result any) {
	value, ok := r.pendingRequests.Load(traceID)
	if !ok {
		r.logger.Debug("No pending request for result", "trace_id", traceID)
		return
	}

	request := value.(*pendingRequest)
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.closed {
		r.logger.Debug("Pending request already closed", "trace_id", traceID)
		return
	}

	select {
	case request.ch <- result:
	default:
		r.logger.Warn("Failed to deliver result, channel full", "trace_id", traceID)
	}
}

func (r *Registry) failPendingRequest(traceID string, err error) {
	if err == nil {
		err = fmt.Errorf("pending request failed")
	}
	value, ok := r.pendingRequests.Load(traceID)
	if !ok {
		return
	}

	request := value.(*pendingRequest)
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.closed {
		return
	}
	request.closed = true
	select {
	case request.ch <- err:
	default:
	}
	r.pendingRequests.Delete(traceID)
}

// FailAllPendingRequests 使全部在途同步请求立即以 err 失败。
// 用于注册表停止与 Leader 降级自隔离（在途 dispatch 快速失败而非挂起）。
func (r *Registry) FailAllPendingRequests(err error) {
	r.pendingRequests.Range(func(key, _ any) bool {
		r.failPendingRequest(key.(string), err)
		return true
	})
}

func (r *Registry) failPendingRequestsForNodeLocked(nodeID string, err error) {
	var traceIDs []string
	r.pendingRequests.Range(func(key, value any) bool {
		if value.(*pendingRequest).targetNode == nodeID {
			traceIDs = append(traceIDs, key.(string))
		}
		return true
	})
	for _, traceID := range traceIDs {
		r.failPendingRequest(traceID, err)
	}
}
