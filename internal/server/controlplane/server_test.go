package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

type fakeSyncServer struct {
	grpc.ServerStream
	ctx      context.Context
	incoming []*pb.AgentMessage
	sent     []*pb.ServerCommand
	sendErr  error
	onSend   func(*pb.ServerCommand)
}

func (s *fakeSyncServer) Send(cmd *pb.ServerCommand) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	if s.onSend != nil {
		s.onSend(cmd)
	}
	s.sent = append(s.sent, cmd)
	return nil
}

func (s *fakeSyncServer) Recv() (*pb.AgentMessage, error) {
	if len(s.incoming) == 0 {
		return nil, io.EOF
	}
	msg := s.incoming[0]
	s.incoming = s.incoming[1:]
	return msg, nil
}

func (s *fakeSyncServer) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func peerContextWithCertificate(t *testing.T) (context.Context, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "agent-a"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	sum := sha256.Sum256(cert.Raw)
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
		}},
	})
	return ctx, hex.EncodeToString(sum[:])
}

type fakeConnectionRuntimeHandler struct {
	reconcileCalls int
	reconcileNode  string
	vncSeats       []contract.VNCSeatInfo
	err            error
}

func (h *fakeConnectionRuntimeHandler) SyncConnectionReport(_ context.Context, nodeID string, report contract.ConnectionReport) error {
	if h.err != nil {
		return h.err
	}
	h.reconcileCalls++
	h.reconcileNode = nodeID
	h.vncSeats = append([]contract.VNCSeatInfo(nil), report.VNCSeats...)
	return nil
}

func newTestRepos(t *testing.T) (*db.Store, *db.Repos) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store, db.NewRepos(store)
}

func createRegisteredNode(t *testing.T, repo *db.NodeRepo, nodeID, fingerprint string) {
	t.Helper()
	if err := repo.Create(context.Background(), &model.Node{
		NodeID:          nodeID,
		IsServer:        false,
		Status:          model.NodeStatusOffline,
		Groups:          []string{"role"},
		CertFingerprint: fingerprint,
	}); err != nil {
		t.Fatalf("create registered node: %v", err)
	}
}

func TestSyncSendsRegisterAckThenHeartbeatAck(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-a", fingerprint)
	handler := &fakeConnectionRuntimeHandler{}
	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-a",
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: handler},
	}
	stream := &fakeSyncServer{
		ctx: ctx,
		incoming: []*pb.AgentMessage{
			{Payload: &pb.AgentMessage_RegisterRequest{RegisterRequest: &pb.RegisterRequest{
				NodeId: "agent-a",
			}}},
			{Payload: &pb.AgentMessage_HeartbeatRequest{HeartbeatRequest: &pb.HeartbeatRequest{
				ReportedAt: time.Now().Unix(),
			}}},
		},
	}

	if err := server.Sync(stream); !errors.Is(err, io.EOF) {
		t.Fatalf("sync error = %v, want EOF", err)
	}

	if got := len(stream.sent); got != 2 {
		t.Fatalf("sent commands = %d, want register ack and heartbeat ack", got)
	}
	if stream.sent[0].GetRegisterResponse() == nil {
		t.Fatalf("first command = %T, want register ack", stream.sent[0].GetPayload())
	}
	if stream.sent[1].GetHeartbeatResponse() == nil {
		t.Fatalf("second command = %T, want heartbeat ack", stream.sent[1].GetPayload())
	}
}

func TestSyncHandlesConnectionReport(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-a", fingerprint)
	handler := &fakeConnectionRuntimeHandler{}
	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-a",
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: handler},
	}
	stream := &fakeSyncServer{
		ctx: ctx,
		incoming: []*pb.AgentMessage{
			{Payload: &pb.AgentMessage_RegisterRequest{RegisterRequest: &pb.RegisterRequest{
				NodeId: "agent-a",
			}}},
			{Payload: &pb.AgentMessage_ConnectionReport{ConnectionReport: &pb.ConnectionReport{
				ReportedAt: time.Now().Unix(),
				VncSeats: []*pb.VNCSeatInfo{{
					ConnectionId: 10,
					Name:         "desktop",
					Username:     "alice",
					Port:         5901,
					Pid:          123,
				}},
			}}},
		},
	}

	if err := server.Sync(stream); !errors.Is(err, io.EOF) {
		t.Fatalf("sync error = %v, want EOF", err)
	}

	if handler.reconcileCalls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", handler.reconcileCalls)
	}
	if handler.reconcileNode != "agent-a" {
		t.Fatalf("reconcile node = %q, want agent-a", handler.reconcileNode)
	}
	if got := len(handler.vncSeats); got != 1 {
		t.Fatalf("vnc desktops = %d, want 1", got)
	}
	if handler.vncSeats[0].ConnectionID != 10 || handler.vncSeats[0].PID != 123 {
		t.Fatalf("vnc desktop = %+v, want connection id 10 and pid 123", handler.vncSeats[0])
	}
}

func TestTriggerConnectionCollectSendsCollectOnly(t *testing.T) {
	t.Parallel()

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: &fakeConnectionRuntimeHandler{}},
	}
	stream := &fakeSyncServer{}
	registry.Register("agent-a", stream, nil, "test-peer")

	if err := server.TriggerCollect(context.Background(), contract.CollectTargetConnection); err != nil {
		t.Fatalf("trigger collect: %v", err)
	}

	if got := len(stream.sent); got != 1 {
		t.Fatalf("sent commands = %d, want collect only", got)
	}
	cmd := stream.sent[0].GetCollectProbeCommand()
	if cmd == nil {
		t.Fatalf("command = %T, want collect probe", stream.sent[0].GetPayload())
	}
	if cmd.GetProbeName() != "connection-probe" {
		t.Fatalf("probe_name = %q, want connection-probe", cmd.GetProbeName())
	}
	if cmd.GetTraceId() == "" {
		t.Fatalf("trace id is empty")
	}
}

func TestTriggerConnectionCollectNoAgentsIsNoOp(t *testing.T) {
	t.Parallel()

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: &fakeConnectionRuntimeHandler{}},
	}

	if err := server.TriggerCollect(context.Background(), contract.CollectTargetConnection); err != nil {
		t.Fatalf("trigger collect with no agents should not error: %v", err)
	}
}

func TestHandleCommandResultPersistsAckAndDelivers(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	if err := repos.Execution.Create(context.Background(), "request-1", string(execution.ActionUserList), "trace-1", "server-a", "agent-a"); err != nil {
		t.Fatalf("create execution record: %v", err)
	}
	if err := repos.Execution.MarkDispatched(context.Background(), "trace-1"); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}

	registry := agentregistry.New(slog.Default())
	pending := registry.CreatePendingRequest("trace-1", "agent-a")
	stream := &fakeSyncServer{}
	server := &Server{
		Runtime: &Runtime{
			registry:   registry,
			ctx:        context.Background(),
			logger:     slog.Default(),
			executions: repos.Execution,
		},
	}

	server.handleCommandResult(&agentregistry.Connection{NodeID: "agent-a", Stream: stream}, &pb.CommandResult{TraceId: "trace-1",
		Success:  true,
		ExitCode: 0})

	if got := len(stream.sent); got != 1 {
		t.Fatalf("ack commands = %d, want 1", got)
	}
	ack := stream.sent[0].GetCommandResultAck()
	if ack == nil || ack.TraceId != "trace-1" || !ack.Persisted {
		t.Fatalf("ack = %+v, want persisted trace-1", ack)
	}
	select {
	case value := <-pending:
		result, ok := value.(*pb.CommandResult)
		if !ok || result.TraceId != "trace-1" {
			t.Fatalf("pending result = %#v", value)
		}
	default:
		t.Fatalf("expected pending result delivery")
	}
}

func TestHandleCommandResultDeliversSyncResultWithoutRecord(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	registry := agentregistry.New(slog.Default())
	pending := registry.CreatePendingRequest("trace-sync", "agent-a")
	stream := &fakeSyncServer{}
	server := &Server{
		Runtime: &Runtime{
			registry:   registry,
			ctx:        context.Background(),
			logger:     slog.Default(),
			executions: repos.Execution,
		},
	}

	server.handleCommandResult(&agentregistry.Connection{NodeID: "agent-a", Stream: stream}, &pb.CommandResult{TraceId: "trace-sync",
		Success:  true,
		ExitCode: 0})

	ack := stream.sent[0].GetCommandResultAck()
	if ack == nil || ack.Persisted {
		t.Fatalf("ack = %+v, want persisted=false", ack)
	}
	select {
	case value := <-pending:
		result, ok := value.(*pb.CommandResult)
		if !ok || result.TraceId != "trace-sync" {
			t.Fatalf("pending result = %#v", value)
		}
	default:
		t.Fatalf("expected sync pending result delivery")
	}
}

func TestHandleCommandResultDeliversBeforeAck(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	registry := agentregistry.New(slog.Default())
	pending := registry.CreatePendingRequest("trace-sync", "agent-a")
	deliveredBeforeAck := false
	stream := &fakeSyncServer{
		onSend: func(cmd *pb.ServerCommand) {
			if cmd.GetCommandResultAck() == nil {
				return
			}
			select {
			case value := <-pending:
				result, ok := value.(*pb.CommandResult)
				if !ok || result.TraceId != "trace-sync" {
					t.Fatalf("pending result = %#v", value)
				}
				deliveredBeforeAck = true
			default:
				t.Fatalf("expected pending result before result ACK")
			}
		},
	}
	server := &Server{
		Runtime: &Runtime{
			registry:   registry,
			ctx:        context.Background(),
			logger:     slog.Default(),
			executions: repos.Execution,
		},
	}

	server.handleCommandResult(&agentregistry.Connection{NodeID: "agent-a", Stream: stream}, &pb.CommandResult{TraceId: "trace-sync",
		Success:  true,
		ExitCode: 0})

	if !deliveredBeforeAck {
		t.Fatalf("result was not delivered before ACK")
	}
}

type blockableSyncServer struct {
	grpc.ServerStream
	ctx  context.Context
	msgs chan *pb.AgentMessage

	mu   sync.Mutex
	sent []*pb.ServerCommand
}

func (s *blockableSyncServer) Send(command *pb.ServerCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, command)
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

func TestSyncExitsWhenConnectionReplaced(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-1", fingerprint)

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-a",
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: &fakeConnectionRuntimeHandler{}},
	}

	msgs := make(chan *pb.AgentMessage, 4)
	msgs <- &pb.AgentMessage{Payload: &pb.AgentMessage_RegisterRequest{
		RegisterRequest: &pb.RegisterRequest{NodeId: "agent-1"},
	}}
	oldStream := &blockableSyncServer{ctx: ctx, msgs: msgs}

	syncDone := make(chan error, 1)
	go func() { syncDone <- server.Sync(oldStream) }()

	deadline := time.Now().Add(5 * time.Second)
	for !registry.IsConnected("agent-1") {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for agent-1 to register")
		}
		time.Sleep(5 * time.Millisecond)
	}

	registry.Register("agent-1", &fakeSyncServer{}, nil, "peer-new")
	msgs <- &pb.AgentMessage{Payload: &pb.AgentMessage_HeartbeatRequest{
		HeartbeatRequest: &pb.HeartbeatRequest{ReportedAt: time.Now().Unix()},
	}}

	select {
	case err := <-syncDone:
		if err == nil || !strings.Contains(err.Error(), "replaced") {
			t.Fatalf("old Sync() error = %v, want replacement error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for old Sync() to exit after replacement")
	}
	if !registry.IsConnected("agent-1") {
		t.Fatal("new connection should remain after old Sync exits")
	}
}

// ---------------------------------------------------------------------------
// NewServer dependency validation
// ---------------------------------------------------------------------------

type fakeSettingsProvider struct{}

func (fakeSettingsProvider) GetAgentSettingsForNode(_ context.Context, _ string) (*pb.AgentSettings, error) {
	return &pb.AgentSettings{}, nil
}
func (fakeSettingsProvider) GetStorageProbeInterval(_ context.Context) int { return 0 }

type fakeAlertEvaluator struct{}

func (fakeAlertEvaluator) EvalMetrics(context.Context, string, *pb.Metrics) error  { return nil }
func (fakeAlertEvaluator) EvalStorage(context.Context, *pb.StorageProbeItem) error { return nil }

type fakeConnectionReportHandler struct{}

func (fakeConnectionReportHandler) SyncConnectionReport(context.Context, string, contract.ConnectionReport) error {
	return nil
}

// TestNewServer_RequiresServiceDeps 锁定构造期校验：必需业务端口缺失时在启动期
// panic（装配错误前置暴露），runtime 缺失返回错误，CertHooks 为 nil 合法。
//
// 这里不再有 Wire* 回填点：依赖只在 NewServer 构造时进入一次。
func TestNewServer_RequiresServiceDeps(t *testing.T) {
	validDeps := func() ServiceDeps {
		return ServiceDeps{
			SettingsProvider:        fakeSettingsProvider{},
			AlertEvaluator:          fakeAlertEvaluator{},
			Publisher:               &fakePublisher{},
			ConnectionReportHandler: fakeConnectionReportHandler{},
			SettingsPusher:          NewSettingsPusher(&Runtime{logger: slog.Default()}, fakeSettingsProvider{}),
		}
	}
	bareRuntime := func() *Runtime { return &Runtime{logger: slog.Default()} }

	t.Run("nil runtime returns error", func(t *testing.T) {
		if _, err := NewServer(nil, validDeps()); err == nil {
			t.Fatal("expected error for nil runtime")
		}
	})

	t.Run("nil CertHooks is legal", func(t *testing.T) {
		// 空 Runtime 没有证书，newGRPCServer 必然失败；这里断言的是「不 panic」。
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("nil CertHooks must not panic: %v", r)
			}
		}()
		_, _ = NewServer(bareRuntime(), validDeps())
	})

	required := map[string]func(ServiceDeps) ServiceDeps{
		"settingsProvider":        func(d ServiceDeps) ServiceDeps { d.SettingsProvider = nil; return d },
		"alertEvaluator":          func(d ServiceDeps) ServiceDeps { d.AlertEvaluator = nil; return d },
		"publisher":               func(d ServiceDeps) ServiceDeps { d.Publisher = nil; return d },
		"connectionReportHandler": func(d ServiceDeps) ServiceDeps { d.ConnectionReportHandler = nil; return d },
		"settingsPusher":          func(d ServiceDeps) ServiceDeps { d.SettingsPusher = nil; return d },
	}
	for name, drop := range required {
		t.Run("missing "+name+" panics", func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic when %s is nil", name)
				}
			}()
			_, _ = NewServer(bareRuntime(), drop(validDeps()))
		})
	}
}

func TestServer_HeartbeatThrottling_DebouncesDBUpdate(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-throttle", fingerprint)

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)

	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-a",
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: &fakeConnectionRuntimeHandler{}},
	}
	server.SetHeartbeatFlushIntervalForTest(200 * time.Millisecond)

	// Step 1: Register agent -> DB is updated with initial heartbeat timestamp.
	stream := &fakeSyncServer{ctx: ctx}
	conn, err := server.acceptAgent(stream, &pb.RegisterRequest{NodeId: "agent-throttle"}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("acceptAgent failed: %v", err)
	}

	node, err := repos.Node.GetByNodeID(context.Background(), "agent-throttle")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	t0 := node.LastHeartbeatAt
	if t0.IsZero() {
		t.Fatal("node last_heartbeat_at must be populated on register")
	}

	// Step 2: Immediate heartbeat (within 200ms) -> in-memory touch, but DB NOT updated.
	server.handleHeartbeat(conn, &pb.HeartbeatRequest{})

	nodeAfterBeat1, err := repos.Node.GetByNodeID(context.Background(), "agent-throttle")
	if err != nil {
		t.Fatalf("get node after beat 1: %v", err)
	}
	if !nodeAfterBeat1.LastHeartbeatAt.Equal(t0) {
		t.Fatalf("heartbeat within debounce window must not update DB: got %v, want %v", nodeAfterBeat1.LastHeartbeatAt, t0)
	}

	// Step 3: Wait past the 200ms threshold -> next heartbeat flushes to DB.
	time.Sleep(260 * time.Millisecond)
	server.handleHeartbeat(conn, &pb.HeartbeatRequest{})

	nodeAfterBeat2, err := repos.Node.GetByNodeID(context.Background(), "agent-throttle")
	if err != nil {
		t.Fatalf("get node after beat 2: %v", err)
	}
	if !nodeAfterBeat2.LastHeartbeatAt.After(t0) {
		t.Fatalf("heartbeat after threshold must update DB: got %v, want after %v", nodeAfterBeat2.LastHeartbeatAt, t0)
	}
	t2 := nodeAfterBeat2.LastHeartbeatAt

	// Step 4: Another immediate heartbeat -> throttled again.
	server.handleHeartbeat(conn, &pb.HeartbeatRequest{})
	nodeAfterBeat3, err := repos.Node.GetByNodeID(context.Background(), "agent-throttle")
	if err != nil {
		t.Fatalf("get node after beat 3: %v", err)
	}
	if !nodeAfterBeat3.LastHeartbeatAt.Equal(t2) {
		t.Fatalf("immediate heartbeat must be throttled again: got %v, want %v", nodeAfterBeat3.LastHeartbeatAt, t2)
	}
}

func TestServer_HeartbeatThrottling_MaintenanceImmunity(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-maint", fingerprint)

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)

	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-a",
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: &fakeConnectionRuntimeHandler{}},
	}
	server.SetHeartbeatFlushIntervalForTest(10 * time.Millisecond)

	stream := &fakeSyncServer{ctx: ctx}
	conn, err := server.acceptAgent(stream, &pb.RegisterRequest{NodeId: "agent-maint"}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("acceptAgent failed: %v", err)
	}

	// Set node to maintenance in DB.
	maintStatus := model.NodeStatusMaintenance
	if err := repos.Node.UpdateAdminFields(context.Background(), "agent-maint", db.NodeAdminPatch{Status: &maintStatus}); err != nil {
		t.Fatalf("set maintenance: %v", err)
	}

	// Send heartbeat after threshold.
	time.Sleep(20 * time.Millisecond)
	server.handleHeartbeat(conn, &pb.HeartbeatRequest{})

	// Node status must remain maintenance!
	node, err := repos.Node.GetByNodeID(context.Background(), "agent-maint")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.Status != model.NodeStatusMaintenance {
		t.Fatalf("node status = %v, want maintenance", node.Status)
	}
}

func TestServer_HeartbeatThrottling_ShutdownFlushesConnected(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-shutdown", fingerprint)

	registry := agentregistry.New(slog.Default())
	server := &Server{
		Runtime: &Runtime{
			ctx:      context.Background(),
			cancel:   func() {},
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-a",
			logger:   slog.Default(),
		},
		deps: ServiceDeps{ConnectionReportHandler: &fakeConnectionRuntimeHandler{}},
	}
	// Long flush interval so heartbeats wouldn't normally flush.
	server.SetHeartbeatFlushIntervalForTest(10 * time.Minute)

	stream := &fakeSyncServer{ctx: ctx}
	_, err := server.acceptAgent(stream, &pb.RegisterRequest{NodeId: "agent-shutdown"}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("acceptAgent failed: %v", err)
	}

	node, err := repos.Node.GetByNodeID(context.Background(), "agent-shutdown")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	t0 := node.LastHeartbeatAt

	time.Sleep(20 * time.Millisecond)

	// Shutdown flushes active connections.
	server.flushConnectedHeartbeats()

	nodeAfterShutdown, err := repos.Node.GetByNodeID(context.Background(), "agent-shutdown")
	if err != nil {
		t.Fatalf("get node after shutdown: %v", err)
	}
	if !nodeAfterShutdown.LastHeartbeatAt.After(t0) {
		t.Fatalf("shutdown flush must update last_heartbeat_at: got %v, want after %v", nodeAfterShutdown.LastHeartbeatAt, t0)
	}
}
