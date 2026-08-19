package connection

import (
	"context"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestUpsertFixed_CreateNewRecords_AutoResolvesHostFromNode(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "192.168.1.10", Status: model.NodeStatusOnline})

	// Omit Host — engine auto-resolves from node01.ClientHost
	forms := []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 5901, Display: ":1"},
		{Username: "bob", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 5902, Display: ":2"},
	}

	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Created != 2 {
		t.Errorf("expected created=2, got %d", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("expected updated=0, got %d", result.Updated)
	}
	if len(result.Failed) != 0 {
		t.Errorf("expected no failures, got: %v", result.Failed)
	}

	// Verify host was populated with node's client host
	aliceConns, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil || len(aliceConns) != 1 {
		t.Fatalf("expected 1 conn for alice, got %d, err: %v", len(aliceConns), err)
	}
	if aliceConns[0].Host != "192.168.1.10" {
		t.Errorf("expected host to be auto-resolved to 192.168.1.10, got %s", aliceConns[0].Host)
	}
}

func TestUpsertFixed_TargetNodeNotFound_FailsRecord(t *testing.T) {
	svc, _ := newTestConnectionService(t)
	ctx := context.Background()

	forms := []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "non-existent-node", Protocol: contract.VNCProtocol, Port: 5901},
	}

	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Created != 0 {
		t.Errorf("expected created=0, got %d", result.Created)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(result.Failed))
	}
	if result.Failed[0].TargetNodeID != "non-existent-node" {
		t.Errorf("expected failure on non-existent-node, got: %s", result.Failed[0].TargetNodeID)
	}
}

func TestUpsertFixed_MissingUsername_FailsRecord(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "192.168.1.10", Status: model.NodeStatusOnline})

	forms := []CreateFixedConnectionForm{
		{Username: "", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 5901},
	}

	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Created != 0 {
		t.Errorf("expected created=0, got %d", result.Created)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(result.Failed))
	}
}

func TestUpsertFixed_UpdateExistingByPort(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "10.0.0.1", Status: model.NodeStatusOnline})

	// Create initial record
	initial := model.Connection{
		Name: "vnc-alice-node01-:1", Username: "alice", TargetNodeID: "node01",
		Host: "10.0.0.1", Protocol: contract.VNCProtocol, Port: 5901, Display: ":1",
		Source: model.ConnectionSourceFixed, Status: model.ConnectionUnknown,
	}
	if err := repos.Connection.Create(ctx, &initial); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Upsert with updated display & startOptions
	forms := []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 5901, Display: ":1", StartOptions: "-geometry 1920x1080"},
	}
	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Created != 0 || result.Updated != 1 {
		t.Errorf("expected created=0 updated=1, got created=%d updated=%d", result.Created, result.Updated)
	}

	updated, err := repos.Connection.GetByID(ctx, initial.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Extra.StartOptions != "-geometry 1920x1080" {
		t.Errorf("expected startOptions=-geometry 1920x1080, got %s", updated.Extra.StartOptions)
	}
}

func TestUpsertFixed_UpdateExistingByDisplay(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "10.0.0.1", Status: model.NodeStatusOnline})

	// Initial creation via UpsertFixed with only Display (port omitted)
	r1, err := svc.UpsertFixed(ctx, []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Display: ":2"},
	})
	if err != nil || r1.Created != 1 {
		t.Fatalf("initial upsert failed: %v, created=%d", err, r1.Created)
	}

	// Update record using Display (port omitted)
	forms := []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Display: ":2", Alias: "Alice Display 2"},
	}
	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("expected updated=1, got %d", result.Updated)
	}

	conns, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil || len(conns) != 1 {
		t.Fatalf("get conns: %v, count=%d", err, len(conns))
	}
	if conns[0].Alias != "Alice Display 2" {
		t.Errorf("expected alias='Alice Display 2', got %s", conns[0].Alias)
	}
	if conns[0].Port != 5902 {
		t.Errorf("expected auto-computed port=5902, got %d", conns[0].Port)
	}
}

func TestUpsertFixed_NodeLevelFixed(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "10.0.0.1", Status: model.NodeStatusOnline})

	// port=0, display="" keyed (node-level slot)
	initial := model.Connection{
		Name: "vnc-alice-node01", Username: "alice", TargetNodeID: "node01",
		Protocol: contract.VNCProtocol, Port: 0, Display: "",
		Source: model.ConnectionSourceFixed, Status: model.ConnectionUnknown,
	}
	if err := repos.Connection.Create(ctx, &initial); err != nil {
		t.Fatalf("create: %v", err)
	}

	forms := []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 0, Display: "", Alias: "Alice Default Node Slot"},
	}
	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("expected updated=1, got %d", result.Updated)
	}

	updated, err := repos.Connection.GetByID(ctx, initial.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Alias != "Alice Default Node Slot" {
		t.Errorf("expected alias='Alice Default Node Slot', got %s", updated.Alias)
	}
}

func TestUpsertFixed_PartialFailure(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "10.0.0.1", Status: model.NodeStatusOnline})
	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node02", ClientHost: "10.0.0.2", Status: model.NodeStatusOnline})

	forms := []CreateFixedConnectionForm{
		// valid
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 5901},
		// invalid: missing targetNodeId
		{Username: "bob", TargetNodeID: "", Protocol: contract.VNCProtocol, Port: 5902},
		// valid
		{Username: "carol", TargetNodeID: "node02", Protocol: contract.RDPProtocol, Port: 3389},
	}

	result, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("UpsertFixed error: %v", err)
	}
	if result.Created != 2 {
		t.Errorf("expected created=2, got %d", result.Created)
	}
	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failure, got %d: %v", len(result.Failed), result.Failed)
	} else if result.Failed[0].Index != 1 {
		t.Errorf("expected failure at index 1, got index %d", result.Failed[0].Index)
	}
}

func TestUpsertFixed_Idempotent(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", ClientHost: "10.0.0.1", Status: model.NodeStatusOnline})

	forms := []CreateFixedConnectionForm{
		{Username: "alice", TargetNodeID: "node01", Protocol: contract.VNCProtocol, Port: 5901},
	}

	r1, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("first upsert error: %v", err)
	}
	if r1.Created != 1 || r1.Updated != 0 {
		t.Errorf("first call: want created=1 updated=0, got created=%d updated=%d", r1.Created, r1.Updated)
	}

	r2, err := svc.UpsertFixed(ctx, forms)
	if err != nil {
		t.Fatalf("second upsert error: %v", err)
	}
	if r2.Created != 0 || r2.Updated != 1 {
		t.Errorf("second call: want created=0 updated=1, got created=%d updated=%d", r2.Created, r2.Updated)
	}
}
