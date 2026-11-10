package connection

import (
	"context"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestReuseDial_StoppedReplacesWhenPreferredNodeDead(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	alice := model.User{Username: "alice", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repos.Node.Create(ctx, &model.Node{
		NodeID: "node-a", DialHost: "10.0.0.1", Status: model.NodeStatusOffline,
		AllowConnections: true, OS: "linux",
	}); err != nil {
		t.Fatalf("create node-a: %v", err)
	}
	if err := repos.Node.Create(ctx, &model.Node{
		NodeID: "node-b", DialHost: "10.0.0.2", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "linux",
	}); err != nil {
		t.Fatalf("create node-b: %v", err)
	}

	port := uint(3389)
	res := model.ConnectionResource{
		Name: "rdp-pool", Protocol: contract.RDPProtocol,
		Nodes: []string{"node-a", "node-b"}, Port: &port, MaxPerUser: 1, Public: true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	conn := model.Connection{
		ResourceID: &res.ID, Name: "rdp-pool-node-a", Username: "alice",
		Protocol: contract.RDPProtocol, NodeID: "node-a", Port: 3389, Status: model.ConnectionStopped,
	}
	if err := repos.Connection.Create(ctx, &conn); err != nil {
		t.Fatalf("create conn: %v", err)
	}

	got, err := svc.reuseDialConnection(ctx, conn, res, "alice")
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if got.ID != conn.ID {
		t.Fatalf("tombstone id changed: %d → %d", conn.ID, got.ID)
	}
	if got.NodeID != "node-b" {
		t.Fatalf("node = %s, want node-b (re-placed off offline node-a)", got.NodeID)
	}
	if got.Status != model.ConnectionRunning {
		t.Fatalf("status = %s, want running", got.Status)
	}
}

func TestStartConnection_DialUsesResourcePlacement(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	alice := model.User{Username: "alice", Role: model.RoleNormal}
	_ = repos.User.Create(ctx, &alice)
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID: "node-a", DialHost: "10.0.0.1", Status: model.NodeStatusOffline,
		AllowConnections: true, OS: "linux",
	})
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID: "node-b", DialHost: "10.0.0.2", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "linux",
	})

	port := uint(3389)
	res := model.ConnectionResource{
		Name: "rdp-pool", Protocol: contract.RDPProtocol,
		Nodes: []string{"node-a", "node-b"}, Port: &port, MaxPerUser: 1, Public: true,
	}
	_ = repos.ConnectionResource.Create(ctx, &res)

	conn := model.Connection{
		ResourceID: &res.ID, Name: "rdp-pool-node-a", Username: "alice",
		Protocol: contract.RDPProtocol, NodeID: "node-a", Port: 3389, Status: model.ConnectionStopped,
	}
	_ = repos.Connection.Create(ctx, &conn)

	if err := svc.StartConnection(ctx, conn.ID, "alice"); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.NodeID != "node-b" || got.Status != model.ConnectionRunning {
		t.Fatalf("got node=%s status=%s, want node-b running", got.NodeID, got.Status)
	}
}

func TestStartConnection_SSH_Rejected(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	alice := model.User{Username: "alice", Role: model.RoleNormal}
	_ = repos.User.Create(ctx, &alice)
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID: "node-a", DialHost: "10.0.0.1", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "linux",
	})
	port := uint(22)
	res := model.ConnectionResource{
		Name: "ssh-pool", Protocol: contract.SSHProtocol,
		Nodes: []string{"node-a"}, Port: &port, MaxPerUser: 1, Public: true,
	}
	_ = repos.ConnectionResource.Create(ctx, &res)
	conn := model.Connection{
		ResourceID: &res.ID, Name: "legacy-ssh", Username: "alice",
		Protocol: contract.SSHProtocol, NodeID: "node-a", Port: 22, Status: model.ConnectionStopped,
	}
	_ = repos.Connection.Create(ctx, &conn)

	err := svc.StartConnection(ctx, conn.ID, "alice")
	if err == nil || !strings.Contains(err.Error(), "GET /api/connections/ssh") {
		t.Fatalf("error = %v, want SSH start rejection pointing at /ssh", err)
	}
}
