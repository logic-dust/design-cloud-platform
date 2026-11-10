package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"
)

func TestDispatchAsyncCreatesRecordAndMarksDispatched(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	createNode(t, repos.Node, "agent-a", model.NodeStatusOnline)
	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	stream := &fakeSyncServer{}
	registry.Register("agent-a", stream, nil, "test-peer")

	service := New(registry, fakeLeader{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)
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
	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	registry.Register("agent-a", &fakeSyncServer{}, nil, "test-peer")
	service := New(registry, fakeLeader{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)

	req := execution.NewExecuteRequest(execution.ActionUserList, "request-1", execution.WithTargetNode("agent-a"))
	req.TraceID = "trace-1"
	req.Mode = execution.RunModeAsyncTracked
	if _, err := service.DispatchToAgent(context.Background(), req); err != nil {
		t.Fatalf("initial dispatch: %v", err)
	}

	duplicate := execution.NewExecuteRequest(execution.ActionUserList, "request-1", execution.WithTargetNode("agent-a"))
	duplicate.TraceID = "trace-2"
	duplicate.Mode = execution.RunModeAsyncTracked
	if _, err := service.DispatchToAgent(context.Background(), duplicate); !errors.Is(err, errInFlight) {
		t.Fatalf("duplicate error = %v, want errInFlight", err)
	}
}

func TestLookupActionResultReturnsTerminalRecords(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	service := New(agentregistry.New(slog.Default()), fakeLeader{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)

	cases := []struct {
		name        string
		status      model.ExecutionStatus
		success     bool
		systemError string
	}{
		{name: "succeeded", status: model.ExecutionStatusSucceeded, success: true},
		{name: "failed", status: model.ExecutionStatusFailed, systemError: "failed"},
		{name: "timed_out", status: model.ExecutionStatusTimedOut, systemError: "timeout"},
		{name: "cancelled", status: model.ExecutionStatusCancelled, systemError: "cancelled"},
	}
	for index, testCase := range cases {
		executionID := fmt.Sprintf("execution-%d", index)
		traceID := fmt.Sprintf("trace-%d", index)
		if err := repos.Execution.Create(context.Background(), executionID, string(execution.ActionUserList), traceID, "server-a", "agent-a"); err != nil {
			t.Fatalf("%s create: %v", testCase.name, err)
		}
		if _, err := repos.Execution.MarkTerminal(context.Background(), traceID, testCase.status, 0, testCase.systemError); err != nil {
			t.Fatalf("%s terminal: %v", testCase.name, err)
		}

		resp, err := service.lookupActionResult(context.Background(), executionID, false)
		if err != nil {
			t.Fatalf("%s lookup: %v", testCase.name, err)
		}
		if resp.TraceID != traceID || resp.Success != testCase.success || resp.SystemError != testCase.systemError {
			t.Fatalf("%s response = %+v", testCase.name, resp)
		}
	}
}

func TestStandbyForwardFailureRecoversTerminalRecord(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	if err := repos.Execution.Create(context.Background(), "request-1", string(execution.ActionUserList), "trace-1", "server-a", "agent-a"); err != nil {
		t.Fatalf("create execution record: %v", err)
	}
	if _, err := repos.Execution.MarkTerminal(context.Background(), "trace-1", model.ExecutionStatusSucceeded, 0, ""); err != nil {
		t.Fatalf("finish execution record: %v", err)
	}

	service := New(
		agentregistry.New(slog.Default()),
		fakeLeader{leaderID: "leader-a"},
		slog.Default(),
		failingForwarder{},
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

func TestSelectLeastLoadedAgentRequiresOnlineConnectionAndGroups(t *testing.T) {
	t.Parallel()

	_, repos := newTestRepos(t)
	ctx := context.Background()
	for _, spec := range []struct {
		id, group string
		status    model.NodeStatus
	}{
		{"agent-ok", "login", model.NodeStatusOnline},
		{"agent-maint", "login", model.NodeStatusMaintenance},
		{"agent-offline", "login", model.NodeStatusOffline},
		{"agent-wrong-group", "viz", model.NodeStatusOnline},
	} {
		if err := repos.Node.Create(ctx, &model.Node{
			NodeID: spec.id,
			Status: spec.status,
			Groups: []string{spec.group},
		}); err != nil {
			t.Fatalf("create %s: %v", spec.id, err)
		}
	}

	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	for _, id := range []string{"agent-ok", "agent-maint", "agent-offline", "agent-wrong-group"} {
		registry.Register(id, &fakeSyncServer{}, nil, "test-peer")
	}

	dispatcher := New(registry, fakeLeader{isLeader: true}, slog.Default(), nil, "server-a", repos.Node, repos.Execution)
	if got := dispatcher.selectLeastLoadedAgent(ctx, []string{"login"}); got != "agent-ok" {
		t.Fatalf("selected %q, want agent-ok", got)
	}
}
