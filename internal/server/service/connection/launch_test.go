package connection

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/user"
)

type mockExecutor struct{}

func (m *mockExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return &execution.ExecuteResponse{Success: true}, nil
}

func newTestLaunchService(t *testing.T) (*Service, *db.Repos) {
	t.Helper()

	tmp := t.TempDir()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tmp, "conn-launch-test.db"),
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

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tmp, "audit.log"),
		SecurityLogPath: filepath.Join(tmp, "security.log"),
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}

	userSvc, err := user.NewService(
		&mockExecutor{},
		user.Config{AuthMode: config.UserAuthModeLocal},
		settingsSvc,
		repos.User,
		repos.TaskLease,
		repos.CacheVersion,
		nil,
		slog.Default(),
		auditSvc,
		nil,
	)
	if err != nil {
		t.Fatalf("new user service: %v", err)
	}

	placer := NewPlacer(repos.Node, repos.Connection)

	svc := &Service{
		connectionRepo:         repos.Connection,
		connectionResourceRepo: repos.ConnectionResource,
		nodeRepo:               repos.Node,
		userSvc:                userSvc,
		settingsSvc:            settingsSvc,
		placer:                 placer,
		logger:                 slog.Default(),
	}

	return svc, repos
}

func TestLaunch_RDP_MultiNode_QuotaAndExclusion(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	// 1. 创建测试用户 alice
	alice := model.User{
		Username: "alice",
		Role:     model.RoleNormal,
	}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// 2. 创建 2 个在线节点
	node1 := model.Node{
		NodeID:           "node-1",
		DialHost:         "192.168.1.101",
		Status:           model.NodeStatusOnline,
		AllowConnections: true,
		OS:               "windows",
	}
	node2 := model.Node{
		NodeID:           "node-2",
		DialHost:         "192.168.1.102",
		Status:           model.NodeStatusOnline,
		AllowConnections: true,
		OS:               "windows",
	}
	if err := repos.Node.Create(ctx, &node1); err != nil {
		t.Fatalf("create node1: %v", err)
	}
	if err := repos.Node.Create(ctx, &node2); err != nil {
		t.Fatalf("create node2: %v", err)
	}

	// 3. 创建多节点 RDP 资源规格，MaxPerUser = 2
	port := uint(3389)
	res := model.ConnectionResource{
		Name:       "rdp-cluster",
		Protocol:   contract.RDPProtocol,
		Nodes:      []string{"node-1", "node-2"},
		Port:       &port,
		MaxPerUser: 2,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	// 4. 第一次 Launch：应在 node-1 或 node-2 上创建连接
	conn1, err := svc.Launch(ctx, "alice", res.ID)
	if err != nil {
		t.Fatalf("first launch failed: %v", err)
	}
	if conn1.Username != "alice" || conn1.Protocol != contract.RDPProtocol {
		t.Fatalf("unexpected conn1: %+v", conn1)
	}
	firstNode := conn1.NodeID
	if firstNode != "node-1" && firstNode != "node-2" {
		t.Fatalf("expected node-1 or node-2, got %s", firstNode)
	}

	// 5. 第二次 Launch：node-1 已被 alice 占用，候选节点直接剔除已用节点，必须分配到另一个节点
	conn2, err := svc.Launch(ctx, "alice", res.ID)
	if err != nil {
		t.Fatalf("second launch failed: %v", err)
	}
	if conn2.ID == conn1.ID {
		t.Fatalf("expected different connection row, got same ID %d", conn2.ID)
	}
	if conn2.NodeID == firstNode {
		t.Fatalf("expected different node than %s, but got %s", firstNode, conn2.NodeID)
	}

	// 6. 第三次 Launch：已达 MaxPerUser = 2 上限，应直接复用已有连接并刷新时间，不新增记录
	conn3, err := svc.Launch(ctx, "alice", res.ID)
	if err != nil {
		t.Fatalf("third launch failed: %v", err)
	}
	if conn3.ID != conn1.ID && conn3.ID != conn2.ID {
		t.Fatalf("expected reuse of existing connection, got new ID %d", conn3.ID)
	}

	// 验证总连接数依然是 2
	allConns, err := repos.Connection.ListByUsernameAndResource(ctx, "alice", res.ID)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(allConns) != 2 {
		t.Fatalf("expected 2 connections in db, got %d", len(allConns))
	}
}

func TestLaunch_SSH_Rejected(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	bob := model.User{Username: "bob", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &bob); err != nil {
		t.Fatalf("create user: %v", err)
	}
	node := model.Node{
		NodeID: "node-ssh", DialHost: "10.0.0.1", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "linux",
	}
	if err := repos.Node.Create(ctx, &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	port := uint(22)
	res := model.ConnectionResource{
		Name: "ssh-single", Protocol: contract.SSHProtocol,
		Nodes: []string{"node-ssh"}, Port: &port, MaxPerUser: 1, Public: true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	_, err := svc.Launch(ctx, "bob", res.ID)
	if err == nil {
		t.Fatal("expected SSH launch to be rejected")
	}
	if !strings.Contains(err.Error(), "GET /api/connections/ssh") {
		t.Fatalf("error = %v, want SSH /ssh guidance", err)
	}
}

func TestLaunch_RDP_SingleNode_ReusesExisting(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	bob := model.User{Username: "bob", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &bob); err != nil {
		t.Fatalf("create user: %v", err)
	}
	node := model.Node{
		NodeID: "node-rdp", DialHost: "10.0.0.1", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "windows",
	}
	if err := repos.Node.Create(ctx, &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	port := uint(3389)
	res := model.ConnectionResource{
		Name: "rdp-single", Protocol: contract.RDPProtocol,
		Nodes: []string{"node-rdp"}, Port: &port, MaxPerUser: 1, Public: true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	c1, err := svc.Launch(ctx, "bob", res.ID)
	if err != nil {
		t.Fatalf("first launch failed: %v", err)
	}
	c2, err := svc.Launch(ctx, "bob", res.ID)
	if err != nil {
		t.Fatalf("second launch failed: %v", err)
	}
	if c2.ID != c1.ID {
		t.Fatalf("expected same connection ID %d, got %d", c1.ID, c2.ID)
	}
	allConns, err := repos.Connection.ListByUsernameAndResource(ctx, "bob", res.ID)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(allConns) != 1 {
		t.Fatalf("expected 1 connection in db, got %d", len(allConns))
	}
}

func TestLaunch_RDP_CandidateNodesExhausted_GracefulFallback(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	charlie := model.User{
		Username: "charlie",
		Role:     model.RoleNormal,
	}
	if err := repos.User.Create(ctx, &charlie); err != nil {
		t.Fatalf("create user: %v", err)
	}

	node := model.Node{
		NodeID:           "node-rdp-only",
		DialHost:         "10.0.0.2",
		Status:           model.NodeStatusOnline,
		AllowConnections: true,
		OS:               "windows",
	}
	if err := repos.Node.Create(ctx, &node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// 允许最多 5 个实例，但实际候选池只有 1 个物理节点
	port := uint(3389)
	res := model.ConnectionResource{
		Name:       "rdp-single-pool",
		Protocol:   contract.RDPProtocol,
		Nodes:      []string{"node-rdp-only"},
		Port:       &port,
		MaxPerUser: 5,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	// 第一次 Launch：分配 node-rdp-only
	c1, err := svc.Launch(ctx, "charlie", res.ID)
	if err != nil {
		t.Fatalf("first launch failed: %v", err)
	}

	// 第二次 Launch：虽然 MaxPerUser 没满，但所有节点均已占满。SelectNode 找不到新机器，优雅回退复用已有连接
	c2, err := svc.Launch(ctx, "charlie", res.ID)
	if err != nil {
		t.Fatalf("second launch failed: %v", err)
	}
	if c2.ID != c1.ID {
		t.Fatalf("expected reuse of connection %d when candidate nodes exhausted, got %d", c1.ID, c2.ID)
	}
}

func TestBuildAvoidDisplaysUsesResourceFixedOnly(t *testing.T) {
	ctx := context.Background()
	svc, repos := newTestLaunchService(t)

	otherDisplay := 9
	if err := repos.ConnectionResource.Create(ctx, &model.ConnectionResource{
		Name:     "fixed-other",
		Protocol: contract.VNCProtocol,
		Nodes:    []string{"node-a"},
		Display:  &otherDisplay,
		Public:   true,
	}); err != nil {
		t.Fatal(err)
	}
	otherNodeDisplay := 11
	if err := repos.ConnectionResource.Create(ctx, &model.ConnectionResource{
		Name:     "fixed-other-node",
		Protocol: contract.VNCProtocol,
		Nodes:    []string{"node-b"},
		Display:  &otherNodeDisplay,
		Public:   true,
	}); err != nil {
		t.Fatal(err)
	}

	disp := 2
	if err := repos.Connection.Create(ctx, &model.Connection{
		Name:     "running",
		Username: "alice",
		Protocol: contract.VNCProtocol,
		NodeID:   "node-a",
		Port:     5902,
		Display:  &disp,
		Status:   model.ConnectionRunning,
	}); err != nil {
		t.Fatal(err)
	}

	avoid, err := svc.buildAvoidDisplays(ctx, "node-a", 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(avoid) != 1 || avoid[0] != 9 {
		t.Fatalf("avoid = %v, want only resource fixed display 9 on node-a", avoid)
	}
}
