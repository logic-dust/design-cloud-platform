package control

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/grpc"
)

// blockableSyncServer is a pb.ControlService_SyncServer whose Recv blocks on a
// channel. Closing the msgs channel makes Recv return io.EOF, simulating a
// clean stream close. Unlike fakeSyncServer it can be paused mid-stream,
// which lets tests control the exact point at which the replaced flag is set.
type blockableSyncServer struct {
	grpc.ServerStream
	ctx  context.Context
	msgs chan *pb.AgentMessage

	mu   sync.Mutex
	sent []*pb.ServerCommand
}

func (s *blockableSyncServer) Send(cmd *pb.ServerCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, cmd)
	return nil
}

func (s *blockableSyncServer) Recv() (*pb.AgentMessage, error) {
	msg, ok := <-s.msgs
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

func (s *blockableSyncServer) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// --- Tests ------------------------------------------------------------------

// A revoked node must lose its live session: Disconnect flips the replaced flag that the
// Sync handler's recv loop watches, so the agent reconnects and gets re-validated.
func TestDisconnectMarksLiveConnectionReplaced(t *testing.T) {
	t.Parallel()

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)

	conn := registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-1")
	if conn.replaced.Load() {
		t.Fatal("fresh connection must not be marked replaced")
	}

	if !registry.Disconnect("agent-1") {
		t.Fatal("Disconnect must report true for a connected node")
	}
	if !conn.replaced.Load() {
		t.Fatal("Disconnect must mark the connection as replaced")
	}

	if registry.Disconnect("agent-absent") {
		t.Fatal("Disconnect must report false for a node without a live session")
	}
}

func TestRegisterFirstConnectionIsNotReplaced(t *testing.T) {
	t.Parallel()

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)

	conn := registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-1")
	if conn.replaced.Load() {
		t.Fatal("first connection should not be marked as replaced")
	}
}

func TestRegisterReplacesOldConnectionAndMarksReplaced(t *testing.T) {
	t.Parallel()

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)

	oldConn := registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-1")
	newConn := registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-2")

	if !oldConn.replaced.Load() {
		t.Fatal("old connection replaced flag was not set")
	}
	if newConn.replaced.Load() {
		t.Fatal("new connection should not be marked as replaced")
	}
	if !registry.IsConnected("agent-1") {
		t.Fatal("agent-1 should still be connected after replacement")
	}
}

func TestUnregisterDoesNotRemoveNewConnectionOnOldConnExit(t *testing.T) {
	t.Parallel()

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)

	oldConn := registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-1")
	registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-2")

	// Simulate what Sync() does on exit: unregister with the old conn reference.
	registry.Unregister("agent-1", oldConn)

	if !registry.IsConnected("agent-1") {
		t.Fatal("new connection was incorrectly removed when old Sync exited")
	}
}

func TestSyncExitsWhenConnectionReplaced(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-1", fingerprint)

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)

	server := &Server{
		registry:    registry,
		nodes:       repos.Node,
		connHandler: &fakeConnectionSyncHandler{},
		nodeID:      "server-a",
		logger:      slog.Default(),
	}

	// Old stream: deliver Register first, then block until we push the next message.
	msgs := make(chan *pb.AgentMessage, 4)
	msgs <- &pb.AgentMessage{Payload: &pb.AgentMessage_RegisterRequest{RegisterRequest: &pb.RegisterRequest{
		NodeId: "agent-1",
	}}}
	oldStream := &blockableSyncServer{ctx: ctx, msgs: msgs}

	syncDone := make(chan error, 1)
	go func() { syncDone <- server.Sync(oldStream) }()

	// Wait for agent-1 to appear in the registry (Register message processed).
	deadline := time.Now().Add(5 * time.Second)
	for !registry.IsConnected("agent-1") {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for agent-1 to register")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Register a new connection for the same node — marks old conn as replaced.
	registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-new")

	// Unblock old Sync's Recv() so the replaced check fires.
	msgs <- &pb.AgentMessage{Payload: &pb.AgentMessage_HeartbeatRequest{HeartbeatRequest: &pb.HeartbeatRequest{
		ReportedAt: time.Now().Unix(),
	}}}

	select {
	case err := <-syncDone:
		if err == nil {
			t.Fatal("old Sync() should return a non-nil error when replaced")
		}
		if !strings.Contains(err.Error(), "replaced") {
			t.Fatalf("old Sync() error = %v, want error containing \"replaced\"", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for old Sync() to exit after replacement")
	}

	// After Sync's deferred Unregister, new connection must still be present.
	if !registry.IsConnected("agent-1") {
		t.Fatal("new connection should still be in registry after old Sync exits")
	}
}
