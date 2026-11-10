package connection

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

type failingMockExecutor struct{}

func (failingMockExecutor) Execute(context.Context, *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return nil, errors.New("kill unavailable")
}

func TestStopConnection_XRDPKillFailureMarksTerminating(t *testing.T) {
	tmp := t.TempDir()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tmp, "xrdp-stop-test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(store)
	svc := &Service{
		connectionRepo: repos.Connection,
		execSvc:        failingMockExecutor{},
		logger:         slog.Default(),
	}
	ctx := context.Background()
	disp := 10
	pid := uint(1234)
	conn := &model.Connection{
		Username: "alice",
		Protocol: contract.XRDPProtocol,
		NodeID:   "node01",
		Port:     3389,
		Status:   model.ConnectionRunning,
		Display:  &disp,
		PID:      &pid,
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
	if got.Display == nil || *got.Display != 10 {
		t.Fatalf("display should remain 10 for tombstone, got %v", got.Display)
	}
}

func TestReconcileTerminatingXRDP_StopTombstoneSeatGoneMarksStopped(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()
	disp := 11
	resID := uint(7)
	conn := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Protocol:   contract.XRDPProtocol,
		NodeID:     "node01",
		Port:       3389,
		Status:     model.ConnectionTerminating,
		Display:    &disp,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	owners := make(map[string]struct{})
	svc.reconcileTerminatingXRDP(ctx, "node01", *conn, nil, owners)
	got, err := repos.Connection.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ConnectionStopped {
		t.Fatalf("status = %v, want stopped", got.Status)
	}
	if got.Display != nil || got.PID != nil {
		t.Fatalf("display/pid should be cleared")
	}
}

func TestReconcileTerminatingXRDP_DeleteTombstoneSeatGoneDeletesRow(t *testing.T) {
	svc, repos := newTestConnectionService(t)
	ctx := context.Background()
	disp := 12
	conn := &model.Connection{
		Username: "alice",
		Protocol: contract.XRDPProtocol,
		NodeID:   "node01",
		Port:     3389,
		Status:   model.ConnectionTerminating,
		Display:  &disp,
	}
	if err := repos.Connection.Create(ctx, conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	owners := make(map[string]struct{})
	svc.reconcileTerminatingXRDP(ctx, "node01", *conn, nil, owners)
	if _, err := repos.Connection.GetByID(ctx, conn.ID); err == nil {
		t.Fatalf("row should be deleted")
	}
}
