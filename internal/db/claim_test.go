package db

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func newClaimRepo(t *testing.T) (*Store, *ConnectionRepo) {
	t.Helper()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "claim.db") + "?_busy_timeout=5000",
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	sqlDB, err := store.DB().DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(2)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store, NewRepos(store).Connection
}

func claimRow(resID uint, username, nodeID string) *model.Connection {
	return &model.Connection{
		ResourceID: &resID,
		Username:   username,
		Protocol:   contract.VNCProtocol,
		NodeID:     nodeID,
	}
}

func TestClaimVNCConcurrentSameDisplay(t *testing.T) {
	t.Parallel()

	store, repo := newClaimRepo(t)
	ctx := context.Background()
	resID := uint(7)
	seedConnectionResource(t, store, resID, 2, []string{"node-1"})
	display := 4

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	rows := [2]*model.Connection{}
	for i, username := range []string{"alice", "bob"} {
		wg.Go(func() {
			row := claimRow(resID, username, "node-1")
			rows[i] = row
			<-start
			errs[i] = repo.ClaimVNC(ctx, row, display)
		})
	}
	close(start)
	wg.Wait()

	var winner *model.Connection
	losers := 0
	for i, err := range errs {
		if err == nil {
			if winner != nil {
				t.Fatal("both claims succeeded")
			}
			winner = rows[i]
			continue
		}
		losers++
		domain, ok := errors.AsType[contract.DomainError](err)
		if !ok || domain.HTTPStatus() != http.StatusConflict {
			t.Fatalf("loser error = %v, want HTTP 409", err)
		}
		if !errors.Is(err, ErrDisplayOccupied) {
			t.Fatalf("loser error = %v, want ErrDisplayOccupied", err)
		}
	}
	if winner == nil || losers != 1 {
		t.Fatalf("winner=%v losers=%d errs=%v", winner != nil, losers, errs)
	}

	var stored []model.Connection
	if err := store.DB().Find(&stored).Error; err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("connection rows = %d, want 1", len(stored))
	}
	if stored[0].Username != winner.Username || stored[0].Status != model.ConnectionPending || stored[0].PID != nil {
		t.Fatalf("stored claim = %+v, want pending winner without pid", stored[0])
	}
	if stored[0].Display == nil || *stored[0].Display != display || stored[0].Port == 0 {
		t.Fatalf("stored display/port = %+v, want display %d and port > 0", stored[0], display)
	}
}

func TestClaimVNCFixedDisplayAndQuota(t *testing.T) {
	t.Parallel()

	store, repo := newClaimRepo(t)
	ctx := context.Background()
	resID := uint(8)
	seedConnectionResource(t, store, resID, 1, []string{"node-1"})
	display := 0

	first := claimRow(resID, "alice", "node-1")
	if err := repo.ClaimVNC(ctx, first, display); err != nil {
		t.Fatalf("claim display 0: %v", err)
	}
	if first.Status != model.ConnectionPending || first.PID != nil || first.Port == 0 || first.Display == nil || *first.Display != 0 {
		t.Fatalf("first claim = %+v", first)
	}

	secondDisplay := 3
	second := claimRow(resID, "alice", "node-1")
	err := repo.ClaimVNC(ctx, second, secondDisplay)
	if err == nil {
		t.Fatal("expected quota conflict")
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusConflict || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error = %v, want HTTP 409 ErrQuotaExceeded", err)
	}

	other := claimRow(resID, "bob", "node-1")
	err = repo.ClaimVNC(ctx, other, display)
	if err == nil || !errors.Is(err, ErrDisplayOccupied) {
		t.Fatalf("display error = %v, want ErrDisplayOccupied", err)
	}

	var count int64
	if err := store.DB().Model(&model.Connection{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
}

func TestClaimVNCPendingMultiplePendingOnNode(t *testing.T) {
	t.Parallel()

	store, repo := newClaimRepo(t)
	ctx := context.Background()
	resID := uint(9)
	seedConnectionResource(t, store, resID, 4, []string{"node-1"})

	held := 1
	holder := claimRow(resID, "bob", "node-1")
	if err := repo.ClaimVNC(ctx, holder, held); err != nil {
		t.Fatalf("hold display 1: %v", err)
	}

	first := claimRow(resID, "alice", "node-1")
	if err := repo.ClaimVNCPending(ctx, first); err != nil {
		t.Fatalf("ClaimVNCPending: %v", err)
	}
	if first.Display != nil {
		t.Fatalf("display = %v, want nil until finalize", first.Display)
	}

	second := claimRow(resID, "alice", "node-1")
	if err := repo.ClaimVNCPending(ctx, second); err != nil {
		t.Fatalf("second ClaimVNCPending: %v", err)
	}
	if second.Port == first.Port {
		t.Fatalf("placeholder ports must differ: both %d", first.Port)
	}

	if err := repo.FinalizeVNCPending(ctx, first.ID, 2, 5902, nil, time.Now()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := repo.ReleaseClaim(ctx, second.ID); err != nil {
		t.Fatalf("release second pending: %v", err)
	}
}

func TestClaimVNCReleaseAndFinalize(t *testing.T) {
	t.Parallel()

	store, repo := newClaimRepo(t)
	ctx := context.Background()
	resID := uint(10)
	seedConnectionResource(t, store, resID, 2, []string{"node-1"})
	display := 6

	pending := claimRow(resID, "alice", "node-1")
	if err := repo.ClaimVNC(ctx, pending, display); err != nil {
		t.Fatalf("claim: %v", err)
	}
	pid := uint(42)
	if err := repo.FinalizeClaim(ctx, pending.ID, 5906, &pid, time.Now()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := repo.ReleaseClaim(ctx, pending.ID); err != nil {
		t.Fatalf("release running: %v", err)
	}

	stored, err := repo.GetByID(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get finalized: %v", err)
	}
	if stored.Status != model.ConnectionRunning || stored.Port != 5906 || stored.PID == nil || *stored.PID != pid {
		t.Fatalf("finalized = %+v", stored)
	}

	otherDisplay := 7
	other := claimRow(resID, "alice", "node-1")
	if err := repo.ClaimVNC(ctx, other, otherDisplay); err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if err := repo.ReleaseClaim(ctx, other.ID); err != nil {
		t.Fatalf("release pending: %v", err)
	}
	if _, err := repo.GetByID(ctx, other.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("released claim err = %v, want not found", err)
	}
	if _, err := repo.GetByID(ctx, pending.ID); err != nil {
		t.Fatalf("running row removed: %v", err)
	}
}

func TestClaimVNCPendingLeavesDisplayNil(t *testing.T) {
	t.Parallel()

	store, repo := newClaimRepo(t)
	ctx := context.Background()
	resID := uint(11)
	seedConnectionResource(t, store, resID, 2, []string{"node-1"})

	pending := claimRow(resID, "bob", "node-1")
	if err := repo.ClaimVNCPending(ctx, pending); err != nil {
		t.Fatalf("ClaimVNCPending: %v", err)
	}
	if pending.Display != nil {
		t.Fatalf("display = %v, want nil", pending.Display)
	}
	stored, err := repo.GetByID(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Display != nil || stored.Status != model.ConnectionPending {
		t.Fatalf("stored = %+v", stored)
	}
	disp := 4
	pid := uint(99)
	if err := repo.FinalizeVNCPending(ctx, pending.ID, disp, 5904, &pid, time.Now()); err != nil {
		t.Fatalf("FinalizeVNCPending: %v", err)
	}
	stored, err = repo.GetByID(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get after finalize: %v", err)
	}
	if stored.Display == nil || *stored.Display != disp {
		t.Fatalf("display after finalize = %v", stored.Display)
	}
}
