package connection

import (
	"context"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestReconcileRDPSessions_RDPLiveKeepsRunning(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()
	conn := &model.Connection{
		Username: "alice",
		Protocol: contract.RDPProtocol,
		NodeID:   "win01",
		Port:     3389,
		Status:   model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	rdp := []contract.RDPSessionInfo{{Username: "alice", SessionID: 2, SessionName: "rdp-tcp#1"}}
	if err := svc.reconcileRDPSessions(ctx, "win01", rdp); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := repos.Connection.GetByID(ctx, conn.ID)
	if got.Status != model.ConnectionRunning {
		t.Fatalf("status=%v want running", got.Status)
	}
}

func TestReconcileRDPSessions_RDPWildAdoption(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "win02", AllowConnections: true, Status: model.NodeStatusOnline, OS: "windows"})

	rdp := []contract.RDPSessionInfo{{Username: "bob", SessionID: 3, SessionName: "rdp-tcp#2"}}
	if err := svc.reconcileRDPSessions(ctx, "win02", rdp); err != nil {
		t.Fatalf("reconcile adoption: %v", err)
	}

	conns, err := repos.Connection.ListByUser(ctx, "bob")
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected 1 adopted connection, got %d", len(conns))
	}
	adopted := conns[0]
	if adopted.Protocol != contract.RDPProtocol {
		t.Fatalf("protocol = %v, want RDP", adopted.Protocol)
	}
	if adopted.Status != model.ConnectionRunning {
		t.Fatalf("status = %v, want running", adopted.Status)
	}
	if adopted.NodeID != "win02" {
		t.Fatalf("nodeID = %v, want win02", adopted.NodeID)
	}
	if adopted.Port != 3389 {
		t.Fatalf("port = %d, want 3389", adopted.Port)
	}
}

func TestReconcileRDPSessions_LateResourceBinding(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "win01", AllowConnections: true, Status: model.NodeStatusOnline, OS: "windows"})
	alice := model.User{Username: "alice", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create user alice: %v", err)
	}

	rdp := []contract.RDPSessionInfo{{Username: "alice", SessionID: 2, SessionName: "rdp-tcp#1"}}
	if err := svc.reconcileRDPSessions(ctx, "win01", rdp); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}

	resRDP := model.ConnectionResource{
		Name:       "Win-Desktop",
		Protocol:   contract.RDPProtocol,
		Nodes:      []string{"win01"},
		MaxPerUser: 1,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resRDP); err != nil {
		t.Fatalf("create RDP resource: %v", err)
	}

	if err := svc.reconcileRDPSessions(ctx, "win01", rdp); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	conns, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil || len(conns) != 1 {
		t.Fatalf("expected 1 RDP connection, got %d (err=%v)", len(conns), err)
	}
	if conns[0].ResourceID == nil || *conns[0].ResourceID != resRDP.ID {
		t.Fatalf("expected late bound resource %d, got %v", resRDP.ID, conns[0].ResourceID)
	}
	if wantName := model.ConnectionName(resRDP.Name, "win01"); conns[0].Name != wantName {
		t.Fatalf("name = %q, want %q", conns[0].Name, wantName)
	}
}
