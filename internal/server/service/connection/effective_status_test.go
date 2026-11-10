package connection

import (
	"context"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestWithEffectiveStatusesOverlaysNodeState(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{NodeID: "n-off", Status: model.NodeStatusOffline})
	_ = repos.Node.Create(ctx, &model.Node{NodeID: "n-maint", Status: model.NodeStatusMaintenance})
	_ = repos.Node.Create(ctx, &model.Node{NodeID: "n-on", Status: model.NodeStatusOnline})

	conns := []model.Connection{
		{ID: 1, NodeID: "n-off", Status: model.ConnectionRunning, Protocol: contract.VNCProtocol},
		{ID: 2, NodeID: "n-maint", Status: model.ConnectionRunning, Protocol: contract.VNCProtocol},
		{ID: 3, NodeID: "n-on", Status: model.ConnectionRunning, Protocol: contract.VNCProtocol},
		{ID: 4, NodeID: "n-off", Status: model.ConnectionStopped, Protocol: contract.VNCProtocol},
	}
	out, err := svc.withEffectiveStatuses(ctx, conns)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Status != model.ConnectionUnknown {
		t.Fatalf("offline running → %v, want unknown", out[0].Status)
	}
	if out[1].Status != model.ConnectionMaintenance {
		t.Fatalf("maintenance → %v, want maintenance", out[1].Status)
	}
	if out[2].Status != model.ConnectionRunning {
		t.Fatalf("online → %v, want running", out[2].Status)
	}
	if out[3].Status != model.ConnectionStopped {
		t.Fatalf("offline stopped stays %v", out[3].Status)
	}
}
