package db

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
)

func newNodeRepoForTest(t *testing.T) (*Store, *NodeRepo) {
	t.Helper()

	store, err := NewStore(config.Database{
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
	return store, NewRepos(store).Node
}

func TestNodeCreateAndGetByNodeID(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID:   "test-node-1",
		IsServer: true,
		Status:   model.NodeStatusOffline,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "test-node-1")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.NodeID != "test-node-1" {
		t.Fatalf("NodeID = %q, want %q", got.NodeID, "test-node-1")
	}
	if got.Status != model.NodeStatusOffline {
		t.Fatalf("Status = %d, want %d", got.Status, model.NodeStatusOffline)
	}
}

func TestNodeGetByNodeIDNotFound(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	_, err := repo.GetByNodeID(context.Background(), "nonexistent")
	if !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestNodeTouchHeartbeat(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID: "heartbeat-node",
		Status: model.NodeStatusOffline,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.TouchHeartbeat(ctx, "heartbeat-node", "server-1"); err != nil {
		t.Fatalf("TouchHeartbeat: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "heartbeat-node")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.Status != model.NodeStatusOnline {
		t.Fatalf("Status after heartbeat = %d, want %d", got.Status, model.NodeStatusOnline)
	}
	if got.LastSeenServer != "server-1" {
		t.Fatalf("LastSeenServer = %q, want %q", got.LastSeenServer, "server-1")
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Fatal("LastHeartbeatAt is zero")
	}
}

func TestNodeTouchHeartbeatDoesNotUpdateMaintenance(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID: "maintenance-node",
		Status: model.NodeStatusMaintenance,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.TouchHeartbeat(ctx, "maintenance-node", "server-1"); err != nil {
		t.Fatalf("TouchHeartbeat on maintenance node: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "maintenance-node")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.Status != model.NodeStatusMaintenance {
		t.Fatalf("Status = %d, want %d (should not change maintenance)", got.Status, model.NodeStatusMaintenance)
	}
}

func TestNodeMarkOnlineAndDisconnected(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID: "toggle-node",
		Status: model.NodeStatusOffline,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.MarkOnline(ctx, "toggle-node", "server-1"); err != nil {
		t.Fatalf("MarkOnline: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "toggle-node")
	if err != nil {
		t.Fatalf("GetByNodeID after online: %v", err)
	}
	if got.Status != model.NodeStatusOnline {
		t.Fatalf("Status = %d, want %d", got.Status, model.NodeStatusOnline)
	}
	if got.ConnectedAt.IsZero() {
		t.Fatal("ConnectedAt is zero after MarkOnline")
	}

	if err := repo.MarkDisconnected(ctx, "toggle-node", "server-1"); err != nil {
		t.Fatalf("MarkDisconnected: %v", err)
	}

	got, err = repo.GetByNodeID(ctx, "toggle-node")
	if err != nil {
		t.Fatalf("GetByNodeID after disconnect: %v", err)
	}
	if got.Status != model.NodeStatusOffline {
		t.Fatalf("Status after disconnect = %d, want %d", got.Status, model.NodeStatusOffline)
	}
	if got.DisconnectedAt.IsZero() {
		t.Fatal("DisconnectedAt is zero after MarkDisconnected")
	}
}

func TestNodeMarkStaleOffline(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	onlineNode := &model.Node{
		NodeID: "stale-node",
		Status: model.NodeStatusOnline,
	}
	if err := repo.Create(ctx, onlineNode); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually set last_heartbeat_at to a time in the past
	cutoff := time.Now().Add(-5 * time.Minute)

	// Mark all nodes online as stale
	staled, err := repo.MarkStaleOffline(ctx, cutoff)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if len(staled) != 1 || staled[0] != "stale-node" {
		t.Fatalf("expected staledNodes [stale-node], got %v", staled)
	}
}

func TestNodeUpdateSettingsAndOmit(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID: "settings-node",
		Status: model.NodeStatusOnline,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 1. UpdateSettings atomically sets settings
	settingsData := map[string]any{
		"agent": map[string]any{
			"launcherPath": "/custom/launcher",
		},
	}
	if err := repo.UpdateSettings(ctx, "settings-node", settingsData); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "settings-node")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.Settings == nil {
		t.Fatalf("expected settings to be persisted, got nil")
	}
	agentMap, ok := got.Settings["agent"].(map[string]any)
	if !ok || agentMap["launcherPath"] != "/custom/launcher" {
		t.Fatalf("unexpected settings payload: %v", got.Settings)
	}

	// 2. UpdateAllFields with older memory state (empty Settings) must NOT overwrite DB settings (Omit("Settings"))
	olderNode := got
	olderNode.Settings = nil
	olderNode.ClientHost = "192.168.1.100"
	if err := repo.UpdateAllFields(ctx, &olderNode); err != nil {
		t.Fatalf("UpdateAllFields: %v", err)
	}

	gotAfterAllFields, err := repo.GetByNodeID(ctx, "settings-node")
	if err != nil {
		t.Fatalf("GetByNodeID after UpdateAllFields: %v", err)
	}
	if gotAfterAllFields.ClientHost != "192.168.1.100" {
		t.Fatalf("expected ClientHost updated to 192.168.1.100, got %q", gotAfterAllFields.ClientHost)
	}
	if gotAfterAllFields.Settings == nil {
		t.Fatalf("Settings was unexpectedly overwritten by UpdateAllFields! Omit('Settings') failed")
	}
	agentMap2, ok := gotAfterAllFields.Settings["agent"].(map[string]any)
	if !ok || agentMap2["launcherPath"] != "/custom/launcher" {
		t.Fatalf("Settings corrupted after UpdateAllFields: %v", gotAfterAllFields.Settings)
	}
}

func TestNodeListByFilter(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	nodes := []*model.Node{
		{
			NodeID: "gpu-01",
			Status: model.NodeStatusOnline,
			OS:     "linux",
			Labels: map[string]string{"pool": "gpu", "zone": "east"},
		},
		{
			NodeID: "gpu-02",
			Status: model.NodeStatusOffline,
			OS:     "linux",
			Labels: map[string]string{"pool": "gpu", "zone": "west"},
		},
		{
			NodeID: "win-01",
			Status: model.NodeStatusOnline,
			OS:     "windows",
			Labels: map[string]string{"pool": "cpu", "zone": "east"},
		},
	}
	for _, n := range nodes {
		if err := repo.Create(ctx, n); err != nil {
			t.Fatalf("Create node %s: %v", n.NodeID, err)
		}
	}

	// 1. Filter by NodeIDs
	res, err := repo.ListByFilter(ctx, NodeFilter{NodeIDs: []string{"gpu-01", "win-01"}})
	if err != nil || len(res) != 2 {
		t.Fatalf("expected 2 nodes by ID, got %d (err=%v)", len(res), err)
	}

	// 2. Filter by OS and Status
	res, err = repo.ListByFilter(ctx, NodeFilter{OS: []string{"linux"}, Status: []model.NodeStatus{model.NodeStatusOnline}})
	if err != nil || len(res) != 1 || res[0].NodeID != "gpu-01" {
		t.Fatalf("expected gpu-01, got %v (err=%v)", res, err)
	}

	// 3. Filter by Labels
	res, err = repo.ListByFilter(ctx, NodeFilter{Labels: map[string]string{"pool": "gpu"}})
	if err != nil || len(res) != 2 {
		t.Fatalf("expected 2 gpu nodes, got %d (err=%v)", len(res), err)
	}

	// 4. Filter by multiple labels
	res, err = repo.ListByFilter(ctx, NodeFilter{Labels: map[string]string{"pool": "gpu", "zone": "west"}})
	if err != nil || len(res) != 1 || res[0].NodeID != "gpu-02" {
		t.Fatalf("expected gpu-02, got %v (err=%v)", res, err)
	}

	// 5. No match
	res, err = repo.ListByFilter(ctx, NodeFilter{NodeIDs: []string{"nonexistent"}})
	if err != nil || len(res) != 0 {
		t.Fatalf("expected 0 nodes, got %d (err=%v)", len(res), err)
	}
}
