// Package registry manages live Agent connections and synchronous request correlation.
package registry

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/protobuf/proto"
)

// Connection represents one live Agent Sync stream.
//
// NodeID, Stream and PeerAddr are fixed at registration. Two independent locks guard the rest:
//   - sendMu serializes Stream.Send (gRPC streams are not safe for concurrent Send) and may be
//     held for as long as a slow Agent's flow control blocks the write;
//   - stateMu guards lastSeen/systemInfo/latestFrame and is only held for field copies, so
//     heartbeat updates, snapshots and health checks never wait on a blocked Send.
type Connection struct {
	NodeID   string
	Stream   pb.ControlService_SyncServer
	PeerAddr string

	sendMu sync.Mutex

	stateMu                sync.Mutex
	lastSeen               time.Time
	lastHeartbeatFlushedAt time.Time
	lastMetricsFlushedAt   time.Time
	systemInfo             *pb.SystemInfo
	latestFrame            *pb.Metrics

	replaced atomic.Bool
}

func (c *Connection) Send(command *pb.ServerCommand) error {
	if c == nil {
		return fmt.Errorf("agent connection is nil")
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.Stream.Send(command)
}

func (c *Connection) StreamContext() context.Context {
	if c == nil || c.Stream == nil {
		return context.Background()
	}
	return c.Stream.Context()
}

func (c *Connection) IsReplaced() bool {
	return c != nil && c.replaced.Load()
}

func (c *Connection) Touch(seenAt time.Time) {
	if c == nil {
		return
	}
	c.stateMu.Lock()
	c.lastSeen = seenAt
	c.stateMu.Unlock()
}

func (c *Connection) UpdateFrame(seenAt time.Time, metrics *pb.Metrics) {
	if c == nil {
		return
	}
	c.stateMu.Lock()
	c.lastSeen = seenAt
	c.latestFrame = metrics
	c.stateMu.Unlock()
}

// LastSeen returns the time of the last message received on this stream.
func (c *Connection) LastSeen() time.Time {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.lastSeen
}

// ShouldFlushHeartbeat reports whether a heartbeat DB flush should be performed,
// based on whether the elapsed time since the last successful flush exceeds threshold.
func (c *Connection) ShouldFlushHeartbeat(now time.Time, threshold time.Duration) bool {
	if c == nil {
		return false
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.lastHeartbeatFlushedAt.IsZero() || now.Sub(c.lastHeartbeatFlushedAt) >= threshold {
		return true
	}
	return false
}

// MarkHeartbeatFlushed records the timestamp of a successful heartbeat DB flush.
func (c *Connection) MarkHeartbeatFlushed(flushedAt time.Time) {
	if c == nil {
		return
	}
	c.stateMu.Lock()
	c.lastHeartbeatFlushedAt = flushedAt
	c.stateMu.Unlock()
}

// LastHeartbeatFlushedAt returns the timestamp of the last DB flush for this connection.
func (c *Connection) LastHeartbeatFlushedAt() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.lastHeartbeatFlushedAt
}

// ShouldFlushMetrics reports whether node metrics should be persisted, based on
// the elapsed time since the last successful flush. The in-memory snapshot and
// alert evaluation still update on every frame; only the DB write is throttled.
func (c *Connection) ShouldFlushMetrics(now time.Time, threshold time.Duration) bool {
	if c == nil {
		return false
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.lastMetricsFlushedAt.IsZero() || now.Sub(c.lastMetricsFlushedAt) >= threshold
}

// MarkMetricsFlushed records the timestamp of a successful node metrics DB flush.
func (c *Connection) MarkMetricsFlushed(flushedAt time.Time) {
	if c == nil {
		return
	}
	c.stateMu.Lock()
	c.lastMetricsFlushedAt = flushedAt
	c.stateMu.Unlock()
}

// Snapshot is a read-only copy of a connected Agent's runtime state.
type Snapshot struct {
	NodeID                 string
	PeerAddr               string
	LastSeen               time.Time
	LastHeartbeatFlushedAt time.Time
	SystemInfo             *pb.SystemInfo
	LatestFrame            *pb.Metrics
}

// Registry owns live connections, pending requests, and their background workers.
type Registry struct {
	connections     map[string]*Connection
	mu              sync.RWMutex
	pendingRequests sync.Map
	logger          *slog.Logger
	stopCh          chan struct{}
	wg              sync.WaitGroup
	onDisconnect    func(nodeID string)

	// afterHealthScan runs between the stale scan and the unregister step of checkHealth.
	// Tests use it to interleave a re-registration; it is nil in production.
	afterHealthScan func()
}

func New(logger *slog.Logger) *Registry {
	return &Registry{
		connections: make(map[string]*Connection),
		logger:      logger,
		stopCh:      make(chan struct{}),
	}
}

func (r *Registry) SetDisconnectHandler(handler func(nodeID string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onDisconnect = handler
}

// Register adds an Agent connection, replacing any existing connection for the node.
func (r *Registry) Register(nodeID string, stream pb.ControlService_SyncServer, systemInfo *pb.SystemInfo, peerAddr string) *Connection {
	r.mu.Lock()
	defer r.mu.Unlock()

	if oldConn, ok := r.connections[nodeID]; ok {
		r.logger.Warn("Kicking off old connection", "nodeID", nodeID, "old_peer", oldConn.PeerAddr)
		oldConn.replaced.Store(true)

		// Best-effort notification; gRPC keepalive bounds a blocked transport.
		go func() {
			_ = oldConn.Send(&pb.ServerCommand{
				Payload: &pb.ServerCommand_RedirectCommand{
					RedirectCommand: &pb.RedirectCommand{
						Reason: "connection replaced by new session",
					},
				},
			})
			r.logger.Info("Sent disconnect command to old connection", "nodeID", nodeID)
		}()
	}

	conn := &Connection{
		NodeID:                 nodeID,
		Stream:                 stream,
		PeerAddr:               peerAddr,
		lastSeen:               time.Now(),
		lastHeartbeatFlushedAt: time.Now(),
		systemInfo:             systemInfo,
	}
	r.connections[nodeID] = conn
	return conn
}

// Unregister removes target only while it is still the registered connection for nodeID.
// A connection that has already been replaced by a newer registration is left untouched,
// so a late cleanup for an old stream never evicts its successor. A nil target is a no-op.
func (r *Registry) Unregister(nodeID string, target *Connection) {
	if target == nil {
		return
	}
	r.mu.Lock()
	handler := r.onDisconnect
	disconnected := false
	if current, ok := r.connections[nodeID]; ok && current == target {
		delete(r.connections, nodeID)
		r.logger.Info("Agent unregistered", "nodeID", nodeID)
		disconnected = true
	}
	if disconnected {
		r.failPendingRequestsForNodeLocked(nodeID, fmt.Errorf("agent disconnected: %s", nodeID))
	}
	r.mu.Unlock()

	if disconnected && handler != nil {
		go handler(nodeID)
	}
}

func (r *Registry) SendCommand(nodeID string, command *pb.ServerCommand) error {
	conn, ok := r.GetConnection(nodeID)
	if !ok {
		return fmt.Errorf("agent not connected: %s", nodeID)
	}
	return conn.Send(command)
}

func (r *Registry) ConnectedNodeIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodeIDs := make([]string, 0, len(r.connections))
	for nodeID := range r.connections {
		nodeIDs = append(nodeIDs, nodeID)
	}
	slices.Sort(nodeIDs)
	return nodeIDs
}

func (r *Registry) IsConnected(nodeID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.connections[nodeID]
	return ok
}

// Disconnect marks a live session as replaced so its Sync loop exits and reconnects.
func (r *Registry) Disconnect(nodeID string) bool {
	conn, ok := r.GetConnection(nodeID)
	if !ok || conn == nil {
		return false
	}
	conn.replaced.Store(true)
	return true
}

func (r *Registry) GetConnection(nodeID string) (*Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.connections[nodeID]
	return conn, ok
}

func (r *Registry) Snapshot(nodeID string) (*Snapshot, bool) {
	r.mu.RLock()
	conn, ok := r.connections[nodeID]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return conn.snapshot(), true
}

func (c *Connection) snapshot() *Snapshot {
	c.stateMu.Lock()
	lastSeen, flushedAt, systemInfo, metrics := c.lastSeen, c.lastHeartbeatFlushedAt, c.systemInfo, c.latestFrame
	c.stateMu.Unlock()
	// Messages are replaced wholesale and never mutated in place, so cloning outside the lock is safe.
	return &Snapshot{
		NodeID:                 c.NodeID,
		PeerAddr:               c.PeerAddr,
		LastSeen:               lastSeen,
		LastHeartbeatFlushedAt: flushedAt,
		SystemInfo:             cloneSystemInfo(systemInfo),
		LatestFrame:            cloneFrame(metrics),
	}
}

func cloneSystemInfo(info *pb.SystemInfo) *pb.SystemInfo {
	if info == nil {
		return nil
	}
	return proto.Clone(info).(*pb.SystemInfo)
}

func cloneFrame(metrics *pb.Metrics) *pb.Metrics {
	if metrics == nil {
		return nil
	}
	return proto.Clone(metrics).(*pb.Metrics)
}
