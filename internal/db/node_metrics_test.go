package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// TestAggregateMetrics_OnlineOnly verifies metrics stats only include
// nodes whose status is online:
//   - online node metrics counted
//   - offline node metrics excluded
//   - removed node's leftover metrics excluded (no matching nodes row)
func TestAggregateMetrics_OnlineOnly(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()
	now := time.Now()

	// 注册 2 个节点：online + offline
	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-online",
		Online: true,
	}); err != nil {
		t.Fatalf("create online node: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-offline",
		Online: false,
	}); err != nil {
		t.Fatalf("create offline node: %v", err)
	}

	// 三个节点的指标记录（第三个 node-removed 无对应 nodes 行，模拟已移除）
	rows := []*model.NodeMetrics{
		{NodeID: "node-online", CPUCores: 8, MemTotal: 16384, MemUsed: 4096, ReportedAt: now},
		{NodeID: "node-offline", CPUCores: 4, MemTotal: 8192, MemUsed: 2048, ReportedAt: now},
		{NodeID: "node-removed", CPUCores: 16, MemTotal: 32768, MemUsed: 8192, ReportedAt: now},
	}
	for _, row := range rows {
		if err := repo.UpsertMetrics(ctx, row); err != nil {
			t.Fatalf("upsert metrics %s: %v", row.NodeID, err)
		}
	}

	stats, err := repo.AggregateMetrics(ctx, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// Configured (已注册 2 节点的总配置能力): 12 核 / 24576 MB 内存 (node-removed 因在 nodes 表中无行被排除)
	if stats.TotalCores != 12 {
		t.Errorf("TotalCores = %d, want 12 (all registered nodes)", stats.TotalCores)
	}
	if stats.ConfiguredMem != 24576 {
		t.Errorf("ConfiguredMem = %d, want 24576 (all registered nodes)", stats.ConfiguredMem)
	}
	// Online (仅 online 且近期有上报的活跃节点能力): 8 核 / 16384 MB 内存
	if stats.OnlineCores != 8 {
		t.Errorf("OnlineCores = %d, want 8 (online node only)", stats.OnlineCores)
	}
	if stats.OnlineMem != 16384 {
		t.Errorf("OnlineMem = %d, want 16384 (online node only)", stats.OnlineMem)
	}
	if stats.TotalMemUsed != 4096 {
		t.Errorf("TotalMemUsed = %d, want 4096", stats.TotalMemUsed)
	}
}

// TestAggregateMetrics_StaleOnline verifies online_* uses reported_at
// freshness: an online node with stale metrics is in total but not online.
func TestAggregateMetrics_StaleOnline(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()
	now := time.Now()

	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-fresh",
		Online: true,
	}); err != nil {
		t.Fatalf("create fresh node: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-stale",
		Online: true,
	}); err != nil {
		t.Fatalf("create stale node: %v", err)
	}

	// 一个刚上报，一个 10 分钟前上报（超 2min 新鲜窗口）
	if err := repo.UpsertMetrics(ctx, &model.NodeMetrics{
		NodeID: "node-fresh", CPUCores: 8, ReportedAt: now,
	}); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}
	if err := repo.UpsertMetrics(ctx, &model.NodeMetrics{
		NodeID: "node-stale", CPUCores: 4, ReportedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert stale: %v", err)
	}

	stats, err := repo.AggregateMetrics(ctx, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// total = 两个在线节点（12 核）；online = 只有刚上报的（8 核）
	if stats.TotalCores != 12 {
		t.Errorf("TotalCores = %d, want 12 (both online nodes)", stats.TotalCores)
	}
	if stats.OnlineCores != 8 {
		t.Errorf("OnlineCores = %d, want 8 (only fresh reporter)", stats.OnlineCores)
	}
}

func TestNodeRepo_ListMetricsPage_And_CountActiveConnectionsByNodes(t *testing.T) {
	t.Parallel()

	store, repo := newNodeRepoForTest(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// 验证联合索引 idx_conn_status_node_id 已迁移
	if !store.db.Migrator().HasIndex(&model.Connection{}, "idx_conn_status_node_id") {
		t.Fatal("expected composite index idx_conn_status_node_id on connections")
	}

	nodes := []*model.Node{
		{NodeID: "n1", Groups: []string{"gpu", "bj"}, Online: true},
		{NodeID: "n2", Groups: []string{"cpu", "bj"}, Online: true},
		{NodeID: "n3", Groups: []string{"gpu", "sh"}, Online: false},
	}
	for _, n := range nodes {
		if err := repo.Create(ctx, n); err != nil {
			t.Fatalf("create node %s: %v", n.NodeID, err)
		}
	}

	// 插入 metrics（含一条孤立节点 n-orphan，验证 JOIN nodes 会过滤掉已删节点）
	metrics := []*model.NodeMetrics{
		{NodeID: "n1", CPUCores: 16, CPUUtil: 0.5, ReportedAt: now},
		{NodeID: "n2", CPUCores: 8, CPUUtil: 0.2, ReportedAt: now},
		{NodeID: "n3", CPUCores: 32, CPUUtil: 0.1, ReportedAt: now},
		{NodeID: "n-orphan", CPUCores: 4, CPUUtil: 0.9, ReportedAt: now},
	}
	for _, m := range metrics {
		if err := repo.UpsertMetrics(ctx, m); err != nil {
			t.Fatalf("upsert metrics %s: %v", m.NodeID, err)
		}
	}

	// 1. 全量分页（第 1 页 limit=2, offset=0）
	page1, total, err := repo.ListMetricsPage(ctx, nil, 2, 0)
	if err != nil {
		t.Fatalf("ListMetricsPage page1: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 (orphan excluded)", total)
	}
	if len(page1) != 2 || page1[0].NodeID != "n1" || page1[1].NodeID != "n2" {
		t.Fatalf("unexpected page1: %+v", page1)
	}

	// 2. 分组过滤（gpu AND bj => 只有 n1）
	gpuBJ, totalGPUBJ, err := repo.ListMetricsPage(ctx, []string{"gpu", "bj"}, 10, 0)
	if err != nil {
		t.Fatalf("ListMetricsPage groups: %v", err)
	}
	if totalGPUBJ != 1 || len(gpuBJ) != 1 || gpuBJ[0].NodeID != "n1" {
		t.Fatalf("unexpected gpu+bj metrics: total=%d rows=%+v", totalGPUBJ, gpuBJ)
	}

	// 3. CountActiveConnectionsByNodes 按指定节点统计 running 连接
	conns := []*model.Connection{
		{Name: "c1", Username: "u1", Protocol: contract.Protocol(1), NodeID: "n1", Port: 5901, Status: model.ConnectionRunning},
		{Name: "c2", Username: "u2", Protocol: contract.Protocol(1), NodeID: "n1", Port: 5902, Status: model.ConnectionRunning},
		{Name: "c3", Username: "u3", Protocol: contract.Protocol(1), NodeID: "n1", Port: 5903, Status: model.ConnectionStopped},
		{Name: "c4", Username: "u4", Protocol: contract.Protocol(1), NodeID: "n2", Port: 5901, Status: model.ConnectionRunning},
	}
	for _, c := range conns {
		if err := store.db.WithContext(ctx).Create(c).Error; err != nil {
			t.Fatalf("create conn %s: %v", c.Name, err)
		}
	}

	counts, err := repo.CountActiveConnectionsByNodes(ctx, []string{"n1", "n3"})
	if err != nil {
		t.Fatalf("CountActiveConnectionsByNodes: %v", err)
	}
	if counts["n1"] != 2 || counts["n2"] != 0 || counts["n3"] != 0 {
		t.Fatalf("unexpected counts: %+v", counts)
	}

	emptyCounts, err := repo.CountActiveConnectionsByNodes(ctx, nil)
	if err != nil || len(emptyCounts) != 0 {
		t.Fatalf("expected empty map for nil nodeIDs, got %+v err=%v", emptyCounts, err)
	}
}

// GetMetrics must not serve snapshots left behind by a node row that no longer exists.
func TestGetMetrics_OrphanSnapshotNotServed(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()

	if err := repo.UpsertMetrics(ctx, &model.NodeMetrics{NodeID: "node-removed", CPUCores: 8, ReportedAt: time.Now()}); err != nil {
		t.Fatalf("upsert metrics: %v", err)
	}
	if _, err := repo.GetMetrics(ctx, "node-removed"); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("GetMetrics err = %v, want ErrNotFound", err)
	}
}
