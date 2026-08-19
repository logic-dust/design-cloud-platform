package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

func TestDispatchAsyncCreatesRecordAndMarksDispatched(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	createNode(t, repos.Node, "agent-a", model.NodeStatusOnline)
	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)
	stream := &fakeSyncServer{}
	registry.Register("agent-a", stream, nil, "test-peer")

	service := newDispatcher(registry, fakeLeaderState{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)
	req := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithTargetNode("agent-a"),
	)
	req.TraceID = "trace-1"
	req.Mode = execution.RunModeAsyncTracked

	resp, err := service.DispatchToAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch async: %v", err)
	}
	if !resp.Accepted || !resp.Success {
		t.Fatalf("response accepted/success = %v/%v, want true/true", resp.Accepted, resp.Success)
	}
	if got := len(stream.sent); got != 1 {
		t.Fatalf("sent commands = %d, want 1", got)
	}

	record, err := repos.Execution.GetByExecutionID(context.Background(), "request-1")
	if err != nil {
		t.Fatalf("get execution record: %v", err)
	}
	if record.Status != model.ExecutionStatusDispatched {
		t.Fatalf("record status = %d, want dispatched", record.Status)
	}
}

func TestDispatchAsyncDuplicateExecutionIDReturnsInFlight(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	createNode(t, repos.Node, "agent-a", model.NodeStatusOnline)
	registry := newAgentRegistry(slog.Default())
	t.Cleanup(registry.Stop)
	registry.Register("agent-a", &fakeSyncServer{}, nil, "test-peer")
	service := newDispatcher(registry, fakeLeaderState{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)
	req := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithTargetNode("agent-a"),
	)
	req.TraceID = "trace-1"
	req.Mode = execution.RunModeAsyncTracked
	if _, err := service.DispatchToAgent(context.Background(), req); err != nil {
		t.Fatalf("initial dispatch: %v", err)
	}

	duplicate := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithTargetNode("agent-a"),
	)
	duplicate.TraceID = "trace-2"
	duplicate.Mode = execution.RunModeAsyncTracked
	_, err := service.DispatchToAgent(context.Background(), duplicate)
	if err == nil {
		t.Fatalf("expected duplicate in-flight error")
	}
	if !errors.Is(err, errInFlight) {
		t.Fatalf("duplicate error = %v, want errInFlight", err)
	}
}

func TestLookupActionResultReturnsTerminalRecords(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	service := newDispatcher(newAgentRegistry(slog.Default()), fakeLeaderState{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)

	cases := []struct {
		name        string
		status      model.ExecutionStatus
		success     bool
		systemError string
	}{
		{name: "succeeded", status: model.ExecutionStatusSucceeded, success: true},
		{name: "failed", status: model.ExecutionStatusFailed, success: false, systemError: "failed"},
		{name: "timed_out", status: model.ExecutionStatusTimedOut, success: false, systemError: "timeout"},
		{name: "cancelled", status: model.ExecutionStatusCancelled, success: false, systemError: "cancelled"},
	}
	for i, tc := range cases {
		executionID := fmt.Sprintf("execution-%d", i)
		traceID := fmt.Sprintf("trace-%d", i)
		if err := repos.Execution.Create(context.Background(), executionID, string(execution.ActionUserList), traceID, "server-a", "agent-a"); err != nil {
			t.Fatalf("%s create: %v", tc.name, err)
		}
		if _, err := repos.Execution.MarkTerminal(context.Background(), traceID, tc.status, tc.success, 0, tc.systemError); err != nil {
			t.Fatalf("%s terminal: %v", tc.name, err)
		}

		resp, err := service.lookupActionResult(context.Background(), executionID, false)
		if err != nil {
			t.Fatalf("%s lookup: %v", tc.name, err)
		}
		if resp.TraceID != traceID || resp.Success != tc.success || resp.SystemError != tc.systemError {
			t.Fatalf("%s response = %+v", tc.name, resp)
		}
	}
}

func TestStandbyForwardFailureRecoversTerminalRecord(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	if err := repos.Execution.Create(context.Background(), "request-1", string(execution.ActionUserList), "trace-1", "server-a", "agent-a"); err != nil {
		t.Fatalf("create execution record: %v", err)
	}
	if _, err := repos.Execution.MarkTerminal(context.Background(), "trace-1", model.ExecutionStatusSucceeded, true, 0, ""); err != nil {
		t.Fatalf("finish execution record: %v", err)
	}

	service := newDispatcher(
		newAgentRegistry(slog.Default()),
		fakeLeaderState{isLeader: false, leaderID: "leader-a"},
		slog.Default(),
		newForwardClient(1, Certs{}, "", "server-b", slog.Default()),
		"server-b",
		repos.Node,
		repos.Execution,
	)
	req := execution.NewExecuteRequest(execution.ActionUserList, "request-1")
	req.TraceID = "trace-retry"
	req.Mode = execution.RunModeAsyncTracked

	resp, err := service.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("execute should recover from DB: %v", err)
	}
	if resp.ExecutionID != "request-1" || resp.TraceID != "trace-1" || !resp.Success {
		t.Fatalf("recovered response = %+v", resp)
	}
}
