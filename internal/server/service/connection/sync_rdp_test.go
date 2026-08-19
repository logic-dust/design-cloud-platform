package connection

import (
	"context"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/push"
)

// listRDP fetches the node's single shared RDP record and asserts there is exactly one.
func listRDP(t *testing.T, svc *Service) model.Connection {
	t.Helper()

	conns, err := svc.ListByUser(context.Background(), AllUsers)
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected exactly 1 connection, got %d", len(conns))
	}
	return conns[0]
}

type recordingPublisher struct {
	mu         sync.Mutex
	broadcasts int
	toUsers    [][]string
}

func (p *recordingPublisher) PublishToUser(_ context.Context, userIDs []string, _ push.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := append([]string(nil), userIDs...)
	p.toUsers = append(p.toUsers, cp)
}

func (p *recordingPublisher) PublishToRole(context.Context, model.Role, push.Message) {}

func (p *recordingPublisher) Broadcast(context.Context, push.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.broadcasts++
}

func TestSyncStaticRDPConnection_CreateAndReconcile(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	const nodeID = "win-node-01"
	_ = repos.Node.Create(ctx, &model.Node{NodeID: nodeID, AllowConnections: true, Status: model.NodeStatusOnline})
	peerAddr := "192.168.1.100:54321"

	// 1. Registration: no probe result yet -> record exists but state stays unknown.
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, peerAddr, nil); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	c := listRDP(t, svc)
	if c.TargetNodeID != nodeID {
		t.Errorf("TargetNodeID = %q, want %q", c.TargetNodeID, nodeID)
	}
	if c.Protocol != contract.RDPProtocol {
		t.Errorf("Protocol = %q, want %q", c.Protocol, contract.RDPProtocol)
	}
	if c.Port != DefaultRDPPort {
		t.Errorf("Port = %d, want default %d before any probe", c.Port, DefaultRDPPort)
	}
	if !c.Shared {
		t.Errorf("Shared = false, want true")
	}
	if c.Source != model.ConnectionSourceFixed {
		t.Errorf("Source = %v, want fixed", c.Source)
	}
	if c.Status != model.ConnectionUnknown {
		t.Errorf("Status = %v, want unknown until the agent reports a probe result", c.Status)
	}
	if c.Host != "192.168.1.100" {
		t.Errorf("Host = %q, want 192.168.1.100", c.Host)
	}

	// 2. Heartbeat reports a custom RDP port with RDP disabled -> stopped + port follows probe.
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, peerAddr, &pb.RDPStatus{Available: false, Port: 3390}); err != nil {
		t.Fatalf("sync to stopped failed: %v", err)
	}
	c = listRDP(t, svc)
	if c.Status != model.ConnectionStopped {
		t.Errorf("Status = %v, want stopped", c.Status)
	}
	if c.Port != 3390 {
		t.Errorf("Port = %d, want 3390 (port must follow the probe, not be hardcoded)", c.Port)
	}

	// 3. Heartbeat reports RDP available -> running.
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, peerAddr, &pb.RDPStatus{Available: true, Port: 3390}); err != nil {
		t.Fatalf("sync to running failed: %v", err)
	}
	c = listRDP(t, svc)
	if c.Status != model.ConnectionRunning {
		t.Errorf("Status = %v, want running", c.Status)
	}

	// 4. A later heartbeat without probe data must not fabricate a state change.
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, peerAddr, nil); err != nil {
		t.Fatalf("sync without probe data failed: %v", err)
	}
	c = listRDP(t, svc)
	if c.Status != model.ConnectionRunning {
		t.Errorf("Status = %v, want running kept when no probe data arrives", c.Status)
	}
	if c.Port != 3390 {
		t.Errorf("Port = %d, want 3390 kept when no probe data arrives", c.Port)
	}
}

func TestSyncStaticRDPConnection_BroadcastsAndPreservesAlias(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	pub := &recordingPublisher{}
	svc.publisher = pub
	svc.connListCache = cache.NewTTLCache[string, []model.Connection](time.Minute)
	ctx := context.Background()

	const nodeID = "win-share-01"
	_ = repos.Node.Create(ctx, &model.Node{NodeID: nodeID, AllowConnections: true, Status: model.NodeStatusOnline})
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, "10.0.0.8:1", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if pub.broadcasts < 1 {
		t.Fatalf("broadcasts = %d, want >= 1 on create (owner \"\" not AllUsers)", pub.broadcasts)
	}
	if len(pub.toUsers) != 0 {
		t.Fatalf("PublishToUser called with %v; shared RDP must Broadcast instead", pub.toUsers)
	}

	c := listRDP(t, svc)
	accessed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repos.Connection.UpdateFields(ctx, c.ID, map[string]any{
		"alias":            "CAD Workstation",
		"last_accessed_at": accessed,
	}); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	// Warm cache for a user so we can assert shared writes clear it.
	fetches := 0
	if _, err := svc.connListCache.Get(ctx, "alice", func(context.Context, string) ([]model.Connection, error) {
		fetches++
		return []model.Connection{c}, nil
	}); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("warm fetches = %d, want 1", fetches)
	}

	pub.broadcasts = 0
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, "10.0.0.9:1", &pb.RDPStatus{Available: true, Port: 3391}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if pub.broadcasts < 1 {
		t.Fatalf("broadcasts = %d after status/port update, want >= 1", pub.broadcasts)
	}

	fetches = 0
	if _, err := svc.connListCache.Get(ctx, "alice", func(context.Context, string) ([]model.Connection, error) {
		fetches++
		return nil, nil
	}); err != nil {
		t.Fatalf("post-clear get: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("post-clear fetches = %d, want 1 (cache must have been Cleared)", fetches)
	}

	got := listRDP(t, svc)
	if got.Alias != "CAD Workstation" {
		t.Errorf("Alias = %q, want preserved across heartbeat UpdateFields", got.Alias)
	}
	if !got.LastAccessedAt.Equal(accessed) {
		t.Errorf("LastAccessedAt = %v, want %v preserved", got.LastAccessedAt, accessed)
	}
	if got.Host != "10.0.0.9" {
		t.Errorf("Host = %q, want 10.0.0.9", got.Host)
	}
	if got.Port != 3391 {
		t.Errorf("Port = %d, want 3391", got.Port)
	}
	if got.Status != model.ConnectionRunning {
		t.Errorf("Status = %v, want running", got.Status)
	}
}

func TestSyncStaticRDPConnection_UniqueSharedNodeProtocol(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	const nodeID = "win-uniq-01"
	_ = repos.Node.Create(ctx, &model.Node{NodeID: nodeID, AllowConnections: true, Status: model.NodeStatusOnline})
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, "10.0.0.1:1", &pb.RDPStatus{Available: true, Port: 3389}); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Attempting a second shared RDP row for the same node+protocol at a different port
	// must hit the unique index (or still leave a single logical record via FindSharedStatic).
	dup := &model.Connection{
		Name:         "dup",
		TargetNodeID: nodeID,
		Host:         "10.0.0.1",
		Port:         3399,
		Protocol:     contract.RDPProtocol,
		Shared:       true,
		Source:       model.ConnectionSourceFixed,
		Status:       model.ConnectionRunning,
	}
	createErr := repos.Connection.Create(ctx, dup)
	if createErr == nil {
		t.Log("unique index not enforced by this driver/migration path; asserting service still reconciles to one row")
	}

	// Heartbeat with a new port must update the existing row, not insert another.
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, "10.0.0.1:1", &pb.RDPStatus{Available: true, Port: 3399}); err != nil {
		t.Fatalf("port change sync: %v", err)
	}
	conns, err := svc.ListByUser(ctx, AllUsers)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	shared := 0
	for _, c := range conns {
		if c.TargetNodeID == nodeID && c.Shared && c.Protocol == contract.RDPProtocol {
			shared++
			if c.Port != 3399 {
				t.Errorf("Port = %d, want 3399", c.Port)
			}
		}
	}
	if shared != 1 {
		t.Fatalf("shared RDP rows = %d, want 1 after port change (createErr=%v)", shared, createErr)
	}
}

func TestSyncStaticRDPConnection_SkipsWhenAllowConnectionsFalse(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	const nodeID = "win-blocked-01"
	node := model.Node{
		NodeID:           nodeID,
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
	}
	if err := repos.Node.Create(ctx, &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	created, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	created.AllowConnections = false
	if err := repos.Node.UpdateAllFields(ctx, &created); err != nil {
		t.Fatalf("update node: %v", err)
	}

	// Should not error, but also should not create the connection
	if err := svc.SyncStaticRDPConnection(ctx, nodeID, "10.0.0.1:3389", &pb.RDPStatus{Available: true, Port: 3389}); err != nil {
		t.Fatalf("sync should succeed gracefully, got %v", err)
	}

	_, found, err := repos.Connection.FindSharedStatic(ctx, nodeID, contract.RDPProtocol)
	if err != nil {
		t.Fatalf("find shared static: %v", err)
	}
	if found {
		t.Error("expected shared RDP NOT to be created when AllowConnections=false")
	}
}
