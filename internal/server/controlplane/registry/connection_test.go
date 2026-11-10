package registry

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/grpc"
)

type testSyncServer struct {
	grpc.ServerStream
}

func (s *testSyncServer) Send(*pb.ServerCommand) error    { return nil }
func (s *testSyncServer) Recv() (*pb.AgentMessage, error) { return nil, io.EOF }
func (s *testSyncServer) Context() context.Context        { return context.Background() }

func TestDisconnectMarksLiveConnectionReplaced(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	conn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")
	if conn.IsReplaced() {
		t.Fatal("fresh connection must not be marked replaced")
	}
	if !registry.Disconnect("agent-1") {
		t.Fatal("Disconnect must report true for a connected node")
	}
	if !conn.IsReplaced() {
		t.Fatal("Disconnect must mark the connection as replaced")
	}
	if registry.Disconnect("agent-absent") {
		t.Fatal("Disconnect must report false for an absent node")
	}
}

func TestRegisterFirstConnectionIsNotReplaced(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	if conn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1"); conn.IsReplaced() {
		t.Fatal("first connection should not be marked replaced")
	}
}

func TestRegisterReplacesOldConnection(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	oldConn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")
	newConn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-2")

	if !oldConn.IsReplaced() {
		t.Fatal("old connection must be marked replaced")
	}
	if newConn.IsReplaced() {
		t.Fatal("new connection must remain active")
	}
	if !registry.IsConnected("agent-1") {
		t.Fatal("agent-1 should remain connected")
	}
}

func TestUnregisterOldConnectionKeepsReplacement(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	oldConn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")
	registry.Register("agent-1", &testSyncServer{}, nil, "peer-2")
	registry.Unregister("agent-1", oldConn)

	if !registry.IsConnected("agent-1") {
		t.Fatal("new connection was removed when old stream exited")
	}
}

func TestConnection_HeartbeatFlushThrottling(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)

	conn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")
	initialFlush := conn.LastHeartbeatFlushedAt()
	if initialFlush.IsZero() {
		t.Fatal("initial flush timestamp must not be zero after register")
	}

	threshold := 50 * time.Millisecond
	now := initialFlush.Add(10 * time.Millisecond)

	// Within threshold: ShouldFlushHeartbeat is false.
	if conn.ShouldFlushHeartbeat(now, threshold) {
		t.Fatal("ShouldFlushHeartbeat must be false when within threshold")
	}

	// Beyond threshold: ShouldFlushHeartbeat is true.
	now = initialFlush.Add(60 * time.Millisecond)
	if !conn.ShouldFlushHeartbeat(now, threshold) {
		t.Fatal("ShouldFlushHeartbeat must be true when beyond threshold")
	}

	// Mark flushed updates the timestamp and resets debounce.
	conn.MarkHeartbeatFlushed(now)
	if got := conn.LastHeartbeatFlushedAt(); !got.Equal(now) {
		t.Fatalf("LastHeartbeatFlushedAt = %v, want %v", got, now)
	}

	// Immediately after flush: ShouldFlushHeartbeat is false again.
	if conn.ShouldFlushHeartbeat(now.Add(10*time.Millisecond), threshold) {
		t.Fatal("ShouldFlushHeartbeat must be false immediately after MarkHeartbeatFlushed")
	}

	// Snapshot reflects the flushed timestamp.
	snap, ok := registry.Snapshot("agent-1")
	if !ok {
		t.Fatal("Snapshot must be found")
	}
	if !snap.LastHeartbeatFlushedAt.Equal(now) {
		t.Fatalf("Snapshot.LastHeartbeatFlushedAt = %v, want %v", snap.LastHeartbeatFlushedAt, now)
	}
}

func TestConnection_MetricsFlushThrottling(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	conn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")

	threshold := 3 * time.Minute
	now := time.Now()

	// 首帧必须落库，保证冷启动后侧表立即可读。
	if !conn.ShouldFlushMetrics(now, threshold) {
		t.Fatal("first metrics frame must be persisted")
	}
	// 未标记成功时仍可写，写库失败可在下一帧重试。
	if !conn.ShouldFlushMetrics(now.Add(time.Second), threshold) {
		t.Fatal("unmarked flush must stay eligible so a failed DB write retries")
	}
	conn.MarkMetricsFlushed(now)

	if conn.ShouldFlushMetrics(now.Add(time.Minute), threshold) {
		t.Fatal("metrics flush must be throttled within the interval")
	}
	if !conn.ShouldFlushMetrics(now.Add(threshold), threshold) {
		t.Fatal("metrics flush must resume once the interval elapsed")
	}
	conn.MarkMetricsFlushed(now.Add(threshold))
	if conn.ShouldFlushMetrics(now.Add(threshold+time.Minute), threshold) {
		t.Fatal("metrics flush must be throttled again after a successful flush")
	}
}
