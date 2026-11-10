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

func TestExecutionRepo_ListStableOrderAndLimitCap(t *testing.T) {
	t.Parallel()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "execution_list.db") + "?_busy_timeout=5000",
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewRepos(store).Execution
	ctx := context.Background()

	ts := time.Now().Add(-time.Hour)
	for _, eid := range []string{"a", "b", "c"} {
		if err := repo.Create(ctx, eid, "USER_LIST", "trace-"+eid, "n1", "n2"); err != nil {
			t.Fatalf("create %s: %v", eid, err)
		}
		if err := store.db.Model(&model.ExecutionRecord{}).Where("execution_id = ?", eid).
			Update("created_at", ts).Error; err != nil {
			t.Fatalf("backdate %s: %v", eid, err)
		}
	}

	got, total, err := repo.List(ctx, model.ExecutionListFilter{Limit: DefaultListLimit})
	if err != nil || total != 3 || len(got) != 3 {
		t.Fatalf("list: n=%d total=%d err=%v", len(got), total, err)
	}
	// created_at 相同：id DESC 兜底
	if got[0].ID <= got[1].ID || got[1].ID <= got[2].ID {
		t.Fatalf("id order=%d %d %d", got[0].ID, got[1].ID, got[2].ID)
	}

	capped, _, err := repo.List(ctx, model.ExecutionListFilter{Limit: MaxListLimit + 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 3 {
		t.Fatalf("cap fetch len=%d", len(capped))
	}
}
