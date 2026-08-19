package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

type fakeSyncClient struct {
	grpc.ClientStream
	sent    []*pb.AgentMessage
	sendErr error
}

func (s *fakeSyncClient) Send(msg *pb.AgentMessage) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, msg)
	return nil
}

func (s *fakeSyncClient) Recv() (*pb.ServerCommand, error) {
	return nil, io.EOF
}

func TestSendCommandResultQueuesUntilAck(t *testing.T) {
	t.Parallel()

	agent := NewAgent(nil, Config{NodeID: "node-a"}, slog.Default(), NewSettingsManager())
	stream := &fakeSyncClient{}

	agent.sendCommandResult(stream, &pb.CommandResult{TraceId: "trace-1", Success: true})

	if got := len(stream.sent); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
	if got := len(agent.pendingResults); got != 1 {
		t.Fatalf("pending results = %d, want 1", got)
	}

	agent.handleResultAck(&pb.CommandResultAck{TraceId: "trace-1", Persisted: true})

	if got := len(agent.pendingResults); got != 0 {
		t.Fatalf("pending results after ack = %d, want 0", got)
	}
}

func TestSendCommandResultKeepsResultWhenSendFails(t *testing.T) {
	t.Parallel()

	agent := NewAgent(nil, Config{NodeID: "node-a"}, slog.Default(), NewSettingsManager())
	stream := &fakeSyncClient{sendErr: fmt.Errorf("stream closed")}

	agent.sendCommandResult(stream, &pb.CommandResult{TraceId: "trace-1", Success: true})

	if got := len(stream.sent); got != 0 {
		t.Fatalf("sent messages = %d, want 0", got)
	}
	if got := len(agent.pendingResults); got != 1 {
		t.Fatalf("pending results = %d, want 1", got)
	}
}

func TestReplayPendingResultsResendsQueuedResults(t *testing.T) {
	t.Parallel()

	agent := NewAgent(nil, Config{NodeID: "node-a"}, slog.Default(), NewSettingsManager())
	agent.storePendingResult(&pb.CommandResult{TraceId: "trace-1", Success: true})
	agent.storePendingResult(&pb.CommandResult{TraceId: "trace-2", Success: false})
	stream := &fakeSyncClient{}

	agent.replayPendingResults(stream)

	if got := len(stream.sent); got != 2 {
		t.Fatalf("replayed messages = %d, want 2", got)
	}
	for _, msg := range stream.sent {
		if msg.GetCommandResult() == nil {
			t.Fatalf("replayed non-result message: %T", msg.GetPayload())
		}
	}
	if got := len(agent.pendingResults); got != 2 {
		t.Fatalf("pending results after replay = %d, want 2", got)
	}

	agent.handleResultAck(&pb.CommandResultAck{TraceId: "trace-1"})
	if got := len(agent.pendingResults); got != 1 {
		t.Fatalf("pending results after first ack = %d, want 1", got)
	}
}

func TestReplayPendingResultsPrunesExpiredResults(t *testing.T) {
	t.Parallel()

	agent := NewAgent(nil, Config{NodeID: "node-a"}, slog.Default(), NewSettingsManager())
	now := time.Now()
	agent.pendingResults = map[string]pendingCommandResult{
		"expired": {
			result:   &pb.CommandResult{TraceId: "expired", Success: true},
			storedAt: now.Add(-pendingCommandResultTTL - time.Second),
		},
		"fresh": {
			result:   &pb.CommandResult{TraceId: "fresh", Success: true},
			storedAt: now,
		},
	}
	stream := &fakeSyncClient{}

	agent.replayPendingResults(stream)

	if got := len(stream.sent); got != 1 {
		t.Fatalf("replayed messages = %d, want 1", got)
	}
	if got := stream.sent[0].GetCommandResult().TraceId; got != "fresh" {
		t.Fatalf("replayed trace = %q, want fresh", got)
	}
	if _, ok := agent.pendingResults["expired"]; ok {
		t.Fatalf("expired result remained after replay")
	}
	if got := len(agent.pendingResults); got != 1 {
		t.Fatalf("pending results = %d, want 1", got)
	}
}

func TestStorePendingResultDropsOldestWhenCapReached(t *testing.T) {
	t.Parallel()

	agent := NewAgent(nil, Config{NodeID: "node-a"}, slog.Default(), NewSettingsManager())
	base := time.Now().Add(-time.Minute)
	agent.pendingResults = make(map[string]pendingCommandResult, maxPendingCommandResults)
	for i := range maxPendingCommandResults {
		traceID := fmt.Sprintf("trace-%04d", i)
		agent.pendingResults[traceID] = pendingCommandResult{
			result:   &pb.CommandResult{TraceId: traceID, Success: true},
			storedAt: base.Add(time.Duration(i) * time.Millisecond),
		}
	}

	agent.storePendingResult(&pb.CommandResult{TraceId: "trace-new", Success: true})

	if got := len(agent.pendingResults); got != maxPendingCommandResults {
		t.Fatalf("pending results = %d, want %d", got, maxPendingCommandResults)
	}
	if _, ok := agent.pendingResults["trace-new"]; !ok {
		t.Fatalf("new pending result was not stored")
	}
	if _, ok := agent.pendingResults["trace-0000"]; ok {
		t.Fatalf("oldest pending result remained after cap pruning")
	}
	if _, ok := agent.pendingResults["trace-0001"]; !ok {
		t.Fatalf("newer pending result was unexpectedly removed")
	}
}

type mockExecutor struct {
	resp *execution.ExecuteResponse
	err  error
}

func (m *mockExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return m.resp, m.err
}

func TestHandleCommandRequest_ResultMapping(t *testing.T) {
	t.Parallel()

	mock := &mockExecutor{
		resp: &execution.ExecuteResponse{
			TraceID:     "trace-exec",
			Success:     false,
			ExitCode:    0,
			Stdout:      "out",
			Stderr:      "err",
			SystemError: "seccomp violation",
		},
	}
	execSvc := newTestExecutorService(mock)
	agent := NewAgent(execSvc, Config{NodeID: "node-a"}, slog.Default(), NewSettingsManager())
	stream := &fakeSyncClient{}

	cmd := &pb.CommandRequest{
		TraceId:     "trace-exec",
		ExecutionId: "exec-1",
		ActionName:  string(execution.ActionLicenseStatus), // 注册于所有平台的 Direct 动作
		PayloadJson: "{}",
		Timeout:     durationpb.New(30 * time.Second),
	}

	agent.handleCommandRequest(stream, cmd)
	agent.executeWg.Wait()

	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(stream.sent))
	}
	res := stream.sent[0].GetCommandResult()
	if res == nil {
		t.Fatalf("expected CommandResult message")
	}
	if res.Success {
		t.Errorf("expected Success to be false, got true")
	}
	if res.ExitCode != 0 {
		t.Errorf("expected ExitCode 0, got %d", res.ExitCode)
	}
	if res.SystemError != "seccomp violation" {
		t.Errorf("expected SystemError %q, got %q", "seccomp violation", res.SystemError)
	}
}
