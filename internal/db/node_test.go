package db

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
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
	if !errors.Is(err, contract.ErrNotFound) {
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

	if err := repo.TouchHeartbeat(ctx, "heartbeat-node"); err != nil {
		t.Fatalf("TouchHeartbeat: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "heartbeat-node")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.Status != model.NodeStatusOnline {
		t.Fatalf("Status after heartbeat = %d, want %d", got.Status, model.NodeStatusOnline)
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

	if err := repo.TouchHeartbeat(ctx, "maintenance-node"); err != nil {
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

	if err := repo.MarkOnline(ctx, "toggle-node"); err != nil {
		t.Fatalf("MarkOnline: %v", err)
	}

	got, err := repo.GetByNodeID(ctx, "toggle-node")
	if err != nil {
		t.Fatalf("GetByNodeID after online: %v", err)
	}
	if got.Status != model.NodeStatusOnline {
		t.Fatalf("Status = %d, want %d", got.Status, model.NodeStatusOnline)
	}
	if got.LastConnectedAt.IsZero() {
		t.Fatal("LastConnectedAt is zero after MarkOnline")
	}

	if err := repo.MarkDisconnected(ctx, "toggle-node"); err != nil {
		t.Fatalf("MarkDisconnected: %v", err)
	}

	got, err = repo.GetByNodeID(ctx, "toggle-node")
	if err != nil {
		t.Fatalf("GetByNodeID after disconnect: %v", err)
	}
	if got.Status != model.NodeStatusOffline {
		t.Fatalf("Status after disconnect = %d, want %d", got.Status, model.NodeStatusOffline)
	}
	if got.LastDisconnectedAt.IsZero() {
		t.Fatal("LastDisconnectedAt is zero after MarkDisconnected")
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
	got, err := repo.GetByNodeID(ctx, "stale-node")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.Status != model.NodeStatusOffline {
		t.Fatalf("Status = %d, want offline", got.Status)
	}
}

func TestNodeMarkStaleOfflineSkipsFreshHeartbeat(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Node{
		NodeID: "fresh-node",
		Status: model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.TouchHeartbeat(ctx, "fresh-node"); err != nil {
		t.Fatalf("TouchHeartbeat: %v", err)
	}

	cutoff := time.Now().Add(-time.Minute)
	staled, err := repo.MarkStaleOffline(ctx, cutoff)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if len(staled) != 0 {
		t.Fatalf("expected no stale nodes with fresh heartbeat, got %v", staled)
	}
	got, err := repo.GetByNodeID(ctx, "fresh-node")
	if err != nil {
		t.Fatalf("GetByNodeID: %v", err)
	}
	if got.Status != model.NodeStatusOnline {
		t.Fatalf("Status = %d, want online", got.Status)
	}
}

func TestCountRuntimeByStatusIgnoresNeverConnected(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Node{
		NodeID: "awaiting",
		Status: model.NodeStatusMaintenance,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{
		NodeID:          "enrolled-offline",
		Status:          model.NodeStatusOffline,
		LastConnectedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create enrolled: %v", err)
	}

	online, offline, maintenance, err := repo.CountRuntimeByStatus(ctx)
	if err != nil {
		t.Fatalf("CountRuntimeByStatus: %v", err)
	}
	if maintenance != 0 {
		t.Fatalf("maintenance = %d, want 0 (never connected)", maintenance)
	}
	if offline != 1 {
		t.Fatalf("offline = %d, want 1", offline)
	}
	if online != 0 {
		t.Fatalf("online = %d, want 0", online)
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
			"execBinPath": "/custom/dcexec",
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
	if !ok || agentMap["execBinPath"] != "/custom/dcexec" {
		t.Fatalf("unexpected settings payload: %v", got.Settings)
	}

	// 2. UpdateAdminFields must not touch Settings
	host := "192.168.1.100"
	if err := repo.UpdateAdminFields(ctx, "settings-node", NodeAdminPatch{
		DialHost: &host,
	}); err != nil {
		t.Fatalf("UpdateAdminFields: %v", err)
	}

	gotAfterAdmin, err := repo.GetByNodeID(ctx, "settings-node")
	if err != nil {
		t.Fatalf("GetByNodeID after UpdateAdminFields: %v", err)
	}
	if gotAfterAdmin.DialHost != "192.168.1.100" {
		t.Fatalf("expected DialHost updated to 192.168.1.100, got %q", gotAfterAdmin.DialHost)
	}
	if gotAfterAdmin.Settings == nil {
		t.Fatalf("Settings was unexpectedly cleared by UpdateAdminFields")
	}
	agentMap2, ok := gotAfterAdmin.Settings["agent"].(map[string]any)
	if !ok || agentMap2["execBinPath"] != "/custom/dcexec" {
		t.Fatalf("Settings corrupted after UpdateAdminFields: %v", gotAfterAdmin.Settings)
	}
}

func TestMarkOnlineRegisterPersistsPlatformColumns(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Node{NodeID: "plat-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkOnlineRegister(ctx, "plat-1", "linux", "amd64", "Ubuntu", "22.04", "6.1.0-1"); err != nil {
		t.Fatalf("mark online register: %v", err)
	}
	got, err := repo.GetByNodeID(ctx, "plat-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OS != "linux" || got.Arch != "amd64" || got.OSName != "Ubuntu" || got.OSVersion != "22.04" || got.KernelVersion != "6.1.0-1" {
		t.Fatalf("platform = %+v", got)
	}
	if err := repo.TouchHeartbeat(ctx, "plat-1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	after, err := repo.GetByNodeID(ctx, "plat-1")
	if err != nil {
		t.Fatalf("get after heartbeat: %v", err)
	}
	if after.OSName != "Ubuntu" || after.OSVersion != "22.04" || after.KernelVersion != "6.1.0-1" {
		t.Fatalf("heartbeat must not change version fields: %+v", after)
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
			Groups: []string{"gpu", "east"},
		},
		{
			NodeID: "gpu-02",
			Status: model.NodeStatusOffline,
			OS:     "linux",
			Groups: []string{"gpu", "west"},
		},
		{
			NodeID: "win-01",
			Status: model.NodeStatusOnline,
			OS:     "windows",
			Groups: []string{"cpu", "east"},
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

	// 3. Filter by groups (AND)
	res, err = repo.ListByFilter(ctx, NodeFilter{Groups: []string{"gpu"}})
	if err != nil || len(res) != 2 {
		t.Fatalf("expected 2 gpu nodes, got %d (err=%v)", len(res), err)
	}

	// 4. Filter by multiple groups
	res, err = repo.ListByFilter(ctx, NodeFilter{Groups: []string{"gpu", "west"}})
	if err != nil || len(res) != 1 || res[0].NodeID != "gpu-02" {
		t.Fatalf("expected gpu-02, got %v (err=%v)", res, err)
	}

	// 5. No match
	res, err = repo.ListByFilter(ctx, NodeFilter{NodeIDs: []string{"nonexistent"}})
	if err != nil || len(res) != 0 {
		t.Fatalf("expected 0 nodes, got %d (err=%v)", len(res), err)
	}
}

func TestNodeListPage(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	for _, id := range []string{"a-node", "b-node", "c-node"} {
		if err := repo.Create(ctx, &model.Node{NodeID: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	page, total, err := repo.ListPage(ctx, NodeFilter{}, 2, 0)
	if err != nil || total != 3 || len(page) != 2 || page[0].NodeID != "a-node" || page[1].NodeID != "b-node" {
		t.Fatalf("first page: page=%v total=%d err=%v", page, total, err)
	}
	page, total, err = repo.ListPage(ctx, NodeFilter{}, 2, 2)
	if err != nil || total != 3 || len(page) != 1 || page[0].NodeID != "c-node" {
		t.Fatalf("last page: page=%v total=%d err=%v", page, total, err)
	}

	groupsPage, total, err := repo.ListPage(ctx, NodeFilter{Groups: []string{"gpu"}}, 10, 0)
	if err != nil {
		t.Fatalf("groups page: %v", err)
	}
	_ = groupsPage
	if total != 0 {
		t.Fatalf("expected 0 nodes with group gpu, total=%d", total)
	}

	if err := repo.Create(ctx, &model.Node{NodeID: "gpu-a", Groups: []string{"gpu", "east"}}); err != nil {
		t.Fatalf("create gpu-a: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{NodeID: "gpu-b", Groups: []string{"gpu", "west"}}); err != nil {
		t.Fatalf("create gpu-b: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{NodeID: "cpu-a", Groups: []string{"cpu"}}); err != nil {
		t.Fatalf("create cpu-a: %v", err)
	}
	groupsPage, total, err = repo.ListPage(ctx, NodeFilter{Groups: []string{"gpu"}}, 1, 0)
	if err != nil || total != 2 || len(groupsPage) != 1 || groupsPage[0].NodeID != "gpu-a" {
		t.Fatalf("gpu first page: page=%v total=%d err=%v", groupsPage, total, err)
	}
	groupsPage, total, err = repo.ListPage(ctx, NodeFilter{Groups: []string{"gpu"}}, 1, 1)
	if err != nil || total != 2 || len(groupsPage) != 1 || groupsPage[0].NodeID != "gpu-b" {
		t.Fatalf("gpu second page: page=%v total=%d err=%v", groupsPage, total, err)
	}
}

func TestAdminDescriptionSurvivesConcurrentHeartbeat(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()
	if err := repo.Create(ctx, &model.Node{
		NodeID:      "race-node",
		Status:      model.NodeStatusOffline,
		Description: "before",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = repo.TouchHeartbeat(ctx, "race-node")
			}
		}
	}()

	for range 50 {
		desc := "admin-updated"
		if err := repo.UpdateAdminFields(ctx, "race-node", NodeAdminPatch{Description: &desc}); err != nil {
			t.Fatalf("update admin: %v", err)
		}
	}
	close(done)

	got, err := repo.GetByNodeID(ctx, "race-node")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Description != "admin-updated" {
		t.Fatalf("description = %q, want admin-updated", got.Description)
	}
}
