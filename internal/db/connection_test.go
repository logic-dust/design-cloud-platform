package db

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
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

func seedConnectionResource(t *testing.T, store *Store, id uint, maxPerUser int, nodes []string) {
	t.Helper()
	res := &model.ConnectionResource{
		ID:         id,
		Name:       "pool",
		Protocol:   contract.VNCProtocol,
		Nodes:      nodes,
		NodeGroups: []string{},
		MaxPerUser: maxPerUser,
	}
	if err := NewRepos(store).ConnectionResource.Create(context.Background(), res); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
}

func TestConnectionRepo_CreateWithResourceQuota(t *testing.T) {
	t.Parallel()

	store, repo := newConnRepoForTest(t)
	ctx := context.Background()

	username := "alice"
	resID := uint(101)
	maxQuota := 2
	seedConnectionResource(t, store, resID, maxQuota, []string{"node-1"})

	// 1. First insert under quota -> OK
	c1 := &model.Connection{
		ResourceID: &resID,
		Username:   username,
		Port:       5901,
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Status:     model.ConnectionRunning,
	}
	if err := repo.CreateWithResourceQuota(ctx, c1); err != nil {
		t.Fatalf("unexpected error on 1st insert: %v", err)
	}

	// 2. Second insert under quota -> OK
	c2 := &model.Connection{
		ResourceID: &resID,
		Username:   username,
		Port:       5902,
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Status:     model.ConnectionRunning,
	}
	if err := repo.CreateWithResourceQuota(ctx, c2); err != nil {
		t.Fatalf("unexpected error on 2nd insert: %v", err)
	}

	// 3. Third insert exceeding quota -> Error
	c3 := &model.Connection{
		ResourceID: &resID,
		Username:   username,
		Port:       5903,
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Status:     model.ConnectionRunning,
	}
	err := repo.CreateWithResourceQuota(ctx, c3)
	if err == nil {
		t.Fatal("expected error on 3rd insert exceeding quota, got nil")
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusConflict {
		t.Fatalf("quota error = %v, want HTTP 409", err)
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error = %v, want ErrQuotaExceeded", err)
	}

	// 4. Other user not affected
	cBob := &model.Connection{
		ResourceID: &resID,
		Username:   "bob",
		Port:       5904,
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Status:     model.ConnectionRunning,
	}
	if err := repo.CreateWithResourceQuota(ctx, cBob); err != nil {
		t.Fatalf("unexpected error for bob: %v", err)
	}
}

func TestConnectionRepo_DisplayLock(t *testing.T) {
	t.Parallel()

	store, repo := newConnRepoForTest(t)
	ctx := context.Background()

	disp := 5
	resID := uint(101)
	seedConnectionResource(t, store, resID, 1, []string{"eda-srv-01", "eda-srv-02"})

	c1 := &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		NodeID:     "eda-srv-01",
		Port:       5905,
		Display:    &disp,
		Protocol:   contract.VNCProtocol,
		Status:     model.ConnectionRunning,
	}
	if err := repo.CreateWithResourceQuota(ctx, c1); err != nil {
		t.Fatalf("insert c1: %v", err)
	}

	// Same display on same node -> should conflict
	c2 := &model.Connection{
		ResourceID: &resID,
		Username:   "bob",
		NodeID:     "eda-srv-01",
		Port:       5905,
		Display:    &disp,
		Protocol:   contract.VNCProtocol,
		Status:     model.ConnectionRunning,
	}
	if err := repo.CreateWithResourceQuota(ctx, c2); err == nil {
		t.Fatal("expected error on duplicate display on same node, got nil")
	} else if !errors.Is(err, ErrDisplayOccupied) {
		t.Fatalf("display error = %v, want ErrDisplayOccupied", err)
	}

	// Same display on different node -> allowed
	c3 := &model.Connection{
		ResourceID: &resID,
		Username:   "bob",
		NodeID:     "eda-srv-02",
		Port:       5905,
		Display:    &disp,
		Protocol:   contract.VNCProtocol,
		Status:     model.ConnectionRunning,
	}
	if err := repo.CreateWithResourceQuota(ctx, c3); err != nil {
		t.Fatalf("insert c3 on different node: %v", err)
	}
}

func TestConnectionRepo_ZeroMaxPerUserRejects(t *testing.T) {
	t.Parallel()

	store, repo := newConnRepoForTest(t)
	ctx := context.Background()
	resID := uint(202)
	seedConnectionResource(t, store, resID, 1, []string{"node-1"})
	if err := store.DB().Model(&model.ConnectionResource{}).Where("id = ?", resID).
		Update("max_per_user", 0).Error; err != nil {
		t.Fatalf("set max_per_user 0: %v", err)
	}

	err := repo.CreateWithResourceQuota(ctx, &model.Connection{
		ResourceID: &resID,
		Username:   "alice",
		Port:       5901,
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Status:     model.ConnectionRunning,
	})
	if err == nil {
		t.Fatal("expected quota rejection when max_per_user is 0")
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error = %v, want ErrQuotaExceeded", err)
	}
}

func TestConnectionRepo_CreateWithResourceMaxTotalQuota(t *testing.T) {
	t.Parallel()

	store, repo := newConnRepoForTest(t)
	ctx := context.Background()

	resID := uint(202)
	// 单人最多 2 个，全集群总实例最多 3 个
	res := &model.ConnectionResource{
		ID:         resID,
		Name:       "limited-pool",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 2,
		MaxTotal:   3,
	}
	if err := NewRepos(store).ConnectionResource.Create(ctx, res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	// 1. Alice 连 2 个 (OK)
	c1 := &model.Connection{ResourceID: &resID, Username: "alice", Port: 5901, Protocol: contract.VNCProtocol, NodeID: "node-1", Status: model.ConnectionRunning}
	c2 := &model.Connection{ResourceID: &resID, Username: "alice", Port: 5902, Protocol: contract.VNCProtocol, NodeID: "node-1", Status: model.ConnectionRunning}
	if err := repo.CreateWithResourceQuota(ctx, c1); err != nil {
		t.Fatalf("alice c1: %v", err)
	}
	if err := repo.CreateWithResourceQuota(ctx, c2); err != nil {
		t.Fatalf("alice c2: %v", err)
	}

	// 2. Bob 连 1 个 (当前总数 2 -> 3，满足 MaxTotal=3，OK)
	c3 := &model.Connection{ResourceID: &resID, Username: "bob", Port: 5903, Protocol: contract.VNCProtocol, NodeID: "node-1", Status: model.ConnectionRunning}
	if err := repo.CreateWithResourceQuota(ctx, c3); err != nil {
		t.Fatalf("bob c3: %v", err)
	}

	// 3. Bob 连第 2 个 (虽然 Bob 自己未达 MaxPerUser=2，但集群总数已达 3，应该报 ErrQuotaExceeded)
	c4 := &model.Connection{ResourceID: &resID, Username: "bob", Port: 5904, Protocol: contract.VNCProtocol, NodeID: "node-1", Status: model.ConnectionRunning}
	err := repo.CreateWithResourceQuota(ctx, c4)
	if err == nil {
		t.Fatal("expected quota rejection when max_total is reached")
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}

	// 4. Carol 尝试创建连接也应因总配额耗尽被拒
	c5 := &model.Connection{ResourceID: &resID, Username: "carol", Port: 5905, Protocol: contract.VNCProtocol, NodeID: "node-1", Status: model.ConnectionRunning}
	if err := repo.CreateWithResourceQuota(ctx, c5); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("carol connection expected ErrQuotaExceeded, got %v", err)
	}

	// 5. Alice 释放 1 个连接后，Bob 再次尝试成功
	if err := repo.DeleteByID(ctx, c1.ID); err != nil {
		t.Fatalf("delete alice c1: %v", err)
	}
	if err := repo.CreateWithResourceQuota(ctx, c4); err != nil {
		t.Fatalf("bob c4 should succeed after quota released, got %v", err)
	}
}
