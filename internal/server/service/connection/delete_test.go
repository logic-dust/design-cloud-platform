package connection

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

func newTestDeleteService(t *testing.T) (*Service, *db.Repos) {
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
		connListCache:  testConnListCache(),
	}
	return svc, repos
}

func TestDeleteByID_Permissions(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	ctx := context.Background()

	// 1. Create a private manual connection owned by alice
	connAlice := model.Connection{
		Name:     "manual-alice",
		Username: "alice",
		Source:   model.ConnectionSourceManual,
		Protocol: contract.VNCProtocol,
		Host:     "192.168.1.10",
		Port:     5901,
	}
	if err := repos.Connection.Create(ctx, &connAlice); err != nil {
		t.Fatalf("create alice connection: %v", err)
	}

	// 2. Create a fixed connection
	connFixed := model.Connection{
		Name:     "fixed-vnc",
		Username: "alice",
		Source:   model.ConnectionSourceFixed,
		Protocol: contract.VNCProtocol,
		Host:     "192.168.1.11",
		Port:     5902,
	}
	if err := repos.Connection.Create(ctx, &connFixed); err != nil {
		t.Fatalf("create fixed connection: %v", err)
	}

	// Non-owner bob cannot delete alice's private connection
	err := svc.Delete(ctx, "bob", connAlice.ID)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("expected ErrPermission when bob deletes alice's connection, got: %v", err)
	}

	// Alice cannot delete fixed connection
	err = svc.Delete(ctx, "alice", connFixed.ID)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("expected ErrPermission when non-admin deletes fixed connection, got: %v", err)
	}

	// Alice can delete her own manual connection
	if err := svc.Delete(ctx, "alice", connAlice.ID); err != nil {
		t.Fatalf("alice delete own connection failed: %v", err)
	}
	if _, err := repos.Connection.GetByID(ctx, connAlice.ID); err == nil {
		t.Errorf("expected connection %d to be deleted from DB", connAlice.ID)
	}

	// Admin (AllUsers) can delete fixed connection
	if err := svc.Delete(ctx, AllUsers, connFixed.ID); err != nil {
		t.Fatalf("admin delete fixed connection failed: %v", err)
	}
	if _, err := repos.Connection.GetByID(ctx, connFixed.ID); err == nil {
		t.Errorf("expected fixed connection %d to be deleted from DB", connFixed.ID)
	}
}

func TestDelete_BulkHandling(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	ctx := context.Background()

	conn1 := model.Connection{Name: "c1", Username: "alice", Source: model.ConnectionSourceManual, Protocol: contract.VNCProtocol, Host: "10.0.0.1", Port: 5901}
	conn2 := model.Connection{Name: "c2", Username: "alice", Source: model.ConnectionSourceManual, Protocol: contract.VNCProtocol, Host: "10.0.0.2", Port: 5902}
	conn3 := model.Connection{Name: "c3", Username: "bob", Source: model.ConnectionSourceManual, Protocol: contract.VNCProtocol, Host: "10.0.0.3", Port: 5903}

	_ = repos.Connection.Create(ctx, &conn1)
	_ = repos.Connection.Create(ctx, &conn2)
	_ = repos.Connection.Create(ctx, &conn3)

	// Alice attempts to delete c1 and c3 (bob's) -> should fail with permission error
	err := svc.Delete(ctx, "alice", conn1.ID, conn3.ID)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("expected ErrPermission when deleting another user's connection in batch, got: %v", err)
	}

	// Alice deletes c1, c2, and a non-existent 9999
	if err := svc.Delete(ctx, "alice", conn1.ID, conn2.ID, 9999); err != nil {
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

type fakeTunnelCloser struct {
	closedIDs []uint
}

func (f *fakeTunnelCloser) CloseByConnectionID(id uint) error {
	f.closedIDs = append(f.closedIDs, id)
	return nil
}

func TestDelete_ClosesTunnel(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	ctx := context.Background()

	closer := &fakeTunnelCloser{}
	svc.SetTunnelCloser(closer)

	conn := model.Connection{Name: "c1", Username: "alice", Source: model.ConnectionSourceManual, Protocol: contract.VNCProtocol, Host: "10.0.0.1", Port: 5901}
	if err := repos.Connection.Create(ctx, &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	if err := svc.Delete(ctx, "alice", conn.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if len(closer.closedIDs) != 1 || closer.closedIDs[0] != conn.ID {
		t.Errorf("expected tunnel closer to be called with %d, got: %v", conn.ID, closer.closedIDs)
	}
}

func TestDelete_PreFlightAtomicity(t *testing.T) {
	svc, repos := newTestDeleteService(t)
	ctx := context.Background()

	closer := &fakeTunnelCloser{}
	svc.SetTunnelCloser(closer)

	conn1 := model.Connection{Name: "c1", Username: "alice", Source: model.ConnectionSourceManual, Protocol: contract.VNCProtocol, Host: "10.0.0.1", Port: 5901}
	conn2 := model.Connection{Name: "c2", Username: "bob", Source: model.ConnectionSourceManual, Protocol: contract.VNCProtocol, Host: "10.0.0.2", Port: 5902}
	_ = repos.Connection.Create(ctx, &conn1)
	_ = repos.Connection.Create(ctx, &conn2)

	// Alice tries to delete both conn1 (hers) and conn2 (bob's)
	err := svc.Delete(ctx, "alice", conn1.ID, conn2.ID)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("expected ErrPermission, got: %v", err)
	}

	// Pre-flight must have aborted BEFORE closing conn1's tunnel or deleting conn1
	if len(closer.closedIDs) > 0 {
		t.Errorf("expected no tunnel closures on pre-flight failure, got: %v", closer.closedIDs)
	}
	if _, err := repos.Connection.GetByID(ctx, conn1.ID); err != nil {
		t.Errorf("expected conn1 to still exist in DB, got: %v", err)
	}
}
