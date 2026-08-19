package db

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
)

func newConnRepoForTest(t *testing.T) (*Store, *ConnectionRepo) {
	t.Helper()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
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
	return store, NewRepos(store).Connection
}

func TestConnectionRepo_CreateWithQuota_Sequential(t *testing.T) {
	t.Parallel()

	_, repo := newConnRepoForTest(t)
	ctx := context.Background()

	username := "alice"
	quota := 2

	// 1. Insert 1st connection within quota -> OK
	c1 := &model.Connection{
		Username: username,
		Host:     "192.168.1.10",
		Port:     5900,
		Protocol: "VNC",
		Shared:   false,
	}
	if err := repo.CreateWithQuota(ctx, c1, quota); err != nil {
		t.Fatalf("unexpected error on 1st insert: %v", err)
	}

	// 2. Insert 2nd connection within quota -> OK
	c2 := &model.Connection{
		Username: username,
		Host:     "192.168.1.10",
		Port:     5901,
		Protocol: "VNC",
		Shared:   false,
	}
	if err := repo.CreateWithQuota(ctx, c2, quota); err != nil {
		t.Fatalf("unexpected error on 2nd insert: %v", err)
	}

	// 3. Insert 3rd connection exceeding quota -> QuotaExceededError (429)
	c3 := &model.Connection{
		Username: username,
		Host:     "192.168.1.10",
		Port:     5902,
		Protocol: "VNC",
		Shared:   false,
	}
	err := repo.CreateWithQuota(ctx, c3, quota)
	if err == nil {
		t.Fatalf("expected error on 3rd insert exceeding quota, got nil")
	}
	var appErr interface{ HTTPStatus() int }
	if errors.As(err, &appErr) {
		if appErr.HTTPStatus() != http.StatusTooManyRequests {
			t.Errorf("expected HTTP 429 StatusTooManyRequests, got %d", appErr.HTTPStatus())
		}
	} else {
		t.Errorf("expected appError with HTTPStatus(), got: %v", err)
	}

	// 4. Shared connection does not count against quota and succeeds even when quota is full
	cShared := &model.Connection{
		Username: username,
		Host:     "192.168.1.10",
		Port:     5903,
		Protocol: "VNC",
		Shared:   true,
	}
	if err := repo.CreateWithQuota(ctx, cShared, quota); err != nil {
		t.Fatalf("unexpected error inserting shared connection: %v", err)
	}

	// 5. Connection with quota <= 0 (unlimited) succeeds
	cUnlimited := &model.Connection{
		Username: username,
		Host:     "192.168.1.10",
		Port:     5904,
		Protocol: "VNC",
		Shared:   false,
	}
	if err := repo.CreateWithQuota(ctx, cUnlimited, 0); err != nil {
		t.Fatalf("unexpected error with quota=0: %v", err)
	}

	// 6. Other user is not affected by alice's quota
	cBob := &model.Connection{
		Username: "bob",
		Host:     "192.168.1.10",
		Port:     5905,
		Protocol: "VNC",
		Shared:   false,
	}
	if err := repo.CreateWithQuota(ctx, cBob, quota); err != nil {
		t.Fatalf("unexpected error for bob: %v", err)
	}
}

func TestConnectionRepo_CreateWithQuota_Concurrent(t *testing.T) {
	t.Parallel()

	store, repo := newConnRepoForTest(t)
	ctx := context.Background()

	username := "charlie"
	quota := 3
	concurrency := 12

	var successCount int64
	var quotaExceededCount int64
	var otherErrCount int64

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // synchronize start

			conn := &model.Connection{
				Username: username,
				Host:     "192.168.1.100",
				Port:     uint(6000 + idx),
				Protocol: "VNC",
				Shared:   false,
			}

			// Retry with slight backoff on SQLite busy / lock contention
			var insertErr error
			for attempt := 0; attempt < 10; attempt++ {
				insertErr = repo.CreateWithQuota(ctx, conn, quota)
				if insertErr == nil {
					atomic.AddInt64(&successCount, 1)
					return
				}
				var appErr interface{ HTTPStatus() int }
				if errors.As(insertErr, &appErr) && appErr.HTTPStatus() == http.StatusTooManyRequests {
					atomic.AddInt64(&quotaExceededCount, 1)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			atomic.AddInt64(&otherErrCount, 1)
			t.Logf("goroutine %d failed with: %v", idx, insertErr)
		}(i)
	}

	close(start) // trigger all goroutines
	wg.Wait()

	if otherErrCount > 0 {
		t.Fatalf("encountered %d unexpected errors (e.g. database locked after retries)", otherErrCount)
	}

	if successCount != int64(quota) {
		t.Errorf("expected exactly %d successful inserts matching quota, got %d", quota, successCount)
	}

	if quotaExceededCount != int64(concurrency)-int64(quota) {
		t.Errorf("expected %d quota exceeded errors, got %d", concurrency-quota, quotaExceededCount)
	}

	// Verify DB record count for charlie
	var dbCount int64
	if err := store.db.Model(&model.Connection{}).Where("username = ? AND shared = ?", username, false).Count(&dbCount).Error; err != nil {
		t.Fatalf("count connections: %v", err)
	}
	if dbCount != int64(quota) {
		t.Errorf("expected DB count to be %d, got %d", quota, dbCount)
	}
}

func TestConnectionRepo_CreateWithQuota_NodeAdmissionAndCapacity(t *testing.T) {
	t.Parallel()

	store, repo := newConnRepoForTest(t)
	ctx := context.Background()

	// 1. Create test nodes
	nodeBlocked := model.Node{
		NodeID:           "node-blocked",
		AllowConnections: false,
		Status:           model.NodeStatusOnline,
	}
	nodeLimited := model.Node{
		NodeID:           "node-limited",
		AllowConnections: true,
		MaxConnections:   1,
		Status:           model.NodeStatusOnline,
	}
	if err := store.db.Create(&nodeBlocked).Error; err != nil {
		t.Fatalf("create nodeBlocked: %v", err)
	}
	if err := store.db.Model(&nodeBlocked).Update("allow_connections", false).Error; err != nil {
		t.Fatalf("update nodeBlocked: %v", err)
	}
	if err := store.db.Create(&nodeLimited).Error; err != nil {
		t.Fatalf("create nodeLimited: %v", err)
	}

	// 2. Reject non-existent node
	cMissing := &model.Connection{
		Username:     "alice",
		TargetNodeID: "node-missing",
		Host:         "192.168.1.10",
		Port:         5900,
		Protocol:     "VNC",
	}
	err := repo.CreateWithQuota(ctx, cMissing, 0)
	if err == nil {
		t.Fatal("expected error for non-existent node, got nil")
	}
	var appErr interface{ HTTPStatus() int }
	if !errors.As(err, &appErr) || appErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing node, got %v", err)
	}

	// 3. Reject connection on node with AllowConnections=false
	cBlocked := &model.Connection{
		Username:     "alice",
		TargetNodeID: "node-blocked",
		Host:         "192.168.1.10",
		Port:         5900,
		Protocol:     "VNC",
	}
	err = repo.CreateWithQuota(ctx, cBlocked, 0)
	if err == nil {
		t.Fatal("expected error for blocked node, got nil")
	}
	if !errors.As(err, &appErr) || appErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for blocked node, got %v", err)
	}

	// 4. Accept 1st connection on node with MaxConnections=1
	cLimited1 := &model.Connection{
		Username:     "alice",
		TargetNodeID: "node-limited",
		Host:         "192.168.1.10",
		Port:         5901,
		Protocol:     "VNC",
		Shared:       false,
	}
	if err := repo.CreateWithQuota(ctx, cLimited1, 0); err != nil {
		t.Fatalf("unexpected error on 1st node connection: %v", err)
	}

	// 5. Reject 2nd non-shared connection on node with MaxConnections=1 (even with different user and quota=0)
	cLimited2 := &model.Connection{
		Username:     "bob",
		TargetNodeID: "node-limited",
		Host:         "192.168.1.10",
		Port:         5902,
		Protocol:     "VNC",
		Shared:       false,
	}
	err = repo.CreateWithQuota(ctx, cLimited2, 0)
	if err == nil {
		t.Fatal("expected error for exceeded node capacity, got nil")
	}
	if !errors.As(err, &appErr) || appErr.HTTPStatus() != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for exceeded node capacity, got %v", err)
	}

	// 6. Shared connection bypasses MaxConnections limit on node-limited
	cShared := &model.Connection{
		TargetNodeID: "node-limited",
		Host:         "192.168.1.10",
		Port:         3389,
		Protocol:     "RDP",
		Shared:       true,
	}
	if err := repo.CreateWithQuota(ctx, cShared, 0); err != nil {
		t.Fatalf("unexpected error for shared connection on node-limited: %v", err)
	}

	// 7. Shared connection still rejected if AllowConnections=false
	cSharedBlocked := &model.Connection{
		TargetNodeID: "node-blocked",
		Host:         "192.168.1.10",
		Port:         3389,
		Protocol:     "RDP",
		Shared:       true,
	}
	err = repo.CreateWithQuota(ctx, cSharedBlocked, 0)
	if err == nil {
		t.Fatal("expected error for shared connection on blocked node, got nil")
	}
	if !errors.As(err, &appErr) || appErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for shared connection on blocked node, got %v", err)
	}
}
