package control

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"
)

func TestSyncSendsRegisterAckThenHeartbeatAck(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx, fingerprint := peerContextWithCertificate(t)
	createRegisteredNode(t, repos.Node, "agent-a", fingerprint)
	handler := &fakeConnectionSyncHandler{}
	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		registry:    registry,
		nodes:       repos.Node,
		connHandler: handler,
		nodeID:      "server-a",
		logger:      slog.Default(),
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
	handler := &fakeConnectionSyncHandler{}
	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		registry:    registry,
		nodes:       repos.Node,
		connHandler: handler,
		nodeID:      "server-a",
		logger:      slog.Default(),
	}
	stream := &fakeSyncServer{
		ctx: ctx,
		incoming: []*pb.AgentMessage{
			{Payload: &pb.AgentMessage_RegisterRequest{RegisterRequest: &pb.RegisterRequest{
				NodeId: "agent-a",
			}}},
			{Payload: &pb.AgentMessage_ConnectionReport{ConnectionReport: &pb.ConnectionReport{
				ReportedAt: time.Now().Unix(),
				Connections: []*pb.ConnectionInfo{{
					ConnectionId: 10,
					Name:         "desktop",
					Username:     "alice",
					Port:         5901,
					Pid:          123,
					Protocol:     string(contract.VNCProtocol),
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
	if got := len(handler.connections); got != 1 {
		t.Fatalf("connections = %d, want 1", got)
	}
	if handler.connections[0].ConnectionID != 10 || handler.connections[0].PID != 123 {
		t.Fatalf("connection = %+v, want connection id 10 and pid 123", handler.connections[0])
	}
}

func TestTriggerVNCCollectSendsCollectOnly(t *testing.T) {
	t.Parallel()

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		registry:    registry,
		connHandler: &fakeConnectionSyncHandler{},
		logger:      slog.Default(),
	}
	stream := &fakeSyncServer{}
	registry.Register("agent-a", stream, nil, "test-peer")

	if err := server.TriggerCollect(context.Background(), contract.CollectTargetVNC); err != nil {
		t.Fatalf("trigger collect: %v", err)
	}

	if got := len(stream.sent); got != 1 {
		t.Fatalf("sent commands = %d, want collect only", got)
	}
	cmd := stream.sent[0].GetCollectProbeCommand()
	if cmd == nil {
		t.Fatalf("command = %T, want collect probe", stream.sent[0].GetPayload())
	}
	if cmd.GetProbeName() != "vnc-probe" {
		t.Fatalf("probe_name = %q, want vnc-probe", cmd.GetProbeName())
	}
	if cmd.GetTraceId() == "" {
		t.Fatalf("trace id is empty")
	}
}

func TestTriggerVNCCollectNoAgentsIsNoOp(t *testing.T) {
	t.Parallel()

	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)
	server := &Server{
		registry: registry,
		logger:   slog.Default(),
	}

	// No connected agents is a normal operational state (all nodes offline).
	// The collect should succeed as a no-op rather than returning an error.
	if err := server.TriggerCollect(context.Background(), contract.CollectTargetVNC); err != nil {
		t.Fatalf("trigger collect with no agents should be no-op, got error: %v", err)
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

	registry := newAgentRegistry(slog.Default())
	pending := registry.CreatePendingRequest("trace-1", "agent-a")
	stream := &fakeSyncServer{}
	server := &Server{
		registry:   registry,
		ctx:        context.Background(),
		logger:     slog.Default(),
		executions: repos.Execution,
	}

	server.handleCommandResult(&AgentConnection{NodeID: "agent-a", Stream: stream}, &pb.CommandResult{TraceId: "trace-1",
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
	registry := newAgentRegistry(slog.Default())
	pending := registry.CreatePendingRequest("trace-sync", "agent-a")
	stream := &fakeSyncServer{}
	server := &Server{
		registry:   registry,
		ctx:        context.Background(),
		logger:     slog.Default(),
		executions: repos.Execution,
	}

	server.handleCommandResult(&AgentConnection{NodeID: "agent-a", Stream: stream}, &pb.CommandResult{TraceId: "trace-sync",
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
	registry := newAgentRegistry(slog.Default())
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
		registry:   registry,
		ctx:        context.Background(),
		logger:     slog.Default(),
		executions: repos.Execution,
	}

	server.handleCommandResult(&AgentConnection{NodeID: "agent-a", Stream: stream}, &pb.CommandResult{TraceId: "trace-sync",
		Success:  true,
		ExitCode: 0})

	if !deliveredBeforeAck {
		t.Fatalf("result was not delivered before ACK")
	}
}
