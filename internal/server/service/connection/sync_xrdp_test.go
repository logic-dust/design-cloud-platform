package connection

import (
	"context"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestSyncConnectionReport_XRDPSeatAttachAndClear(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", AllowConnections: true, Status: model.NodeStatusOnline, OS: "linux"})

	conn := &model.Connection{
		Name:     "xrdp-alice",
		Username: "alice",
		Protocol: contract.XRDPProtocol,
		NodeID:   "node01",
		Port:     3389,
		Status:   model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}

	disp := 10
	pid := uint(5555)
	report := contract.ConnectionReport{
		XRDPSeats: []contract.XRDPSeatInfo{{
			Username: "alice",
			Display:  &disp,
			PID:      pid,
		}},
	}
	if err := svc.SyncConnectionReport(ctx, "node01", report); err != nil {
		t.Fatalf("sync with seat: %v", err)
	}
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ConnectionRunning || got.Display == nil || *got.Display != 10 || got.PID == nil || *got.PID != pid {
		t.Fatalf("after seat attach got %+v", got)
	}
	if got.Port != 3389 {
		t.Fatalf("port = %d, want 3389", got.Port)
	}

	if err := svc.SyncConnectionReport(ctx, "node01", contract.ConnectionReport{}); err != nil {
		t.Fatalf("sync without seat: %v", err)
	}
	got, err = repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got.Status != model.ConnectionStopped {
		t.Fatalf("status after seat gone = %s, want stopped", got.Status)
	}
	if got.Display != nil || got.PID != nil {
		t.Fatalf("display/pid should clear, got display=%v pid=%v", got.Display, got.PID)
	}
	if got.Port != 3389 {
		t.Fatalf("port changed to %d", got.Port)
	}
}

func TestSyncConnectionReport_XRDPWildAdoption(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node02", AllowConnections: true, Status: model.NodeStatusOnline, OS: "linux"})

	disp := 12
	pid := uint(8888)
	report := contract.ConnectionReport{
		XRDPSeats: []contract.XRDPSeatInfo{{
			Username: "bob",
			Display:  &disp,
			PID:      pid,
		}},
	}

	if err := svc.SyncConnectionReport(ctx, "node02", report); err != nil {
		t.Fatalf("sync adoption: %v", err)
	}

	conns, err := repos.Connection.ListByUser(ctx, "bob")
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected 1 adopted connection, got %d", len(conns))
	}
	adopted := conns[0]
	if adopted.Protocol != contract.XRDPProtocol {
		t.Fatalf("protocol = %v, want XRDP", adopted.Protocol)
	}
	if adopted.Status != model.ConnectionRunning {
		t.Fatalf("status = %v, want running", adopted.Status)
	}
	if adopted.Display == nil || *adopted.Display != disp {
		t.Fatalf("display = %v, want %d", adopted.Display, disp)
	}
	if adopted.PID == nil || *adopted.PID != pid {
		t.Fatalf("pid = %v, want %d", adopted.PID, pid)
	}
}

func TestSyncConnectionReport_XRDPMultiSeatAndLateResourceBinding(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", AllowConnections: true, Status: model.NodeStatusOnline, OS: "linux"})
	alice := model.User{Username: "alice", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create user alice: %v", err)
	}

	// Report two seats for the same user on the same node (port 3389 is unique per node+user):
	// the first seat (:10) is adopted, and the second seat (:11) must not overwrite :10 or fail
	// with a duplicate key error.
	disp10 := 10
	disp11 := 11
	report := contract.ConnectionReport{
		XRDPSeats: []contract.XRDPSeatInfo{
			{Username: "alice", Display: &disp10, PID: 1010},
			{Username: "alice", Display: &disp11, PID: 1011},
		},
	}

	if err := svc.SyncConnectionReport(ctx, "node01", report); err != nil {
		t.Fatalf("initial multi-seat sync: %v", err)
	}

	conns, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected 1 XRDP connection for alice on node01 (unique port 3389), got %d", len(conns))
	}
	if conns[0].Display == nil || *conns[0].Display != 10 || conns[0].PID == nil || *conns[0].PID != 1010 {
		t.Fatalf("expected first seat (:10, pid 1010) to be preserved without overwrite by :11, got display=%v pid=%v", conns[0].Display, conns[0].PID)
	}

	// Create an XRDP resource covering node01 and verify late resource binding on next sync
	resXRDP := model.ConnectionResource{
		Name:       "XRDP-Pool",
		Protocol:   contract.XRDPProtocol,
		Nodes:      []string{"node01"},
		MaxPerUser: 2,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &resXRDP); err != nil {
		t.Fatalf("create XRDP resource: %v", err)
	}

	if err := svc.SyncConnectionReport(ctx, "node01", report); err != nil {
		t.Fatalf("second sync for late resource binding: %v", err)
	}

	connsAfter, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil || len(connsAfter) != 1 {
		t.Fatalf("expected 1 connection after second sync, got %d (err=%v)", len(connsAfter), err)
	}
	if connsAfter[0].ResourceID == nil || *connsAfter[0].ResourceID != resXRDP.ID {
		t.Fatalf("expected XRDP seat on display %v to bind resource %d, got %v", connsAfter[0].Display, resXRDP.ID, connsAfter[0].ResourceID)
	}
	if connsAfter[0].Display == nil || *connsAfter[0].Display != 10 {
		t.Fatalf("expected display :10 preserved, got %v", connsAfter[0].Display)
	}
}
