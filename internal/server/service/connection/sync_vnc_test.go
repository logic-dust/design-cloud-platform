package connection

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
)

// testConnListCache 给手写 Service 字面量的测试补上连接列表缓存。
// 生产路径（NewService）必然设置它，这里必须同样设置：否则生产代码就得为
// "缓存为空"写运行时分支，而这个状态在生产中不存在。
func testConnListCache() *cache.TTLCache[string, []model.Connection] {
	return cache.NewTTLCache[string, []model.Connection](time.Minute)
}

func newTestConnectionService(t *testing.T) (*Service, *db.Repos) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "vnc-sync-test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(store)
	svc := &Service{
		connectionRepo: repos.Connection,
		nodeRepo:       repos.Node,
		logger:         slog.Default(),
		connListCache:  testConnListCache(),
	}
	return svc, repos
}

func TestFormatDefaultConnectionName(t *testing.T) {
	tests := []struct {
		name     string
		protocol contract.Protocol
		username string
		nodeID   string
		display  string
		want     string
	}{
		{
			name:     "vnc with display and node",
			protocol: contract.VNCProtocol,
			username: "levin",
			nodeID:   "node01",
			display:  ":1",
			want:     "vnc-levin-node01:1",
		},
		{
			name:     "vnc with numeric display without colon",
			protocol: contract.VNCProtocol,
			username: "alice",
			nodeID:   "gpu-node",
			display:  "2",
			want:     "vnc-alice-gpu-node:2",
		},
		{
			name:     "rdp without display",
			protocol: contract.RDPProtocol,
			username: "bob",
			nodeID:   "win01",
			display:  "",
			want:     "rdp-bob-win01",
		},
		{
			name:     "shared rdp without user",
			protocol: contract.RDPProtocol,
			username: "",
			nodeID:   "win-share",
			display:  "",
			want:     "rdp-shared-win-share",
		},
		{
			name:     "empty protocol defaults to vnc",
			protocol: "",
			username: "charlie",
			nodeID:   "node02",
			display:  ":5",
			want:     "vnc-charlie-node02:5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDefaultConnectionName(tt.protocol, tt.username, tt.nodeID, tt.display)
			if got != tt.want {
				t.Errorf("FormatDefaultConnectionName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateFixedConnectionForm_NormalizeAutoGeneratesName(t *testing.T) {
	form := CreateFixedConnectionForm{
		Username:     "levin",
		TargetNodeID: "node01",
		Host:         "192.168.1.10",
		Protocol:     contract.VNCProtocol,
		Port:         5901,
		Display:      ":1",
	}

	norm, err := form.normalize()
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if norm.Name != "vnc-levin-node01:1" {
		t.Errorf("expected auto-generated name %q, got %q", "vnc-levin-node01:1", norm.Name)
	}

	// Specified name should be retained
	formWithCustomName := form
	formWithCustomName.Name = "custom-vnc-name"
	normCustom, err := formWithCustomName.normalize()
	if err != nil {
		t.Fatalf("normalize with custom name failed: %v", err)
	}
	if normCustom.Name != "custom-vnc-name" {
		t.Errorf("expected custom name %q, got %q", "custom-vnc-name", normCustom.Name)
	}
}

func TestSyncVNCConnections_MatchesOnNodeUserPortRegardlessOfNameDrift(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", AllowConnections: true, Status: model.NodeStatusOnline})

	// Initial scan: connection reported with desktop name A
	connections1 := []contract.ConnectionInfo{
		{
			Username: "levin",
			Port:     5901,
			Display:  ":1",
			PID:      1234,
			Name:     "TurboVNC: host:1 (levin)",
			Protocol: contract.VNCProtocol,
		},
	}

	if err := svc.SyncVNCConnections(ctx, "node01", connections1); err != nil {
		t.Fatalf("initial SyncVNCConnections failed: %v", err)
	}

	conns, err := repos.Connection.ListByUser(ctx, "levin")
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(conns))
	}
	firstID := conns[0].ID
	if conns[0].Status != model.ConnectionRunning {
		t.Errorf("expected Running status, got %d", conns[0].Status)
	}

	// Subsequent scan: connection has changed desktop name / PID (e.g. restart or title change)
	connections2 := []contract.ConnectionInfo{
		{
			Username: "levin",
			Port:     5901,
			Display:  ":1",
			PID:      5678,
			Name:     "My-New-Simulation-Desktop",
			Protocol: contract.VNCProtocol,
		},
	}

	if err := svc.SyncVNCConnections(ctx, "node01", connections2); err != nil {
		t.Fatalf("second SyncVNCConnections failed: %v", err)
	}

	connsAfter, err := repos.Connection.ListByUser(ctx, "levin")
	if err != nil {
		t.Fatalf("list connections after update: %v", err)
	}
	if len(connsAfter) != 1 {
		t.Fatalf("expected still 1 connection (matched and reconciled), got %d", len(connsAfter))
	}
	if connsAfter[0].ID != firstID {
		t.Errorf("expected connection ID %d to be preserved, got %d", firstID, connsAfter[0].ID)
	}
	if connsAfter[0].PID != 5678 {
		t.Errorf("expected PID to be updated to 5678, got %d", connsAfter[0].PID)
	}
	if connsAfter[0].Status != model.ConnectionRunning {
		t.Errorf("expected Running status, got %d", connsAfter[0].Status)
	}
}

func TestSyncVNCConnections_MultiNodeSameNameNoCollision(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node-01", AllowConnections: true, Status: model.NodeStatusOnline})
	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node-02", AllowConnections: true, Status: model.NodeStatusOnline})

	// Node 1 connection
	node1Connections := []contract.ConnectionInfo{
		{
			Username: "levin",
			Port:     5901,
			Display:  ":1",
			PID:      1001,
			Name:     "default-name",
			Protocol: contract.VNCProtocol,
		},
	}
	if err := svc.SyncVNCConnections(ctx, "node-01", node1Connections); err != nil {
		t.Fatalf("node1 SyncVNCConnections failed: %v", err)
	}

	// Node 2 connection with same username, port, and name
	node2Connections := []contract.ConnectionInfo{
		{
			Username: "levin",
			Port:     5901,
			Display:  ":1",
			PID:      2002,
			Name:     "default-name",
			Protocol: contract.VNCProtocol,
		},
	}
	if err := svc.SyncVNCConnections(ctx, "node-02", node2Connections); err != nil {
		t.Fatalf("node2 SyncVNCConnections failed (should not conflict on name): %v", err)
	}

	conns, err := repos.Connection.ListByUser(ctx, "levin")
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(conns) != 2 {
		t.Fatalf("expected 2 distinct connections across 2 nodes, got %d", len(conns))
	}
}

func TestSyncVNCConnections_GracePeriodPreventsPrematureStop(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	// 1. Create a fixed VNC connection marked Running
	fixedConn := model.Connection{
		Name:         "fixed-node01",
		Username:     "alice",
		Source:       model.ConnectionSourceFixed,
		Protocol:     contract.VNCProtocol,
		TargetNodeID: "node01",
		Display:      ":1",
		Port:         5901,
		Status:       model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, &fixedConn); err != nil {
		t.Fatalf("create fixed connection: %v", err)
	}

	// 2. Mark controlled (e.g. StartConnection just called)
	svc.markControlled(fixedConn.ID)

	// 3. Probe runs and reports no connections on node01
	if err := svc.SyncVNCConnections(ctx, "node01", nil); err != nil {
		t.Fatalf("SyncVNCConnections failed: %v", err)
	}

	// 4. Connection should still be Running because of grace period
	after, err := repos.Connection.GetByID(ctx, fixedConn.ID)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if after.Status != model.ConnectionRunning {
		t.Errorf("expected status to remain Running during grace period, got %s", after.Status)
	}
}
