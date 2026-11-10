package connection

import (
	"context"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestResolveSSHDialTarget_SelectsNodeWithoutConnectionRow(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	bob := model.User{Username: "bob", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &bob); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repos.Node.Create(ctx, &model.Node{
		NodeID: "node-ssh", DialHost: "10.0.0.9", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "linux",
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	port := uint(2222)
	res := model.ConnectionResource{
		Name: "ssh-login", Protocol: contract.SSHProtocol,
		Nodes: []string{"node-ssh"}, Port: &port, MaxPerUser: 1, Public: true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	host, gotPort, err := svc.ResolveSSHDialTarget(ctx, "bob", res.ID)
	if err != nil {
		t.Fatalf("ResolveSSHDialTarget: %v", err)
	}
	if host != "10.0.0.9" || gotPort != 2222 {
		t.Fatalf("got %s:%d, want 10.0.0.9:2222", host, gotPort)
	}
	rows, err := repos.Connection.ListByUsernameAndResource(ctx, "bob", res.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no connection rows, got %d", len(rows))
	}
}

func TestResolveSSHDialTarget_RejectsNonSSH(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	bob := model.User{Username: "bob", Role: model.RoleNormal}
	_ = repos.User.Create(ctx, &bob)
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID: "node-1", DialHost: "10.0.0.1", Status: model.NodeStatusOnline,
		AllowConnections: true, OS: "linux",
	})
	res := model.ConnectionResource{
		Name: "vnc", Protocol: contract.VNCProtocol,
		Nodes: []string{"node-1"}, MaxPerUser: 1, Public: true,
	}
	_ = repos.ConnectionResource.Create(ctx, &res)

	_, _, err := svc.ResolveSSHDialTarget(ctx, "bob", res.ID)
	if err == nil {
		t.Fatal("expected non-SSH resource to fail")
	}
}
