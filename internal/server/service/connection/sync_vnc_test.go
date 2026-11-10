package connection

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

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
		resolvePortalUser: func(_ context.Context, osUser string) (string, bool) {
			if osUser == "" {
				return "", false
			}
			return osUser, true
		},
	}
	return svc, repos
}

func TestSyncConnectionReport_MatchesOnNodeUserPort(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", AllowConnections: true, Status: model.NodeStatusOnline})

	// Initial scan: connection reported
	vnc1 := []contract.VNCSeatInfo{
		{
			Username: "levin",
			Port:     5901,
			Display:  new(1),
			PID:      1234,
		},
	}

	if err := svc.SyncConnectionReport(ctx, "node01", contract.ConnectionReport{VNCSeats: vnc1}); err != nil {
		t.Fatalf("initial SyncConnectionReport failed: %v", err)
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
	if conns[0].Display == nil || *conns[0].Display != 1 {
		t.Fatalf("display = %v, want 1", conns[0].Display)
	}

	// Subsequent scan: connection has changed PID
	vnc2 := []contract.VNCSeatInfo{
		{
			Username: "levin",
			Port:     5901,
			Display:  new(1),
			PID:      5678,
		},
	}

	if err := svc.SyncConnectionReport(ctx, "node01", contract.ConnectionReport{VNCSeats: vnc2}); err != nil {
		t.Fatalf("second SyncConnectionReport failed: %v", err)
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
	if connsAfter[0].PID == nil || *connsAfter[0].PID != 5678 {
		t.Errorf("expected PID to be updated to 5678, got %v", connsAfter[0].PID)
	}
	if connsAfter[0].Status != model.ConnectionRunning {
		t.Errorf("expected Running status, got %d", connsAfter[0].Status)
	}
	if conns[0].LastAccessedAt == nil || connsAfter[0].LastAccessedAt == nil || !connsAfter[0].LastAccessedAt.Equal(*conns[0].LastAccessedAt) {
		t.Fatalf("reconcile changed last_accessed_at from %v to %v", conns[0].LastAccessedAt, connsAfter[0].LastAccessedAt)
	}
}

func TestSyncConnectionReport_GracePeriodPreventsPrematureStop(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	disp := 1
	conn := model.Connection{
		Username: "alice",
		Protocol: contract.VNCProtocol,
		NodeID:   "node01",
		Display:  &disp,
		Port:     5901,
		Status:   model.ConnectionRunning,
	}
	now := time.Now()
	conn.ControlledAt = &now
	if err := repos.Connection.Create(ctx, &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	// Probe runs and reports no connections on node01
	if err := svc.SyncConnectionReport(ctx, "node01", contract.ConnectionReport{}); err != nil {
		t.Fatalf("SyncConnectionReport failed: %v", err)
	}

	// Connection should still be Running because of grace period
	after, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if after.Status != model.ConnectionRunning {
		t.Errorf("expected status to remain Running during grace period, got %s", after.Status)
	}
}

func TestWithinVNCProbeGrace(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	old := now.Add(-20 * time.Second)
	fresh := now.Add(-5 * time.Second)
	if withinVNCProbeGrace(nil, now) {
		t.Fatal("nil controlled_at is outside grace")
	}
	if withinVNCProbeGrace(&old, now) {
		t.Fatal("expected expired controlled_at to be outside grace")
	}
	if !withinVNCProbeGrace(&fresh, now) {
		t.Fatal("expected fresh controlled_at to remain inside grace")
	}
}

//go:fix inline
func displayPtr(n int) *int {
	return new(n)
}

func TestSyncConnectionReport_ResourceBindingScenarios(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	// 1. 初始化节点 node01, node02
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID:           "node01",
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
		Groups:           []string{"eda-group"},
	})
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID:           "node02",
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
		Groups:           []string{"eda-group"},
	})

	// 2. 初始化用户 alice, bob
	alice := model.User{Username: "alice", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create user alice: %v", err)
	}
	bob := model.User{Username: "bob", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &bob); err != nil {
		t.Fatalf("create user bob: %v", err)
	}

	// 3. 创建规格
	// resFixed1: node01 display:1 固定专机
	resFixed1 := model.ConnectionResource{
		Name:       "Node01-Fixed1",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node01"},
		Display:    new(1),
		MaxPerUser: 1,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resFixed1); err != nil {
		t.Fatalf("create resFixed1: %v", err)
	}

	// resDynamicPool: eda-group 动态池
	resDynamicPool := model.ConnectionResource{
		Name:       "EDA-Pool",
		Protocol:   contract.VNCProtocol,
		NodeGroups: []string{"eda-group"},
		Display:    nil,
		MaxPerUser: 2,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resDynamicPool); err != nil {
		t.Fatalf("create resDynamicPool: %v", err)
	}

	// resPrivate: 非公开规格，未授权给 alice；放在 node09 上，避免被 node01 的 eda-group 动态池覆盖
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID:           "node09",
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
	})
	resPrivate := model.ConnectionResource{
		Name:       "Private-Fixed3",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node09"},
		Display:    new(3),
		MaxPerUser: 1,
		Public:     false,
	}
	if err := repos.ConnectionResource.Create(ctx, &resPrivate); err != nil {
		t.Fatalf("create resPrivate: %v", err)
	}

	// --- 场景 A: 新上报固定 display:1 自动绑定 resFixed1 并命名为 <res.Name>-node01 ---
	reportFixed := contract.ConnectionReport{
		VNCSeats: []contract.VNCSeatInfo{
			{
				Username: "alice",
				Port:     5901,
				Display:  new(1),
				PID:      1001,
			},
		},
	}
	if err := svc.SyncConnectionReport(ctx, "node01", reportFixed); err != nil {
		t.Fatalf("sync fixed failed: %v", err)
	}
	conns, _ := repos.Connection.ListByUser(ctx, "alice")
	if len(conns) != 1 {
		t.Fatalf("expected 1 conn for alice, got %d", len(conns))
	}
	if conns[0].ResourceID == nil || *conns[0].ResourceID != resFixed1.ID {
		t.Fatalf("expected bound to resFixed1 (%d), got %v", resFixed1.ID, conns[0].ResourceID)
	}
	if wantName := model.ConnectionName(resFixed1.Name, "node01"); conns[0].Name != wantName {
		t.Fatalf("expected name %q, got %q", wantName, conns[0].Name)
	}

	// --- 场景 B: 新上报 display:2 命中动态池 eda-group 自动绑定 ---
	reportDynamic := contract.ConnectionReport{
		VNCSeats: []contract.VNCSeatInfo{
			{
				Username: "alice",
				Port:     5901,
				Display:  new(1),
				PID:      1001,
			},
			{
				Username: "alice",
				Port:     5902,
				Display:  new(2),
				PID:      1002,
			},
		},
	}
	if err := svc.SyncConnectionReport(ctx, "node01", reportDynamic); err != nil {
		t.Fatalf("sync dynamic failed: %v", err)
	}
	conns, _ = repos.Connection.ListByUser(ctx, "alice")
	if len(conns) != 2 {
		t.Fatalf("expected 2 conns for alice, got %d", len(conns))
	}
	var conn2 *model.Connection
	for i := range conns {
		if conns[i].Display != nil && *conns[i].Display == 2 {
			conn2 = &conns[i]
			break
		}
	}
	if conn2 == nil || conn2.ResourceID == nil || *conn2.ResourceID != resDynamicPool.ID {
		t.Fatalf("expected conn2 bound to resDynamicPool (%d), got %+v", resDynamicPool.ID, conn2)
	}
	if wantName := model.ConnectionName(resDynamicPool.Name, "node01"); conn2.Name != wantName {
		t.Fatalf("expected name %q, got %q", wantName, conn2.Name)
	}

	// --- 场景 C: 未授权的非公开规格不匹配 ---
	reportPrivate := contract.ConnectionReport{
		VNCSeats: []contract.VNCSeatInfo{
			{
				Username: "alice",
				Port:     5903,
				Display:  new(3),
				PID:      1003,
			},
		},
	}
	if err := svc.SyncConnectionReport(ctx, "node09", reportPrivate); err != nil {
		t.Fatalf("sync private failed: %v", err)
	}
	conns, _ = repos.Connection.ListByUser(ctx, "alice")
	var conn3 *model.Connection
	for i := range conns {
		if conns[i].NodeID == "node09" && conns[i].Display != nil && *conns[i].Display == 3 {
			conn3 = &conns[i]
			break
		}
	}
	if conn3 == nil {
		t.Fatalf("conn3 not found")
	}
	if conn3.ResourceID != nil {
		t.Fatalf("expected conn3 resourceID nil (unauthorized), got %v", *conn3.ResourceID)
	}

	// --- 场景 D: 先无规格入库，后创建规格，二次对账回填同一行的 resource_id 和规范名 ---
	// 在 node02 上由 bob 上报 display:4（当前无覆盖 node02 display:4 的固定规格，且此时暂无其它规格）
	connBob := model.Connection{
		Username: "bob",
		Protocol: contract.VNCProtocol,
		NodeID:   "node02",
		Port:     5904,
		Display:  new(4),
		Status:   model.ConnectionRunning,
		Name:     "node02:5904",
	}
	if err := repos.Connection.Create(ctx, &connBob); err != nil {
		t.Fatalf("create connBob: %v", err)
	}
	bobID := connBob.ID

	// 现在创建专机规格 resBobFixed4
	resBobFixed4 := model.ConnectionResource{
		Name:       "Node02-Fixed4",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node02"},
		Display:    new(4),
		MaxPerUser: 1,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resBobFixed4); err != nil {
		t.Fatalf("create resBobFixed4: %v", err)
	}

	// 探针对账 node02
	reportBob := contract.ConnectionReport{
		VNCSeats: []contract.VNCSeatInfo{
			{
				Username: "bob",
				Port:     5904,
				Display:  new(4),
				PID:      2004,
			},
		},
	}
	if err := svc.SyncConnectionReport(ctx, "node02", reportBob); err != nil {
		t.Fatalf("sync bob failed: %v", err)
	}

	afterBob, err := repos.Connection.GetByID(ctx, bobID)
	if err != nil {
		t.Fatalf("get bob conn: %v", err)
	}
	if afterBob.ResourceID == nil || *afterBob.ResourceID != resBobFixed4.ID {
		t.Fatalf("expected bob conn bound to resBobFixed4 (%d), got %v", resBobFixed4.ID, afterBob.ResourceID)
	}
	if wantName := model.ConnectionName(resBobFixed4.Name, "node02"); afterBob.Name != wantName {
		t.Fatalf("expected bob conn name %q, got %q", wantName, afterBob.Name)
	}

	// --- 场景 E: 进程已停（空上报）、resource_id 为空的行，对账也会回填 ---
	connStopped := model.Connection{
		Username: "bob",
		Protocol: contract.VNCProtocol,
		NodeID:   "node02",
		Port:     5905,
		Display:  new(5),
		Status:   model.ConnectionStopped,
		Name:     "node02:5905",
	}
	if err := repos.Connection.Create(ctx, &connStopped); err != nil {
		t.Fatalf("create connStopped: %v", err)
	}
	resBobFixed5 := model.ConnectionResource{
		Name:       "Node02-Fixed5",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node02"},
		Display:    new(5),
		MaxPerUser: 1,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resBobFixed5); err != nil {
		t.Fatalf("create resBobFixed5: %v", err)
	}

	// 空上报同步 node02
	if err := svc.SyncConnectionReport(ctx, "node02", contract.ConnectionReport{}); err != nil {
		t.Fatalf("empty sync node02 failed: %v", err)
	}
	afterStopped, err := repos.Connection.GetByID(ctx, connStopped.ID)
	if err != nil {
		t.Fatalf("get stopped conn: %v", err)
	}
	if afterStopped.ResourceID == nil || *afterStopped.ResourceID != resBobFixed5.ID {
		t.Fatalf("expected stopped conn bound to %d, got %v", resBobFixed5.ID, afterStopped.ResourceID)
	}

	// --- 场景 F: 已经是 stopped、无归属、名称非旧前缀、本轮无改动的行，对账后 updated_at 保持不变 ---
	time.Sleep(10 * time.Millisecond)
	connNoChange := model.Connection{
		Username: "bob",
		Protocol: contract.VNCProtocol,
		NodeID:   "node02",
		Port:     5999,
		Display:  new(99),
		Status:   model.ConnectionStopped,
		Name:     "node02:5999", // 并非旧前缀，且 display 99 在 node02 上有两个重叠动态池（将创建并列规格测试）
	}
	if err := repos.Connection.Create(ctx, &connNoChange); err != nil {
		t.Fatalf("create connNoChange: %v", err)
	}
	initConn, _ := repos.Connection.GetByID(ctx, connNoChange.ID)
	initUpdatedAt := initConn.UpdatedAt

	// 此时 node02 上只有 eda-group 动态池，为了制造并列，我们再加一个 eda-group2 动态池同时包含 node02
	_ = repos.Node.UpdateAdminFields(ctx, "node02", db.NodeAdminPatch{
		Groups:    []string{"eda-group", "eda-group2"},
		GroupsSet: true,
	})
	resTied2 := model.ConnectionResource{
		Name:       "EDA-Pool-2",
		Protocol:   contract.VNCProtocol,
		NodeGroups: []string{"eda-group2"},
		Display:    nil,
		MaxPerUser: 2,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resTied2); err != nil {
		t.Fatalf("create resTied2: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	// 再次对账 node02
	if err := svc.SyncConnectionReport(ctx, "node02", contract.ConnectionReport{}); err != nil {
		t.Fatalf("sync node02: %v", err)
	}

	checkConn, _ := repos.Connection.GetByID(ctx, connNoChange.ID)
	if checkConn.ResourceID != nil {
		t.Fatalf("tied pools must not bind, got %v", *checkConn.ResourceID)
	}
	if !checkConn.UpdatedAt.Equal(initUpdatedAt) {
		t.Fatalf("expected updated_at unchanged (%v), but got %v", initUpdatedAt, checkConn.UpdatedAt)
	}
}

func TestSyncConnectionReport_VNCPortalUsernameMappingAndNoOpSkip(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	// Simulate OS username "Alice_OS" mapping to portal username "alice"
	svc.resolvePortalUser = func(_ context.Context, osUser string) (string, bool) {
		if osUser == "Alice_OS" || osUser == "alice" {
			return "alice", true
		}
		return "", false
	}

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", AllowConnections: true, Status: model.NodeStatusOnline})

	report := contract.ConnectionReport{
		VNCSeats: []contract.VNCSeatInfo{
			{
				Username: "Alice_OS",
				Port:     5901,
				Display:  new(1),
				PID:      4321,
			},
		},
	}

	// First scan adopts under portal user "alice"
	if err := svc.SyncConnectionReport(ctx, "node01", report); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	conns, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil || len(conns) != 1 {
		t.Fatalf("expected 1 connection for alice, got %d (err=%v)", len(conns), err)
	}
	if conns[0].Status != model.ConnectionRunning {
		t.Fatalf("expected running, got %s", conns[0].Status)
	}
	firstUpdatedAt := conns[0].UpdatedAt

	time.Sleep(10 * time.Millisecond)

	// Second scan with same OS username "Alice_OS" must match existing "alice" row,
	// keep it running, and skip redundant DB write since nothing changed.
	if err := svc.SyncConnectionReport(ctx, "node01", report); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	connsAfter, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil || len(connsAfter) != 1 {
		t.Fatalf("expected 1 connection for alice after second sync, got %d (err=%v)", len(connsAfter), err)
	}
	if connsAfter[0].Status != model.ConnectionRunning {
		t.Fatalf("expected connection to remain running, got %s", connsAfter[0].Status)
	}
	if !connsAfter[0].UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("expected unchanged VNC seat to skip DB update (%v), got %v", firstUpdatedAt, connsAfter[0].UpdatedAt)
	}
}
