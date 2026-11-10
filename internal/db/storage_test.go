package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func newStorageTestRepo(t *testing.T) *StorageRepo {
	t.Helper()
	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "storage-test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRepos(store).Storage
}

func TestPruneReportsBefore_BatchesByID(t *testing.T) {
	repo := newStorageTestRepo(t)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	for i := range 3 {
		r := &model.StorageReport{
			StorageID:  "s1",
			Total:      100,
			Used:       int64(i),
			ReportedAt: old.Add(time.Duration(i) * time.Minute),
			SampledBy:  "n1",
		}
		if err := repo.store.db.WithContext(ctx).Create(r).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Bypass InsertStorageReport dedup; direct Create above.
	n, err := repo.PruneReportsBefore(ctx, time.Now().Add(-24*time.Hour), 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d, want 2", n)
	}
	n, err = repo.PruneReportsBefore(ctx, time.Now().Add(-24*time.Hour), 2)
	if err != nil {
		t.Fatalf("prune2: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned remainder = %d, want 1", n)
	}
}

func TestQueryStorageHistory_KeepsNewestWhenCapped(t *testing.T) {
	repo := newStorageTestRepo(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	const rows = 5
	for i := range rows {
		r := &model.StorageReport{
			StorageID:  "s1",
			Total:      100,
			Used:       int64(i),
			ReportedAt: base.Add(time.Duration(i) * time.Minute),
			SampledBy:  "n1",
		}
		if err := repo.store.db.WithContext(ctx).Create(r).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	prev := maxStorageHistoryQueryRows
	maxStorageHistoryQueryRows = 3
	t.Cleanup(func() { maxStorageHistoryQueryRows = prev })

	got, err := repo.QueryStorageHistory(ctx, "s1", base.Add(-time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Used != 2 || got[2].Used != 4 {
		t.Fatalf("expected newest window Used=2..4 chronological, got %d..%d", got[0].Used, got[2].Used)
	}
}
