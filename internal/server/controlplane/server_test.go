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

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/controlplane/leader"
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
	createRegisteredNode(t, repos.Node, "agent-active", fingerprint)
	createRegisteredNode(t, repos.Node, "agent-stale", fingerprint)

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
	server.SetHeartbeatFlushIntervalForTest(50 * time.Millisecond)

	// Step 1: Register agents -> DB is updated with initial heartbeat timestamp.
	stream := &fakeSyncServer{ctx: ctx}
	activeConn, err := server.acceptAgent(stream, &pb.RegisterRequest{NodeId: "agent-active"}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("acceptAgent active failed: %v", err)
	}
	staleConn, err := server.acceptAgent(stream, &pb.RegisterRequest{NodeId: "agent-stale"}, "127.0.0.1:12346")
	if err != nil {
		t.Fatalf("acceptAgent stale failed: %v", err)
	}

	activeBefore, err := repos.Node.GetByNodeID(context.Background(), "agent-active")
	if err != nil {
		t.Fatalf("get active node: %v", err)
	}
	t0 := activeBefore.LastHeartbeatAt
	if t0.IsZero() {
		t.Fatal("node last_heartbeat_at must be populated on register")
	}
	staleBefore, err := repos.Node.GetByNodeID(context.Background(), "agent-stale")
	if err != nil {
		t.Fatalf("get stale node: %v", err)
	}
	staleT0 := staleBefore.LastHeartbeatAt

	// Backdate agent-stale past activeWindow (50ms + 60s default heartbeatInterval).
	staleConn.Touch(time.Now().Add(-2 * time.Minute))

	// Step 2: handleHeartbeat updates memory only, not DB directly.
	time.Sleep(15 * time.Millisecond)
	server.handleHeartbeat(activeConn, &pb.HeartbeatRequest{})

	afterBeat, err := repos.Node.GetByNodeID(context.Background(), "agent-active")
	if err != nil {
		t.Fatalf("get active node after beat: %v", err)
	}
	if !afterBeat.LastHeartbeatAt.Equal(t0) {
		t.Fatalf("handleHeartbeat must not write DB directly: got %v, want %v", afterBeat.LastHeartbeatAt, t0)
	}

	// Step 3: Periodic batch flusher renews active node, skips stale node.
	registry.StartWorker(server.startHeartbeatFlusher)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := repos.Node.GetByNodeID(context.Background(), "agent-active")
		if err != nil {
			t.Fatalf("get active node: %v", err)
		}
		if got.LastHeartbeatAt.After(t0) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	activeAfter, err := repos.Node.GetByNodeID(context.Background(), "agent-active")
	if err != nil {
		t.Fatalf("get active node after flush: %v", err)
	}
	if !activeAfter.LastHeartbeatAt.After(t0) {
		t.Fatalf("batch flusher must update active node last_heartbeat_at: got %v, want after %v", activeAfter.LastHeartbeatAt, t0)
	}

	staleAfter, err := repos.Node.GetByNodeID(context.Background(), "agent-stale")
	if err != nil {
		t.Fatalf("get stale node after flush: %v", err)
	}
	if !staleAfter.LastHeartbeatAt.Equal(staleT0) {
		t.Fatalf("batch flusher must not renew stale node: got %v, want %v", staleAfter.LastHeartbeatAt, staleT0)
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
	maint := true
	if err := repos.Node.UpdateAdminFields(context.Background(), "agent-maint", db.NodeAdminPatch{InMaintenance: &maint}); err != nil {
		t.Fatalf("set maintenance: %v", err)
	}

	// Heartbeat + batch flush must keep maintenance true and runtime status as maintenance.
	time.Sleep(20 * time.Millisecond)
	server.handleHeartbeat(conn, &pb.HeartbeatRequest{})
	server.flushConnectedHeartbeats()

	// Node must remain in maintenance (while runtime liveness online stays independent)!
	node, err := repos.Node.GetByNodeID(context.Background(), "agent-maint")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !node.InMaintenance || node.RuntimeStatus() != model.NodeStatusMaintenance {
		t.Fatalf("node InMaintenance=%v RuntimeStatus=%v, want true/maintenance", node.InMaintenance, node.RuntimeStatus())
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

func TestServer_HeartbeatFlushThreshold(t *testing.T) {
	t.Parallel()

	sDefault := &Server{Runtime: &Runtime{clusterCfg: config.Cluster{NodeHeartbeatTimeout: 180}}}
	if got := sDefault.heartbeatFlushThreshold(); got != 45*time.Second {
		t.Fatalf("default threshold = %v, want 45s", got)
	}

	sShort := &Server{Runtime: &Runtime{clusterCfg: config.Cluster{NodeHeartbeatTimeout: 60}}}
	if got := sShort.heartbeatFlushThreshold(); got != 30*time.Second {
		t.Fatalf("short timeout threshold = %v, want 30s", got)
	}
}

func TestInitializeLocalNode_StandbySkipsGlobalReset(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx := context.Background()

	// Pre-populate an online agent connected to the active leader.
	if err := repos.Node.Create(ctx, &model.Node{
		NodeID: "cn01",
		Online: true,
	}); err != nil {
		t.Fatalf("create cn01: %v", err)
	}

	// Active leader holds the control lease in DB.
	acquired, _, err := repos.TaskLease.TryAcquire(ctx, leader.LeaseTaskName, "server-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire leader lease: acquired=%v err=%v", acquired, err)
	}

	standby := &Server{
		Runtime: &Runtime{
			nodes:  repos.Node,
			leases: repos.TaskLease,
			nodeID: "server-b",
			logger: slog.Default(),
		},
	}
	standby.initializeLocalNode(ctx)

	gotCN01, err := repos.Node.GetByNodeID(ctx, "cn01")
	if err != nil {
		t.Fatalf("get cn01: %v", err)
	}
	if !gotCN01.Online || gotCN01.RuntimeStatus() != model.NodeStatusOnline {
		t.Fatalf("cn01 Online=%v RuntimeStatus=%v, want online (standby startup must not reset active leader's nodes)", gotCN01.Online, gotCN01.RuntimeStatus())
	}

	// When the active leader holds no lease (e.g. cold restart by server-a), reset applies.
	if _, err := repos.TaskLease.ReleaseIfOwner(ctx, leader.LeaseTaskName, "server-a"); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	leaderCold := &Server{
		Runtime: &Runtime{
			nodes:  repos.Node,
			leases: repos.TaskLease,
			nodeID: "server-a",
			logger: slog.Default(),
		},
	}
	leaderCold.initializeLocalNode(ctx)

	afterCold, err := repos.Node.GetByNodeID(ctx, "cn01")
	if err != nil {
		t.Fatalf("get cn01 after cold reset: %v", err)
	}
	if afterCold.Online || afterCold.RuntimeStatus() != model.NodeStatusOffline {
		t.Fatalf("cn01 Online=%v RuntimeStatus=%v after cold reset, want offline", afterCold.Online, afterCold.RuntimeStatus())
	}
}

func TestServer_FlushHeartbeats_KeepsPureServerAlive(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx := context.Background()
	if err := repos.Node.Create(ctx, &model.Node{NodeID: "server-only", IsServer: true}); err != nil {
		t.Fatalf("create server-only: %v", err)
	}

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		Runtime: &Runtime{
			registry: registry,
			nodes:    repos.Node,
			nodeID:   "server-only",
			logger:   slog.Default(),
		},
	}

	// Shutdown path (includeSelf=false) must not touch server-only when no agents are connected.
	server.flushConnectedHeartbeats()
	got, err := repos.Node.GetByNodeID(ctx, "server-only")
	if err != nil {
		t.Fatalf("get after flushConnectedHeartbeats: %v", err)
	}
	if !got.LastHeartbeatAt.IsZero() {
		t.Fatalf("expected zero LastHeartbeatAt when includeSelf=false, got %v", got.LastHeartbeatAt)
	}

	// Periodic flusher path (includeSelf=true) keeps pure Server node's heartbeat fresh.
	server.flushHeartbeats(true)
	got, err = repos.Node.GetByNodeID(ctx, "server-only")
	if err != nil {
		t.Fatalf("get after flushHeartbeats(true): %v", err)
	}
	if !got.Online || got.LastHeartbeatAt.IsZero() {
		t.Fatalf("expected server-only online with non-zero LastHeartbeatAt, got online=%v hb=%v", got.Online, got.LastHeartbeatAt)
	}
}
