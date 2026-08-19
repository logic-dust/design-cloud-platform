package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/protobuf/proto"
)

// AgentConnection represents a connected agent
type AgentConnection struct {
	NodeID         string
	Stream         pb.ControlService_SyncServer
	PeerAddr       string
	LastSeen       time.Time
	SystemInfo     *pb.SystemInfo
	ResourceReport *pb.ResourceReport
	RDPStatus      *pb.RDPStatus
	mu             sync.Mutex
	replaced       atomic.Bool // set to true when this connection is superseded by a new one
}

func (c *AgentConnection) GetRDPStatus() *pb.RDPStatus {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.RDPStatus
}

func (c *AgentConnection) SetRDPStatus(status *pb.RDPStatus) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.RDPStatus = status
}

func (c *AgentConnection) Send(cmd *pb.ServerCommand) error {
	if c == nil {
		return fmt.Errorf("agent connection is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Stream.Send(cmd)
}

func (c *AgentConnection) StreamContext() context.Context {
	if c == nil || c.Stream == nil {
		return context.Background()
	}
	return c.Stream.Context()
}

// AgentRegistry manages all connected agents and pending requests
type AgentRegistry struct {
	connections     map[string]*AgentConnection
	mu              sync.RWMutex
	pendingRequests sync.Map // map[traceID]*pendingRequest
	logger          *slog.Logger
	stopCh          chan struct{}
	wg              sync.WaitGroup // tracks background goroutines
	onDisconnect    func(nodeID string)
}

// AgentSnapshot is a read-only copy of a connected agent's runtime state.
type AgentSnapshot struct {
	NodeID         string
	PeerAddr       string
	LastSeen       time.Time
	SystemInfo     *pb.SystemInfo
	ResourceReport *pb.ResourceReport
}

// pendingRequest holds a response channel with creation timestamp for TTL cleanup.
// The TraceID field is the lookup key for synchronous dispatch results.
type pendingRequest struct {
	TraceID    string
	TargetNode string
	Ch         chan any
	CreatedAt  time.Time
	closed     bool       // Indicates if the channel has been closed
	mu         sync.Mutex // Protects closed flag
}

var errRegistryStopped = errors.New("agent registry stopped")

func newAgentRegistry(logger *slog.Logger) *AgentRegistry {
	return &AgentRegistry{
		connections: make(map[string]*AgentConnection),
		logger:      logger,
		stopCh:      make(chan struct{}),
	}
}

func (r *AgentRegistry) SetDisconnectHandler(handler func(nodeID string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onDisconnect = handler
}

// Stop signals all background goroutines to exit and waits for them to finish.
func (r *AgentRegistry) Stop() {
	r.failAllPendingRequests(errRegistryStopped)
	close(r.stopCh)
	r.wg.Wait()
}

// Register adds an agent connection, kicking any existing connection for the same nodeID.
func (r *AgentRegistry) Register(nodeID string, stream pb.ControlService_SyncServer, sysInfo *pb.SystemInfo, peerAddr string) *AgentConnection {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for existing connection (kick mechanism)
	if oldConn, ok := r.connections[nodeID]; ok {
		r.logger.Warn("Kicking off old connection", "nodeID", nodeID, "old_peer", oldConn.PeerAddr)

		// Mark old connection as replaced so its Sync handler can exit on the next Recv iteration.
		// This must happen before the redirect goroutine starts to ensure the old Sync loop
		// does not race to process further messages after it detects the flag.
		oldConn.replaced.Store(true)

		// Send disconnect command to old connection (best-effort, fire-and-forget).
		// The goroutine is bounded by gRPC keepalive (server: Time=30s, Timeout=5s).
		go func() {
			disconnectCmd := &pb.ServerCommand{
				Payload: &pb.ServerCommand_RedirectCommand{
					RedirectCommand: &pb.RedirectCommand{
						Reason: "connection replaced by new session",
					},
				},
			}
			_ = oldConn.Send(disconnectCmd)
			r.logger.Info("Sent disconnect command to old connection", "nodeID", nodeID)
		}()
	}

	// Create and store new connection
	conn := &AgentConnection{
		NodeID:     nodeID,
		Stream:     stream,
		PeerAddr:   peerAddr,
		LastSeen:   time.Now(),
		SystemInfo: sysInfo,
	}
	r.connections[nodeID] = conn

	return conn
}

func (r *AgentRegistry) Unregister(nodeID string, targetConn *AgentConnection) {
	r.mu.Lock()
	var disconnected bool
	handler := r.onDisconnect
	defer r.mu.Unlock()

	// Only delete if the current connection matches the one being unregistered
	if currentConn, ok := r.connections[nodeID]; ok && (targetConn == nil || currentConn == targetConn) {
		delete(r.connections, nodeID)
		r.logger.Info("Agent unregistered", "nodeID", nodeID)
		disconnected = true
	}

	if disconnected {
		r.failPendingRequestsForNodeLocked(nodeID, fmt.Errorf("agent disconnected: %s", nodeID))
		if handler != nil {
			go handler(nodeID)
		}
	}
}

func (r *AgentRegistry) SendCommand(nodeID string, cmd *pb.ServerCommand) error {
	r.mu.RLock()
	conn, ok := r.connections[nodeID]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent not connected: %s", nodeID)
	}

	return conn.Send(cmd)
}

func (r *AgentRegistry) GetConnectedAgents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]string, 0, len(r.connections))
	for nodeID := range r.connections {
		agents = append(agents, nodeID)
	}
	slices.Sort(agents)
	return agents
}

func (r *AgentRegistry) IsConnected(nodeID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.connections[nodeID]
	return ok
}

// Disconnect asks a node's live session to end by marking its connection as replaced —
// the Sync handler exits on the next received message (see the replaced check in its recv
// loop), so the agent reconnects right away and gets re-validated. Returns false when the
// node has no live session on this server.
func (r *AgentRegistry) Disconnect(nodeID string) bool {
	conn, ok := r.GetConnection(nodeID)
	if !ok || conn == nil {
		return false
	}
	conn.replaced.Store(true)
	return true
}

func (r *AgentRegistry) GetConnection(nodeID string) (*AgentConnection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.connections[nodeID]
	return conn, ok
}

func (r *AgentRegistry) Snapshot(nodeID string) (*AgentSnapshot, bool) {
	r.mu.RLock()
	conn, ok := r.connections[nodeID]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	return &AgentSnapshot{
		NodeID:         conn.NodeID,
		PeerAddr:       conn.PeerAddr,
		LastSeen:       conn.LastSeen,
		SystemInfo:     cloneSystemInfo(conn.SystemInfo),
		ResourceReport: cloneResourceReport(conn.ResourceReport),
	}, true
}

func (r *AgentRegistry) SnapshotAll() map[string]AgentSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshots := make(map[string]AgentSnapshot, len(r.connections))
	for nodeID, conn := range r.connections {
		conn.mu.Lock()
		snapshots[nodeID] = AgentSnapshot{
			NodeID:         conn.NodeID,
			PeerAddr:       conn.PeerAddr,
			LastSeen:       conn.LastSeen,
			SystemInfo:     cloneSystemInfo(conn.SystemInfo),
			ResourceReport: cloneResourceReport(conn.ResourceReport),
		}
		conn.mu.Unlock()
	}
	return snapshots
}

func cloneSystemInfo(info *pb.SystemInfo) *pb.SystemInfo {
	if info == nil {
		return nil
	}
	return proto.Clone(info).(*pb.SystemInfo)
}

func cloneResourceReport(report *pb.ResourceReport) *pb.ResourceReport {
	if report == nil {
		return nil
	}
	return proto.Clone(report).(*pb.ResourceReport)
}

// CreatePendingRequest creates a response channel for a pending synchronous
// execution keyed by traceID.
func (r *AgentRegistry) CreatePendingRequest(traceID string, targetNode string) chan any {
	ch := make(chan any, 1) // Buffered to prevent goroutine leak
	pr := &pendingRequest{
		TraceID:    traceID,
		TargetNode: targetNode,
		Ch:         ch,
		CreatedAt:  time.Now(),
		closed:     false,
	}
	r.pendingRequests.Store(traceID, pr)
	return ch
}

// DeletePendingRequest removes a pending request without closing the channel.
// The channel will be garbage collected when no longer referenced.
func (r *AgentRegistry) DeletePendingRequest(traceID string) {
	value, ok := r.pendingRequests.Load(traceID)
	if ok {
		pr := value.(*pendingRequest)
		pr.mu.Lock()
		pr.closed = true
		pr.mu.Unlock()
	}
	r.pendingRequests.Delete(traceID)
}

// DeliverResult delivers a result to the pending channel identified by traceID.
func (r *AgentRegistry) DeliverResult(traceID string, result any) {
	value, ok := r.pendingRequests.Load(traceID)
	if !ok {
		r.logger.Debug("No pending request for result", "trace_id", traceID)
		return
	}

	pr := value.(*pendingRequest)
	pr.mu.Lock()
	defer pr.mu.Unlock()

	if pr.closed {
		r.logger.Debug("Pending request already closed", "trace_id", traceID)
		return
	}

	select {
	case pr.Ch <- result:
		// Successfully delivered
	default:
		// Channel full, log and continue
		r.logger.Warn("Failed to deliver result, channel full", "trace_id", traceID)
	}
}

func (r *AgentRegistry) failPendingRequest(traceID string, err error) {
	if err == nil {
		err = fmt.Errorf("pending request failed")
	}
	value, ok := r.pendingRequests.Load(traceID)
	if !ok {
		return
	}
	pr := value.(*pendingRequest)
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.closed {
		return
	}
	pr.closed = true
	select {
	case pr.Ch <- err:
	default:
	}
	r.pendingRequests.Delete(traceID)
}

func (r *AgentRegistry) failAllPendingRequests(err error) {
	r.pendingRequests.Range(func(key, value any) bool {
		r.failPendingRequest(key.(string), err)
		return true
	})
}

func (r *AgentRegistry) failPendingRequestsForNodeLocked(nodeID string, err error) {
	var ids []string
	r.pendingRequests.Range(func(key, value any) bool {
		pr := value.(*pendingRequest)
		if pr.TargetNode == nodeID {
			ids = append(ids, key.(string))
		}
		return true
	})
	for _, id := range ids {
		r.failPendingRequest(id, err)
	}
}

// StartPendingRequestCleanup starts a background goroutine that periodically
// cleans up stale pending requests to prevent memory leaks.
// Note: We don't close channels to avoid nil returns; instead we mark as closed
// and let the timeout mechanism in defaultWaitResult handle expiration.
func (r *AgentRegistry) StartPendingRequestCleanup(interval, ttl time.Duration) {
	r.logger.Info("Starting pending request cleanup", "interval", interval, "ttl", ttl)
	defer r.wg.Done()
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("fatal panic in StartPendingRequestCleanup loop",
				"panic", rec,
				"stack", string(debug.Stack()),
			)
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						r.logger.Error("panic recovered in pending request cleanup tick",
							"panic", rec,
							"stack", string(debug.Stack()),
						)
					}
				}()

				now := time.Now()
				var cleaned int

				r.pendingRequests.Range(func(key, value any) bool {
					pr := value.(*pendingRequest)
					if now.Sub(pr.CreatedAt) > ttl {
						pr.mu.Lock()
						pr.closed = true
						pr.mu.Unlock()
						r.pendingRequests.Delete(key)
						cleaned++
					}
					return true
				})

				if cleaned > 0 {
					r.logger.Debug("Cleaned up stale pending requests", "count", cleaned)
				}
			}()
		case <-r.stopCh:
			return
		}
	}
}

func (r *AgentRegistry) StartHealthChecker(checkInterval, pingInterval, staleTimeout time.Duration) {
	r.logger.Info("Starting connection health checker",
		"check_interval", checkInterval,
		"ping_interval", pingInterval,
		"stale_timeout", staleTimeout)

	defer r.wg.Done()
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("fatal panic in StartHealthChecker loop",
				"panic", rec,
				"stack", string(debug.Stack()),
			)
		}
	}()

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						r.logger.Error("panic recovered in health check tick",
							"panic", rec,
							"stack", string(debug.Stack()),
						)
					}
				}()
				r.checkHealth(pingInterval, staleTimeout)
			}()
		case <-r.stopCh:
			return
		}
	}
}

func (r *AgentRegistry) checkHealth(pingInterval, staleTimeout time.Duration) {
	r.mu.RLock()
	// Create a copy of connections to iterate safely without holding lock too long
	var staleHosts []string
	var pingTargets []*AgentConnection
	now := time.Now()

	for nodeID, conn := range r.connections {
		conn.mu.Lock()
		lastSeen := conn.LastSeen
		conn.mu.Unlock()

		timeSinceLastSeen := now.Sub(lastSeen)

		if timeSinceLastSeen > staleTimeout {
			staleHosts = append(staleHosts, nodeID)
		} else if timeSinceLastSeen > pingInterval {
			pingTargets = append(pingTargets, conn)
		}
	}
	r.mu.RUnlock()

	// Handle stale connections
	for _, nodeID := range staleHosts {
		r.logger.Warn("Agent connection is stale, removing",
			"nodeID", nodeID)
		r.Unregister(nodeID, nil) // nil means force remove regardless of connection matching
	}

	// Log idle connections; gRPC keepalive pings handle transport-layer liveness
	for _, conn := range pingTargets {
		// Just logging potential issue if gRPC keepalive failed to kill it.
		r.logger.Debug("Agent connection idle, relying on gRPC keepalive",
			"nodeID", conn.NodeID)
	}
}
