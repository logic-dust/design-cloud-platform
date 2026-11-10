package registry

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/grpc"
)

// blockingSyncServer simulates an Agent whose gRPC flow control stalls Send.
type blockingSyncServer struct {
	grpc.ServerStream
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSyncServer() *blockingSyncServer {
	return &blockingSyncServer{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockingSyncServer) Send(*pb.ServerCommand) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}
func (s *blockingSyncServer) Recv() (*pb.AgentMessage, error) { return nil, io.EOF }
func (s *blockingSyncServer) Context() context.Context        { return context.Background() }

func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked behind a stalled Send", what)
	}
}

func TestStateAccessNotBlockedBySlowSend(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	stream := newBlockingSyncServer()
	conn := registry.Register("agent-slow", stream, &pb.SystemInfo{}, "peer-slow")
	registry.Register("agent-ok", &testSyncServer{}, nil, "peer-ok")

	sendDone := make(chan error, 1)
	go func() { sendDone <- conn.Send(&pb.ServerCommand{}) }()
	<-stream.entered

	within(t, 2*time.Second, "Touch", func() { conn.Touch(time.Now()) })
	within(t, 2*time.Second, "UpdateFrame", func() { conn.UpdateFrame(time.Now(), &pb.Metrics{CpuUtil: 0.5}) })
	within(t, 2*time.Second, "Snapshot", func() {
		ids := registry.ConnectedNodeIDs()
		if len(ids) != 2 {
			t.Errorf("ConnectedNodeIDs returned %d entries, want 2", len(ids))
		}
		snap, ok := registry.Snapshot("agent-slow")
		if !ok {
			t.Fatal("Snapshot(agent-slow) not found")
		}
		if got := snap.LatestFrame.GetCpuUtil(); got != 0.5 {
			t.Errorf("snapshot latest frame cpu = %v, want 0.5", got)
		}
	})
	within(t, 2*time.Second, "checkHealth", func() { registry.checkHealth(time.Minute, time.Hour) })
	within(t, 2*time.Second, "Register", func() { registry.Register("agent-new", &testSyncServer{}, nil, "peer-new") })

	close(stream.release)
	if err := <-sendDone; err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestConcurrentSendAndStateUpdates(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	conn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = conn.Send(&pb.ServerCommand{})
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				conn.UpdateFrame(time.Now(), &pb.Metrics{ActiveTasks: int64(i)})
				conn.Touch(time.Now())
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_, _ = registry.Snapshot("agent-1")
				registry.checkHealth(time.Minute, time.Hour)
			}
		}()
	}
	wg.Wait()
}

func TestCheckHealthRemovesStaleConnection(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	conn := registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")
	conn.Touch(time.Now().Add(-time.Hour))

	registry.checkHealth(time.Second, time.Minute)

	if registry.IsConnected("agent-1") {
		t.Fatal("stale connection should be unregistered")
	}
}

func TestCheckHealthKeepsConnectionReregisteredDuringScan(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	stale := registry.Register("agent-1", &testSyncServer{}, nil, "peer-old")
	stale.Touch(time.Now().Add(-time.Hour))

	var fresh *Connection
	registry.afterHealthScan = func() {
		// The Agent reconnects after the scan judged the old stream stale.
		fresh = registry.Register("agent-1", &testSyncServer{}, nil, "peer-new")
	}
	registry.checkHealth(time.Second, time.Minute)

	current, ok := registry.GetConnection("agent-1")
	if !ok {
		t.Fatal("re-registered connection was evicted by the stale-connection cleanup")
	}
	if current != fresh {
		t.Fatal("registry should hold the re-registered connection")
	}
}

func TestUnregisterNilTargetIsNoop(t *testing.T) {
	t.Parallel()

	registry := New(slog.Default())
	t.Cleanup(registry.Stop)
	registry.Register("agent-1", &testSyncServer{}, nil, "peer-1")
	registry.Unregister("agent-1", nil)

	if !registry.IsConnected("agent-1") {
		t.Fatal("Unregister with nil target must not remove the registered connection")
	}
}
