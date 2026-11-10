package connection

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func newTestPlacerDB(t *testing.T) (*db.Store, *db.Repos) {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "conn-placer-test.db"),
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
	return store, db.NewRepos(store)
}

func TestPlacer_ExcludeNodeIDs(t *testing.T) {
	_, repos := newTestPlacerDB(t)
	ctx := context.Background()

	// 准备 3 个健康节点
	nodes := []model.Node{
		{NodeID: "node-1", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux"},
		{NodeID: "node-2", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux"},
		{NodeID: "node-3", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux"},
	}
	for _, n := range nodes {
		if err := repos.Node.Create(ctx, &n); err != nil {
			t.Fatalf("create node %s: %v", n.NodeID, err)
		}
	}

	placer := NewPlacer(repos.Node, repos.Connection)

	res := model.ConnectionResource{
		Name:     "rdp-pool",
		Protocol: contract.RDPProtocol,
		Nodes:    []string{"node-1", "node-2", "node-3"},
	}

	// 1. 无排除时返回全部 3 个节点
	ranked, err := placer.RankedNodes(ctx, res, nil)
	if err != nil {
		t.Fatalf("RankedNodes failed: %v", err)
	}
	if len(ranked) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(ranked))
	}

	// 2. 排除 node-1 后，候选名单只有 node-2 和 node-3
	exclude := map[string]struct{}{
		"node-1": {},
	}
	ranked, err = placer.RankedNodes(ctx, res, exclude)
	if err != nil {
		t.Fatalf("RankedNodes failed: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(ranked))
	}
	for _, n := range ranked {
		if n.NodeID == "node-1" {
			t.Errorf("node-1 should have been excluded")
		}
	}

	// 3. 排除全部节点时，应报错 ErrAllNodesOccupied
	excludeAll := map[string]struct{}{
		"node-1": {},
		"node-2": {},
		"node-3": {},
	}
	_, err = placer.RankedNodes(ctx, res, excludeAll)
	if !errors.Is(err, ErrAllNodesOccupied) {
		t.Fatalf("expected ErrAllNodesOccupied when all nodes excluded, got %v", err)
	}

	// 4. SelectNode 也直接返回 ErrAllNodesOccupied
	_, err = placer.SelectNode(ctx, res, excludeAll)
	if !errors.Is(err, ErrAllNodesOccupied) {
		t.Fatalf("expected ErrAllNodesOccupied from SelectNode, got %v", err)
	}
}

func TestPlacer_LoadBalancing(t *testing.T) {
	_, repos := newTestPlacerDB(t)
	ctx := context.Background()

	// 准备 2 个节点
	n1 := model.Node{NodeID: "node-1", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux"}
	n2 := model.Node{NodeID: "node-2", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux"}
	if err := repos.Node.Create(ctx, &n1); err != nil {
		t.Fatalf("create node-1: %v", err)
	}
	if err := repos.Node.Create(ctx, &n2); err != nil {
		t.Fatalf("create node-2: %v", err)
	}

	// 在 node-1 上添加 2 个 running 连接
	c1 := model.Connection{Username: "u1", Protocol: contract.SSHProtocol, NodeID: "node-1", Port: 22, Status: model.ConnectionRunning}
	c2 := model.Connection{Username: "u2", Protocol: contract.SSHProtocol, NodeID: "node-1", Port: 22, Status: model.ConnectionRunning}
	if err := repos.Connection.Create(ctx, &c1); err != nil {
		t.Fatalf("create c1: %v", err)
	}
	if err := repos.Connection.Create(ctx, &c2); err != nil {
		t.Fatalf("create c2: %v", err)
	}

	placer := NewPlacer(repos.Node, repos.Connection)
	res := model.ConnectionResource{
		Name:     "ssh-pool",
		Protocol: contract.SSHProtocol,
		Nodes:    []string{"node-1", "node-2"},
	}

	// SelectNode 应优先选择负载更轻的 node-2
	selected, err := placer.SelectNode(ctx, res, nil)
	if err != nil {
		t.Fatalf("SelectNode failed: %v", err)
	}
	if selected.NodeID != "node-2" {
		t.Errorf("expected node-2 (0 conns) to be selected over node-1 (2 conns), got %s", selected.NodeID)
	}
}

type stubPlacerSettings struct {
	defaultMaxConn uint
}

func (s stubPlacerSettings) MaxPerUserCap(ctx context.Context) (int, error) {
	return 16, nil
}

func (s stubPlacerSettings) GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error) {
	return schema.ConnectionSettings{DefaultMaxConnectionsPerNode: s.defaultMaxConn}, nil
}

func (s stubPlacerSettings) GetEffectiveAgentSettingsForNode(ctx context.Context, nodeID string) (schema.AgentSettings, error) {
	return schema.AgentSettings{}, nil
}

func TestPlacer_NodeCapacityLimits(t *testing.T) {
	_, repos := newTestPlacerDB(t)
	ctx := context.Background()

	// n1: 显式容量上限 2
	n1 := model.Node{NodeID: "node-1", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux", MaxConnections: 2}
	// n2: 容量 0（继承全局设置）
	n2 := model.Node{NodeID: "node-2", Status: model.NodeStatusOnline, AllowConnections: true, OS: "linux", MaxConnections: 0}
	if err := repos.Node.Create(ctx, &n1); err != nil {
		t.Fatalf("create node-1: %v", err)
	}
	if err := repos.Node.Create(ctx, &n2); err != nil {
		t.Fatalf("create node-2: %v", err)
	}

	// 全局默认单节点上限为 1
	settingsReader := stubPlacerSettings{defaultMaxConn: 1}
	placer := NewPlacer(repos.Node, repos.Connection).WithSettings(settingsReader)

	res := model.ConnectionResource{
		Name:     "web-pool",
		Protocol: contract.SSHProtocol,
		Nodes:    []string{"node-1", "node-2"},
	}

	// 1. 初始状态：node-1(0/2), node-2(0/1) -> 均可用，两节点按 nodeID 排序
	ranked, err := placer.RankedNodes(ctx, res, nil)
	if err != nil || len(ranked) != 2 {
		t.Fatalf("expected 2 nodes, got %d (err: %v)", len(ranked), err)
	}

	// 2. node-2 创建 1 个连接 -> node-2 达到全局上限 1，应该被剔除
	c2 := model.Connection{Username: "u1", Protocol: contract.SSHProtocol, NodeID: "node-2", Port: 22, Status: model.ConnectionRunning}
	if err := repos.Connection.Create(ctx, &c2); err != nil {
		t.Fatalf("create c2: %v", err)
	}
	ranked, err = placer.RankedNodes(ctx, res, nil)
	if err != nil || len(ranked) != 1 || ranked[0].NodeID != "node-1" {
		t.Fatalf("expected only node-1 available (node-2 full), got %v (err: %v)", ranked, err)
	}

	// 3. node-1 创建 1 个连接 -> node-1 (1/2) 依然可用
	c1a := model.Connection{Username: "u2", Protocol: contract.SSHProtocol, NodeID: "node-1", Port: 22, Status: model.ConnectionRunning}
	if err := repos.Connection.Create(ctx, &c1a); err != nil {
		t.Fatalf("create c1a: %v", err)
	}
	selected, err := placer.SelectNode(ctx, res, nil)
	if err != nil || selected.NodeID != "node-1" {
		t.Fatalf("expected node-1 selected, got %s (err: %v)", selected.NodeID, err)
	}

	// 4. node-1 创建第 2 个连接 -> node-1 (2/2) 也满了
	c1b := model.Connection{Username: "u3", Protocol: contract.SSHProtocol, NodeID: "node-1", Port: 22, Status: model.ConnectionRunning}
	if err := repos.Connection.Create(ctx, &c1b); err != nil {
		t.Fatalf("create c1b: %v", err)
	}

	// 5. 此时全部候选节点已满载 -> 期望 ErrNodeConnectionLimit
	_, err = placer.RankedNodes(ctx, res, nil)
	if !errors.Is(err, ErrNodeConnectionLimit) {
		t.Fatalf("expected ErrNodeConnectionLimit when all nodes full, got %v", err)
	}
	_, err = placer.SelectNode(ctx, res, nil)
	if !errors.Is(err, ErrNodeConnectionLimit) {
		t.Fatalf("expected ErrNodeConnectionLimit from SelectNode when all nodes full, got %v", err)
	}
}
