package connection

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

type recordingSessionExecutor struct {
	mu       sync.Mutex
	requests []*execution.ExecuteRequest
	fail     bool
}

func (e *recordingSessionExecutor) Execute(_ context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := *req
	e.requests = append(e.requests, &cp)
	if e.fail {
		return nil, errors.New("session stop failed")
	}
	return &execution.ExecuteResponse{Success: true, Stdout: `{"terminated":1}`}, nil
}

func newSessionStopService(t *testing.T, exec execution.Executor) (*Service, *db.Repos) {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "session-stop.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(store)
	return &Service{
		connectionRepo: repos.Connection,
		execSvc:        exec,
		logger:         slog.Default(),
	}, repos
}

func TestStopConnection_SSHDispatchesSessionControl(t *testing.T) {
	exec := &recordingSessionExecutor{}
	svc, repos := newSessionStopService(t, exec)
	ctx := context.Background()
	resID := uint(9)
	conn := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Protocol:   contract.SSHProtocol,
		NodeID:     "node01",
		Port:       22,
		Status:     model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.StopConnection(ctx, conn.ID, "alice"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(exec.requests) != 1 || exec.requests[0].ActionName != execution.ActionSessionControl {
		t.Fatalf("requests = %+v", exec.requests)
	}
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ConnectionStopped {
		t.Fatalf("status = %v, want stopped", got.Status)
	}
	if got.ResourceID == nil || *got.ResourceID != resID {
		t.Fatalf("resourceId should be kept, got %v", got.ResourceID)
	}
}

func TestStopConnection_RDPKillFailureMarksTerminatingKeepResource(t *testing.T) {
	exec := &recordingSessionExecutor{fail: true}
	svc, repos := newSessionStopService(t, exec)
	ctx := context.Background()
	resID := uint(3)
	conn := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Protocol:   contract.RDPProtocol,
		NodeID:     "win01",
		Port:       3389,
		Status:     model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.StopConnection(ctx, conn.ID, "alice"); err == nil {
		t.Fatal("stop should surface kill failure after marking terminating")
	}
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ConnectionTerminating {
		t.Fatalf("status = %v, want terminating", got.Status)
	}
	if got.ResourceID == nil || *got.ResourceID != resID {
		t.Fatalf("stop failure must keep resourceId, got %v", got.ResourceID)
	}
}

func TestStopConnection_RDPTeardownOnStop(t *testing.T) {
	exec := &recordingSessionExecutor{}
	svc, repos := newSessionStopService(t, exec)
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
	if err := svc.StopConnection(ctx, conn.ID, "alice"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(exec.requests) != 1 || exec.requests[0].ActionName != execution.ActionSessionControl {
		t.Fatalf("stop must tear down session, requests=%+v", exec.requests)
	}
	got, _ := repos.Connection.GetByID(ctx, conn.ID)
	if got.Status != model.ConnectionStopped {
		t.Fatalf("status = %v, want stopped", got.Status)
	}
}

func TestReuseDial_TerminatingRetriesTeardown(t *testing.T) {
	exec := &recordingSessionExecutor{}
	svc, repos := newSessionStopService(t, exec)
	ctx := context.Background()
	res := model.ConnectionResource{
		Name: "ssh-pool", Protocol: contract.SSHProtocol, Nodes: []string{"node01"}, Public: true, MaxPerUser: 1,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("resource: %v", err)
	}
	_ = repos.Node.Create(ctx, &model.Node{NodeID: "node01", AllowConnections: true, Status: model.NodeStatusOnline, OS: "linux"})
	svc.connectionResourceRepo = repos.ConnectionResource
	svc.placer = NewPlacer(repos.Node, repos.Connection)
	resID := res.ID
	conn := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Protocol:   contract.SSHProtocol,
		NodeID:     "node01",
		Port:       22,
		Status:     model.ConnectionTerminating,
		Name:       "ssh-pool-node01",
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.reuseDialConnection(ctx, *conn, res, "alice")
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if len(exec.requests) != 1 || exec.requests[0].ActionName != execution.ActionSessionControl {
		t.Fatalf("expected teardown retry, got %+v", exec.requests)
	}
	if got.Status != model.ConnectionRunning {
		t.Fatalf("status = %v, want running after resolve+place", got.Status)
	}
}

func TestPurgeUserConnections_IncludesTerminating(t *testing.T) {
	exec := &recordingSessionExecutor{}
	svc, repos := newSessionStopService(t, exec)
	ctx := context.Background()
	conn := &model.Connection{
		Username: "alice",
		Protocol: contract.SSHProtocol,
		NodeID:   "node01",
		Port:     22,
		Status:   model.ConnectionTerminating,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.PurgeUserConnections(ctx, "alice"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := repos.Connection.GetByID(ctx, conn.ID); err == nil {
		t.Fatal("terminating row should be purged")
	}
	if len(exec.requests) != 1 || exec.requests[0].ActionName != execution.ActionSessionControl {
		t.Fatalf("purge must teardown terminating, requests=%+v", exec.requests)
	}
}

func TestCleanup_RetriesDialTerminating(t *testing.T) {
	exec := &recordingSessionExecutor{}
	svc, repos := newSessionStopService(t, exec)
	ctx := context.Background()
	svc.leaseRepo = repos.TaskLease
	svc.ownerID = "cleanup-owner"
	svc.nodeRepo = repos.Node
	resID := uint(1)
	conn := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Protocol:   contract.RDPProtocol,
		NodeID:     "win01",
		Port:       3389,
		Status:     model.ConnectionTerminating,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	run, err := svc.ExecuteCleanup(ctx)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if run.Updated < 1 {
		t.Fatalf("updated = %d, want >=1 terminating resolve", run.Updated)
	}
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ConnectionStopped {
		t.Fatalf("status = %v, want stopped", got.Status)
	}
}

func TestDelete_SSHKillFailureMarksTerminatingClearResource(t *testing.T) {
	exec := &recordingSessionExecutor{fail: true}
	svc, repos := newSessionStopService(t, exec)
	ctx := context.Background()
	resID := uint(4)
	conn := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Protocol:   contract.SSHProtocol,
		NodeID:     "node01",
		Port:       22,
		Status:     model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	delRes, err := svc.Delete(ctx, "alice", conn.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if delRes.TerminatingCount != 1 {
		t.Fatalf("TerminatingCount = %d, want 1", delRes.TerminatingCount)
	}
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("row should remain: %v", err)
	}
	if got.Status != model.ConnectionTerminating {
		t.Fatalf("status = %v, want terminating", got.Status)
	}
	if got.ResourceID != nil {
		t.Fatalf("delete failure must clear resourceId")
	}
}
