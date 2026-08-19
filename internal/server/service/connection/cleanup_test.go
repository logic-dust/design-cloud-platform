package connection

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

func newConnectionReposForCleanupTest(t *testing.T) *db.Repos {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "cleanup-test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db.NewRepos(store)
}

func TestConnectionCleanupDeletesStaleManagedConnections(t *testing.T) {
	t.Parallel()

	repos := newConnectionReposForCleanupTest(t)
	now := time.Now()

	managedStopped := model.Connection{
		Name:     "managed-stopped",
		Username: "alice",
		Status:   model.ConnectionStopped,
		Source:   model.ConnectionSourceDiscovered,
		Protocol: contract.VNCProtocol,
	}
	managedUnknown := model.Connection{
		Name:     "managed-unknown",
		Username: "alice",
		Status:   model.ConnectionUnknown,
		Source:   model.ConnectionSourceDiscovered,
		Protocol: contract.VNCProtocol,
	}
	manualStopped := model.Connection{
		Name:     "manual-stopped",
		Username: "alice",
		Status:   model.ConnectionStopped,
		Source:   model.ConnectionSourceManual,
		Protocol: contract.VNCProtocol,
	}
	for _, conn := range []*model.Connection{&managedStopped, &managedUnknown, &manualStopped} {
		if err := repos.Connection.Create(context.Background(), conn); err != nil {
			t.Fatalf("create connection %s: %v", conn.Name, err)
		}
	}

	svc := &Service{
		connectionRepo: repos.Connection,
		leaseRepo:      repos.TaskLease,
		ownerID:        "node-a",
		logger:         slog.Default(),
		connListCache:  testConnListCache(),
		now:            func() time.Time { return now.Add(10 * 24 * time.Hour) },
	}

	run, err := svc.ExecuteCleanup(context.Background())
	if err != nil {
		t.Fatalf("execute cleanup: %v", err)
	}
	if run.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", run.Deleted)
	}

	list, _, err := repos.Connection.List(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("connections after cleanup = %d, want 1", len(list))
	}
	if list[0].Name != "manual-stopped" {
		t.Fatalf("remaining connection = %s, want manual-stopped", list[0].Name)
	}
}

func TestConnectionCleanupSkipsWhenLeaseHeldElsewhere(t *testing.T) {
	t.Parallel()

	repos := newConnectionReposForCleanupTest(t)

	managedStopped := model.Connection{
		Name:     "managed-stopped",
		Username: "alice",
		Status:   model.ConnectionStopped,
		Source:   model.ConnectionSourceDiscovered,
		Protocol: contract.VNCProtocol,
	}
	if err := repos.Connection.Create(context.Background(), &managedStopped); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	acquired, err := repos.TaskLease.TryAcquire(context.Background(), "connection_cleanup", "node-b", time.Hour)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if !acquired {
		t.Fatalf("expected lease to be acquired by node-b")
	}

	svc := &Service{
		connectionRepo: repos.Connection,
		leaseRepo:      repos.TaskLease,
		ownerID:        "node-a",
		logger:         slog.Default(),
		connListCache:  testConnListCache(),
		now:            func() time.Time { return time.Now().Add(48 * time.Hour) },
	}

	_, err = svc.ExecuteCleanup(context.Background())
	if !errors.Is(err, ErrCleanupLeaseHeld) {
		t.Fatalf("err = %v, want ErrCleanupLeaseHeld", err)
	}

	list, _, err := repos.Connection.List(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("connections after skipped cleanup = %d, want 1", len(list))
	}
}

func TestConnectionCleanupExecute(t *testing.T) {
	t.Parallel()

	repos := newConnectionReposForCleanupTest(t)
	managedStopped := model.Connection{
		Name:     "managed-execute",
		Username: "alice",
		Status:   model.ConnectionStopped,
		Source:   model.ConnectionSourceDiscovered,
		Protocol: contract.VNCProtocol,
	}
	if err := repos.Connection.Create(context.Background(), &managedStopped); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	now := time.Now()
	svc := &Service{
		connectionRepo: repos.Connection,
		leaseRepo:      repos.TaskLease,
		ownerID:        "node-a",
		logger:         slog.Default(),
		connListCache:  testConnListCache(),
		now:            func() time.Time { return now.Add(10 * 24 * time.Hour) },
	}

	run, err := svc.ExecuteCleanup(context.Background())
	if err != nil {
		t.Fatalf("execute cleanup: %v", err)
	}
	if run.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", run.Deleted)
	}

	list, _, err := repos.Connection.List(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("connections after execute = %d, want 0", len(list))
	}
}
