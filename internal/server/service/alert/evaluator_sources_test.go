package alert

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/settings"
)

// newSourceEvaluator 构造一个真实 DB 支撑的告警引擎（默认 DomainAlert 配置 + 告警历史仓储），
// 用于覆盖 EvalResource/EvalStorage/GetHistory 数据源接入路径。
func newSourceEvaluator(t *testing.T) (*Evaluator, *push.Client, *db.Repos) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := settings.EnsureDefaults(store); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}

	repos := db.NewRepos(store)
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(t.TempDir(), "audit.log"),
		SecurityLogPath: filepath.Join(t.TempDir(), "security.log"),
		NodeID:          "test-node",
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() {
		if err := auditSvc.Close(); err != nil {
			t.Fatalf("close audit service: %v", err)
		}
	})

	svc, err := settings.NewService(repos.Setting, auditSvc, nil)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	svc.SetNodeRepo(repos.Node)

	hub := push.NewHub()
	client := hub.Register("admin-1", model.RoleSystemAdmin)
	t.Cleanup(func() { hub.Unregister(client) })

	e := NewEvaluator(hub, svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetHistoryRepo(repos.AlertHistory)
	return e, client, repos
}

// TestEvalResourceMemoryAndDisk 验证节点资源上报：内存/磁盘使用率触发与恢复。
// 默认 DomainAlert：Memory.Enabled(UsedPercent=90, MinFree=0)、Disk.Enabled(UsedPercent=90, MinFree=0)。
func TestEvalResourceMemoryAndDisk(t *testing.T) {
	e, client, _ := newSourceEvaluator(t)
	ctx := context.Background()

	// 触发：内存 93.75% >= 90 → fire；磁盘 90% >= 90 → fire
	report := &pb.ResourceReport{
		MemTotal: 128 * 1024 * 1024,
		MemUsed:  120 * 1024 * 1024, // 93.75%
		DiskUsages: []*pb.DiskUsage{
			{MountPoint: "/", Total: 500 * 1024 * 1024, Used: 450 * 1024 * 1024}, // 90%
		},
	}
	if err := e.EvalResource(ctx, "node-01", report); err != nil {
		t.Fatalf("EvalResource: %v", err)
	}
	mustReceive(t, client) // memory fire
	mustReceive(t, client) // disk fire
	mustNotReceive(t, client)

	// 恢复：内存 50%、磁盘 40%
	reportOK := &pb.ResourceReport{
		MemTotal: 128 * 1024 * 1024,
		MemUsed:  64 * 1024 * 1024, // 50%
		DiskUsages: []*pb.DiskUsage{
			{MountPoint: "/", Total: 500 * 1024 * 1024, Used: 200 * 1024 * 1024}, // 40%
		},
	}
	if err := e.EvalResource(ctx, "node-01", reportOK); err != nil {
		t.Fatalf("EvalResource recover: %v", err)
	}
	mustReceive(t, client) // memory recover
	mustReceive(t, client) // disk recover
	mustNotReceive(t, client)
}

// TestEvalResourceDiskMinFree 验证磁盘最小剩余空间维度（MinFree，KB 口径）：
// 大盘（>MinFree）剩余低于阈值 → fire；小盘不误报。
func TestEvalResourceDiskMinFree(t *testing.T) {
	e, client, repos := newSourceEvaluator(t)
	ctx := context.Background()

	// 覆盖：磁盘 MinFreeSpace = 50GB（52428800 KB），关闭 maxUsedPercent 维度
	enabled := true
	usedPercent := float64(0)
	minFree := int64(52428800)
	node := &model.Node{
		NodeID: "node-01",
		Settings: map[string]any{
			"alert": map[string]any{
				"disk": map[string]any{
					"enabled":        enabled,
					"maxUsedPercent": usedPercent,
					"minFreeSpace":   minFree,
				},
			},
		},
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// 大盘（500GB）剩余 20GB < 50GB → fire
	bigDisk := &pb.ResourceReport{
		DiskUsages: []*pb.DiskUsage{
			{MountPoint: "/", Total: 500 * 1024 * 1024, Used: 480 * 1024 * 1024}, // free=20GB (20971520 KB)
		},
	}
	if err := e.EvalResource(ctx, "node-01", bigDisk); err != nil {
		t.Fatalf("EvalResource big disk: %v", err)
	}
	fireNotify := mustReceive(t, client)
	expectedFireMsg := "node.node-01.disk. 当前值 20971520.00，阈值 52428800.00"
	if fireNotify.Message != expectedFireMsg {
		t.Errorf("got notify.Message = %q, want %q", fireNotify.Message, expectedFireMsg)
	}

	// 恢复：剩余 300GB > 50GB
	bigDiskOK := &pb.ResourceReport{
		DiskUsages: []*pb.DiskUsage{
			{MountPoint: "/", Total: 500 * 1024 * 1024, Used: 200 * 1024 * 1024}, // free=300GB
		},
	}
	if err := e.EvalResource(ctx, "node-01", bigDiskOK); err != nil {
		t.Fatalf("EvalResource big disk recover: %v", err)
	}
	mustReceive(t, client) // recover
	mustNotReceive(t, client)

	// 小盘（40GB，< MinFreeSpace 50GB）：剩余 10GB 也不该按 MinFreeSpace 误报
	smallDisk := &pb.ResourceReport{
		DiskUsages: []*pb.DiskUsage{
			{MountPoint: "/", Total: 40 * 1024 * 1024, Used: 30 * 1024 * 1024}, // free=10GB
		},
	}
	if err := e.EvalResource(ctx, "node-01", smallDisk); err != nil {
		t.Fatalf("EvalResource small disk: %v", err)
	}
	mustNotReceive(t, client)
}

// TestEvalResourceNodeOverride 验证节点级 override 可开启默认关闭的 CPU 告警维度。
func TestEvalResourceNodeOverride(t *testing.T) {
	e, client, repos := newSourceEvaluator(t)
	ctx := context.Background()

	enabledCPU := true
	usedPercentCPU := float64(80)
	enabledMem := false
	usedPercentMem := float64(0)
	minFreeMem := int64(0)
	node := &model.Node{
		NodeID: "node-01",
		Settings: map[string]any{
			"alert": map[string]any{
				"cpu": map[string]any{
					"enabled":        enabledCPU,
					"maxUsedPercent": usedPercentCPU,
				},
				"memory": map[string]any{
					"enabled":        enabledMem,
					"maxUsedPercent": usedPercentMem,
					"minFreeSpace":   minFreeMem,
				},
			},
		},
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	report := &pb.ResourceReport{
		MemTotal: 128 * 1024 * 1024,
		MemUsed:  64 * 1024 * 1024, // 50%（memory 已禁用，不影响）
		CpuCores: 16,
		CpuUtil:  0.9, // 90% >= 80 → CPU fire
	}
	if err := e.EvalResource(ctx, "node-01", report); err != nil {
		t.Fatalf("EvalResource: %v", err)
	}
	mustReceive(t, client) // CPU fire
	mustNotReceive(t, client)
}

// TestEvalResourceLoadPercent 验证 Load 按相对核数百分比 (load1/cores * 100) 评估告警。
func TestEvalResourceLoadPercent(t *testing.T) {
	e, client, repos := newSourceEvaluator(t)
	ctx := context.Background()

	// 开启 Load 告警：阈值 90% (100核超过90告警，8核超过7.2告警)
	node := &model.Node{
		NodeID: "node-100c",
		Settings: map[string]any{
			"alert": map[string]any{
				"load": map[string]any{
					"enabled":        true,
					"maxLoadPercent": float64(90),
				},
			},
		},
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// 100 核机器，Load1=91 → 91% >= 90% → fire
	reportFire := &pb.ResourceReport{
		CpuCores: 100,
		Load1:    91.0,
	}
	if err := e.EvalResource(ctx, "node-100c", reportFire); err != nil {
		t.Fatalf("EvalResource load: %v", err)
	}
	notify := mustReceive(t, client)
	expectedMsg := "node.node-100c.load 当前值 91.00，阈值 90.00"
	if notify.Message != expectedMsg {
		t.Errorf("got notify.Message = %q, want %q", notify.Message, expectedMsg)
	}

	// 恢复：Load1 降到 80 → 80% < 85.5% (带迟滞) → recover
	reportRecover := &pb.ResourceReport{
		CpuCores: 100,
		Load1:    80.0,
	}
	if err := e.EvalResource(ctx, "node-100c", reportRecover); err != nil {
		t.Fatalf("EvalResource load recover: %v", err)
	}
	recoverNotify := mustReceive(t, client)
	if recoverNotify.Level != "info" {
		t.Errorf("recover level = %q, want info", recoverNotify.Level)
	}
}

// TestEvalStorage 验证共享存储 probe 上报：使用率触发与恢复。
// 默认 DomainAlert.Storage：Enabled(MaxUsedPercent=90, MinFreeSpace=0)。
func TestEvalStorage(t *testing.T) {
	e, client, _ := newSourceEvaluator(t)
	ctx := context.Background()

	// 95% >= 90 → fire
	report := &pb.StorageProbeItem{
		StorageId: "share",
		Total:     1000 * 1024 * 1024,
		Used:      950 * 1024 * 1024,
	}
	if err := e.EvalStorage(ctx, report); err != nil {
		t.Fatalf("EvalStorage: %v", err)
	}
	mustReceive(t, client)

	// 50% → recover
	reportOK := &pb.StorageProbeItem{
		StorageId: "share",
		Total:     1000 * 1024 * 1024,
		Used:      500 * 1024 * 1024,
	}
	if err := e.EvalStorage(ctx, reportOK); err != nil {
		t.Fatalf("EvalStorage recover: %v", err)
	}
	mustReceive(t, client)
	mustNotReceive(t, client)
}

// TestEvalHistoryRecords 验证告警触发会落盘历史，且 GetHistory 可查询。
func TestEvalHistoryRecords(t *testing.T) {
	e, client, _ := newSourceEvaluator(t)
	ctx := context.Background()

	e.Eval(ctx, "node.n1.memory", 95, Rule{Threshold: 90, Cooldown: time.Hour, Level: "warning"})
	mustReceive(t, client)

	items, err := e.GetHistory(ctx, 10)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(items) < 1 {
		t.Fatal("expected at least one alert history record")
	}
	if items[0].RuleKey != "node.n1.memory" || items[0].AlertState != "fire" {
		t.Errorf("unexpected history record: %+v", items[0])
	}
}

// TestEvalResourceDualThresholdMetricConsistency 验证双阈值配置下，由 MinFreeSpace 触发的告警在恢复时量纲对齐一致（仍为容量 KB，非百分比）。
func TestEvalResourceDualThresholdMetricConsistency(t *testing.T) {
	e, client, repos := newSourceEvaluator(t)
	ctx := context.Background()

	// 覆盖：内存 MaxUsedPercent = 90, MinFreeSpace = 50GB (52428800 KB)
	enabled := true
	usedPercent := float64(90)
	minFree := int64(52428800)
	node := &model.Node{
		NodeID: "node-02",
		Settings: map[string]any{
			"alert": map[string]any{
				"memory": map[string]any{
					"enabled":        enabled,
					"maxUsedPercent": usedPercent,
					"minFreeSpace":   minFree,
				},
			},
		},
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// 场景 1：总内存 200GB，已用 160GB (80% < 90%)，但剩余 40GB < 50GB -> 由 MinFreeSpace 触发
	reportFire := &pb.ResourceReport{
		MemTotal: 200 * 1024 * 1024,
		MemUsed:  160 * 1024 * 1024,
	}
	if err := e.EvalResource(ctx, "node-02", reportFire); err != nil {
		t.Fatalf("EvalResource fire: %v", err)
	}
	mustReceive(t, client)

	// 验证触发时的历史记录
	history, err := e.GetHistory(ctx, 2)
	if err != nil || len(history) < 1 {
		t.Fatalf("get history after fire: %v", err)
	}
	if history[0].AlertState != "fire" || history[0].Threshold != float64(minFree) {
		t.Fatalf("expected fire threshold to be minFree (%v), got %v", minFree, history[0].Threshold)
	}

	// 场景 2：释放内存，总内存 200GB，已用 120GB (60% < 90%)，剩余 80GB (> 50GB*1.05 = 52.5GB) -> 恢复
	reportRecover := &pb.ResourceReport{
		MemTotal: 200 * 1024 * 1024,
		MemUsed:  120 * 1024 * 1024, // free = 80GB (83886080 KB)
	}
	if err := e.EvalResource(ctx, "node-02", reportRecover); err != nil {
		t.Fatalf("EvalResource recover: %v", err)
	}
	mustReceive(t, client)

	// 验证恢复时的历史记录：量纲应为 KB 且对应 MinFreeSpace，而非百分比
	historyAfter, err := e.GetHistory(ctx, 2)
	if err != nil || len(historyAfter) < 2 {
		t.Fatalf("get history after recover: %v", err)
	}
	latest := historyAfter[0]
	if latest.AlertState != "recover" {
		t.Fatalf("expected latest history to be recover, got %s", latest.AlertState)
	}
	expectedFreeKB := float64(80 * 1024 * 1024)
	if latest.Metric != expectedFreeKB {
		t.Errorf("expected recover metric to be freeKB (%v), got %v", expectedFreeKB, latest.Metric)
	}
	if latest.Threshold != float64(minFree) {
		t.Errorf("expected recover threshold to match minFree (%v), got %v", minFree, latest.Threshold)
	}
}
