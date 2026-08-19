package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
)

func newStorageRepoForTest(t *testing.T) (*Store, *StorageRepo) {
	t.Helper()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
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
	return store, NewRepos(store).Storage
}

func TestInsertStorageReportDedup(t *testing.T) {
	t.Parallel()

	_, repo := newStorageRepoForTest(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 24, 14, 30, 0, 0, time.UTC)

	first := &model.StorageReport{
		StorageID:  "share",
		Total:      1000,
		Used:       400,
		ReportedAt: base,
		SampledBy:  "node-a",
	}
	if err := repo.InsertStorageReport(ctx, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second report within the 5-minute dedup window must be dropped.
	second := &model.StorageReport{
		StorageID:  "share",
		Total:      1100,
		Used:       500,
		ReportedAt: base.Add(2 * time.Minute),
		SampledBy:  "node-b",
	}
	if err := repo.InsertStorageReport(ctx, second); err != nil {
		t.Fatalf("second insert: %v", err)
	}

	// Same storage, but outside the window, must be kept.
	outside := &model.StorageReport{
		StorageID:  "share",
		Total:      1200,
		Used:       600,
		ReportedAt: base.Add(10 * time.Minute),
		SampledBy:  "node-c",
	}
	if err := repo.InsertStorageReport(ctx, outside); err != nil {
		t.Fatalf("outside-window insert: %v", err)
	}

	// Different storage ID must always be kept.
	other := &model.StorageReport{
		StorageID:  "other-array",
		Total:      900,
		Used:       300,
		ReportedAt: base,
		SampledBy:  "node-a",
	}
	if err := repo.InsertStorageReport(ctx, other); err != nil {
		t.Fatalf("other-storage insert: %v", err)
	}

	reports, err := repo.QueryStorageHistory(ctx, "share", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("query history: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("share report count = %d, want 2 (dedup within window)", len(reports))
	}

	others, err := repo.QueryStorageHistory(ctx, "other-array", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("query other history: %v", err)
	}
	if len(others) != 1 {
		t.Fatalf("other-array report count = %d, want 1", len(others))
	}
}

func TestInsertStorageReportConcurrentNoDuplicates(t *testing.T) {
	t.Parallel()

	_, repo := newStorageRepoForTest(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 24, 14, 30, 0, 0, time.UTC)
	const n = 8

	errs := make(chan error, n)
	for range n {
		go func() {
			errs <- repo.InsertStorageReport(ctx, &model.StorageReport{
				StorageID:  "concurrent-share",
				Total:      1000,
				Used:       400,
				ReportedAt: base,
				SampledBy:  "node-x",
			})
		}()
	}

	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent insert: %v", err)
		}
	}

	reports, err := repo.QueryStorageHistory(ctx, "concurrent-share", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("query history: %v", err)
	}
	// Even under concurrent inserts, only one row should survive the dedup.
	if len(reports) != 1 {
		t.Fatalf("concurrent-share report count = %d, want 1 (TOCTOU race fixed)", len(reports))
	}
}
