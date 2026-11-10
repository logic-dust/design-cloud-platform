package connection

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func newTestDeleteService(t *testing.T, opts ...Option) (*Service, *db.Repos) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "conn-delete-test.db"),
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
		logger:         slog.Default(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, repos
}

func TestDeleteByID_Permissions(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	ctx := context.Background()

	// 1. Create a connection owned by alice
	connAlice := model.Connection{
		Username: "alice",
		Protocol: contract.VNCProtocol,
		Port:     5901,
	}
	if err := repos.Connection.Create(ctx, &connAlice); err != nil {
		t.Fatalf("create alice connection: %v", err)
	}

	// Non-owner bob cannot delete alice's connection
	_, err := svc.Delete(ctx, "bob", connAlice.ID)
	var contractErr contract.DomainError
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when bob deletes alice's connection, got: %v", err)
	}

	// Alice can delete her own connection
	if _, err := svc.Delete(ctx, "alice", connAlice.ID); err != nil {
		t.Fatalf("alice delete own connection failed: %v", err)
	}
	if _, err := repos.Connection.GetByID(ctx, connAlice.ID); err == nil {
		t.Errorf("expected connection %d to be deleted from DB", connAlice.ID)
	}
}

func TestDelete_BulkHandling(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	ctx := context.Background()

	conn1 := model.Connection{Username: "alice", Protocol: contract.VNCProtocol, Port: 5901}
	conn2 := model.Connection{Username: "alice", Protocol: contract.VNCProtocol, Port: 5902}
	conn3 := model.Connection{Username: "bob", Protocol: contract.VNCProtocol, Port: 5903}

	_ = repos.Connection.Create(ctx, &conn1)
	_ = repos.Connection.Create(ctx, &conn2)
	_ = repos.Connection.Create(ctx, &conn3)

	// Alice attempts to delete c1 and c3 (bob's) -> should fail with permission error
	_, err := svc.Delete(ctx, "alice", conn1.ID, conn3.ID)
	var contractErr contract.DomainError
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when deleting another user's connection in batch, got: %v", err)
	}

	// Alice deletes c1, c2, and a non-existent 9999
	if _, err := svc.Delete(ctx, "alice", conn1.ID, conn2.ID, 9999); err != nil {
		t.Fatalf("expected Delete to succeed, got err: %v", err)
	}

	// c1 and c2 should be deleted
	if _, err := repos.Connection.GetByID(ctx, conn1.ID); err == nil {
		t.Errorf("expected conn1 to be deleted")
	}
	if _, err := repos.Connection.GetByID(ctx, conn2.ID); err == nil {
		t.Errorf("expected conn2 to be deleted")
	}

	// c3 should remain
	if _, err := repos.Connection.GetByID(ctx, conn3.ID); err != nil {
		t.Errorf("expected conn3 to remain, got err: %v", err)
	}
}

type fakeProxyConnectionCloser struct {
	closedIDs []uint
}

func (f *fakeProxyConnectionCloser) CloseAllByConnectionID(id uint) error {
	f.closedIDs = append(f.closedIDs, id)
	return nil
}

func TestDelete_ClosesProxyConnection(t *testing.T) {
	closer := &fakeProxyConnectionCloser{}
	svc, repos := newTestDeleteService(t, WithProxyConnectionCloser(closer))
	ctx := context.Background()

	conn := model.Connection{Username: "alice", Protocol: contract.VNCProtocol, Port: 5901}
	if err := repos.Connection.Create(ctx, &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	if _, err := svc.Delete(ctx, "alice", conn.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if len(closer.closedIDs) != 1 || closer.closedIDs[0] != conn.ID {
		t.Errorf("expected proxy session closer to be called with %d, got: %v", conn.ID, closer.closedIDs)
	}
}

type failingKillExecutor struct{}

func (failingKillExecutor) Execute(context.Context, *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return nil, errors.New("agent unreachable")
}

func TestDelete_KillFailureMarksTerminating(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	svc.execSvc = failingKillExecutor{}
	ctx := context.Background()

	disp := 3
	res := model.ConnectionResource{
		Name: "r1", Protocol: contract.VNCProtocol, Nodes: []string{"node-a"}, Public: true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	conn := model.Connection{
		ResourceID: &res.ID,
		Username:   "alice",
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-a",
		Port:       5903,
		Display:    &disp,
		Status:     model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, &conn); err != nil {
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
		t.Fatalf("row should remain as terminating: %v", err)
	}
	if got.Status != model.ConnectionTerminating {
		t.Fatalf("status = %v, want terminating", got.Status)
	}
	if got.ResourceID != nil {
		t.Fatalf("resource_id = %v, want nil", got.ResourceID)
	}
	list, err := svc.ListByUser(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == conn.ID {
			t.Fatalf("terminating row must not appear in user list")
		}
	}
}

func TestDelete_PreFlightAtomicity(t *testing.T) {
	closer := &fakeProxyConnectionCloser{}
	svc, repos := newTestDeleteService(t, WithProxyConnectionCloser(closer))
	ctx := context.Background()

	conn1 := model.Connection{Username: "alice", Protocol: contract.VNCProtocol, Port: 5901}
	conn2 := model.Connection{Username: "bob", Protocol: contract.VNCProtocol, Port: 5902}
	_ = repos.Connection.Create(ctx, &conn1)
	_ = repos.Connection.Create(ctx, &conn2)

	// Alice tries to delete both conn1 (hers) and conn2 (bob's)
	_, err := svc.Delete(ctx, "alice", conn1.ID, conn2.ID)
	var contractErr contract.DomainError
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got: %v", err)
	}

	// Pre-flight must have aborted BEFORE closing conn1's proxy session or deleting conn1
	if len(closer.closedIDs) > 0 {
		t.Errorf("expected no proxy session closures on pre-flight failure, got: %v", closer.closedIDs)
	}
	if _, err := repos.Connection.GetByID(ctx, conn1.ID); err != nil {
		t.Errorf("expected conn1 to still exist in DB, got: %v", err)
	}
}
