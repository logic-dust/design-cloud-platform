package connection

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
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

func TestConnectionCleanupSkipsOfflineNodesWithoutWritingUnknown(t *testing.T) {
	t.Parallel()

	repos := newConnectionReposForCleanupTest(t)

	node := model.Node{
		NodeID: "node-online",
		Status: model.NodeStatusOnline,
	}
	if err := repos.Node.Create(context.Background(), &node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	onlinePID := uint(11)
	offlinePID := uint(22)
	runningVNC := model.Connection{
		Username: "alice",
		NodeID:   "node-online",
		Status:   model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
		PID:      &onlinePID,
	}
	offlineVNC := model.Connection{
		Username: "bob",
		NodeID:   "node-offline",
		Status:   model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
		PID:      &offlinePID,
	}
	if err := repos.Connection.Create(context.Background(), &runningVNC); err != nil {
		t.Fatalf("create running vnc: %v", err)
	}
	if err := repos.Connection.Create(context.Background(), &offlineVNC); err != nil {
		t.Fatalf("create offline vnc: %v", err)
	}

	svc := &Service{
		connectionRepo: repos.Connection,
		nodeRepo:       repos.Node,
		leaseRepo:      repos.TaskLease,
		ownerID:        "node-a",
		logger:         slog.Default(),
		// execSvc nil → online node check skipped without error
	}

	run, err := svc.ExecuteCleanup(context.Background())
	if err != nil {
		t.Fatalf("execute cleanup: %v", err)
	}
	if run.Updated != 0 {
		t.Fatalf("updated = %d, want 0 (no exec, no offline writes)", run.Updated)
	}

	c2, err := repos.Connection.GetByID(context.Background(), offlineVNC.ID)
	if err != nil {
		t.Fatalf("get c2: %v", err)
	}
	if c2.Status != model.ConnectionRunning {
		t.Fatalf("c2 status = %v, want running (offline no longer written to unknown)", c2.Status)
	}
}

func TestConnectionCleanupSkipsWhenLeaseHeldElsewhere(t *testing.T) {
	t.Parallel()

	repos := newConnectionReposForCleanupTest(t)
	ctx := context.Background()
	ok, _, err := repos.TaskLease.TryAcquire(ctx, "connection_cleanup", "other-owner", time.Minute)
	if err != nil || !ok {
		t.Fatalf("seed lease: ok=%v err=%v", ok, err)
	}

	svc := &Service{
		connectionRepo: repos.Connection,
		nodeRepo:       repos.Node,
		leaseRepo:      repos.TaskLease,
		ownerID:        "node-a",
		logger:         slog.Default(),
	}
	_, err = svc.ExecuteCleanup(context.Background())
	if !errors.Is(err, ErrCleanupLeaseHeld) {
		t.Fatalf("err = %v, want ErrCleanupLeaseHeld", err)
	}
}
