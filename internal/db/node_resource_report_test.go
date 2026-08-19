package db

import (
	"context"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
)

// TestAggregateResourceStats_OnlineOnly verifies resource stats only include
// nodes whose status is online:
//   - online node resources counted
//   - offline node resources excluded
//   - removed node's leftover report excluded (no matching nodes row)
func TestAggregateResourceStats_OnlineOnly(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()
	now := time.Now()

	// 注册 2 个节点：online + offline
	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-online",
		Status: model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create online node: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-offline",
		Status: model.NodeStatusOffline,
	}); err != nil {
		t.Fatalf("create offline node: %v", err)
	}

	// 三个节点的上报记录（第三个 node-removed 无对应 nodes 行，模拟已移除）
	reports := []*model.NodeResourceReport{
		{NodeID: "node-online", CPUCores: 8, MemTotal: 16384, MemUsed: 4096, ReportedAt: now},
		{NodeID: "node-offline", CPUCores: 4, MemTotal: 8192, MemUsed: 2048, ReportedAt: now},
		{NodeID: "node-removed", CPUCores: 16, MemTotal: 32768, MemUsed: 8192, ReportedAt: now},
	}
	for _, r := range reports {
		if err := repo.UpsertResourceReport(ctx, r); err != nil {
			t.Fatalf("upsert report %s: %v", r.NodeID, err)
		}
	}

	stats, err := repo.AggregateResourceStats(ctx, now.Add(-2*time.Minute))
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

// TestAggregateResourceStats_StaleOnline verifies online_* uses reported_at
// freshness: an online node with stale report is in total but not online.
func TestAggregateResourceStats_StaleOnline(t *testing.T) {
	t.Parallel()

	_, repo := newNodeRepoForTest(t)
	ctx := context.Background()
	now := time.Now()

	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-fresh",
		Status: model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create fresh node: %v", err)
	}
	if err := repo.Create(ctx, &model.Node{
		NodeID: "node-stale",
		Status: model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create stale node: %v", err)
	}

	// 一个刚上报，一个 10 分钟前上报（超 2min 新鲜窗口）
	if err := repo.UpsertResourceReport(ctx, &model.NodeResourceReport{
		NodeID: "node-fresh", CPUCores: 8, ReportedAt: now,
	}); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}
	if err := repo.UpsertResourceReport(ctx, &model.NodeResourceReport{
		NodeID: "node-stale", CPUCores: 4, ReportedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert stale: %v", err)
	}

	stats, err := repo.AggregateResourceStats(ctx, now.Add(-2*time.Minute))
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
