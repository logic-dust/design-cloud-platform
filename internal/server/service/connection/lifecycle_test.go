package connection

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

func TestStartConnection_StoppedUsesResourceAndNotifiesOnFailure(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	_ = repos.Node.Create(ctx, &model.Node{
		NodeID:           "node01",
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
		OS:               "linux",
	})

	alice := model.User{Username: "alice", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	cfgJSON := `{"startOptions":"-geometry 1600x900"}`
	res1 := model.ConnectionResource{
		Name:       "Res-Fixed1",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node01"},
		Display:    new(1),
		MaxPerUser: 1,
		Public:     true,
		VNCConfig:  json.RawMessage(cfgJSON),
	}
	if err := repos.ConnectionResource.Create(ctx, &res1); err != nil {
		t.Fatalf("create res1: %v", err)
	}

	conn := model.Connection{
		ResourceID: &res1.ID,
		Username:   "alice",
		Protocol:   contract.VNCProtocol,
		NodeID:     "node01",
		Port:       42,
		Status:     model.ConnectionStopped,
		Name:       model.ConnectionName(res1.Name, "node01"),
	}
	if err := repos.Connection.Create(ctx, &conn); err != nil {
		t.Fatalf("create conn: %v", err)
	}

	pub := &recordingPublisher{}
	svc.publisher = pub

	err := svc.StartConnection(ctx, conn.ID, "alice")
	if err == nil {
		t.Fatalf("expected launch error because mockExecutor didn't return listen port")
	}
	if len(pub.msgs) != 0 {
		t.Fatalf("self-service start must not push; got %d messages", len(pub.msgs))
	}

	svc.execSvc = &customMockExecutor{res: `{"display":1,"port":5901,"pid":9999}`}
	if err := svc.StartConnection(ctx, conn.ID, "alice"); err != nil {
		t.Fatalf("start connection with valid mock failed: %v", err)
	}
	runningConn, _ := repos.Connection.GetByID(ctx, conn.ID)
	if runningConn.Status != model.ConnectionRunning {
		t.Fatalf("expected status Running, got %v", runningConn.Status)
	}
	if runningConn.Display == nil || *runningConn.Display != 1 {
		t.Fatalf("expected display 1, got %v", runningConn.Display)
	}
	if runningConn.PID == nil || *runningConn.PID != 9999 {
		t.Fatalf("expected pid 9999, got %v", runningConn.PID)
	}
}

type customMockExecutor struct {
	res string
}

func (m *customMockExecutor) Execute(_ context.Context, _ *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return &execution.ExecuteResponse{
		Success: true,
		Stdout:  m.res,
	}, nil
}
