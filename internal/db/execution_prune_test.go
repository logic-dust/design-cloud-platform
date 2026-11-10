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

// 执行记录清理只回收终态记录：pending/dispatched 可能仍有在途请求，
// 删除会让幂等回查误判为不存在。
func TestExecutionPruneTerminal(t *testing.T) {
	t.Parallel()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "execution_prune.db") + "?_busy_timeout=5000",
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

	insert := func(executionID, traceID string, status model.ExecutionStatus, createdAt time.Time) {
		t.Helper()
		if err := repo.Create(ctx, executionID, "USER_LIST", traceID, "node-a", "node-b"); err != nil {
			t.Fatalf("create %s: %v", executionID, err)
		}
		if err := store.db.Model(&model.ExecutionRecord{}).
			Where("execution_id = ?", executionID).
			Update("created_at", createdAt).Error; err != nil {
			t.Fatalf("backdate %s: %v", executionID, err)
		}
		if status != model.ExecutionStatusPending {
			if _, err := repo.MarkTerminal(ctx, traceID, status, 0, ""); err != nil {
				t.Fatalf("mark terminal %s: %v", executionID, err)
			}
		}
	}

	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	insert("exec-old-terminal", "trace-1", model.ExecutionStatusSucceeded, old)
	insert("exec-old-pending", "trace-2", model.ExecutionStatusPending, old)
	insert("exec-recent-terminal", "trace-3", model.ExecutionStatusSucceeded, now)

	deleted, err := repo.PruneTerminal(ctx, now.Add(-7*24*time.Hour), 100)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (only expired terminal record)", deleted)
	}

	for _, id := range []string{"exec-old-pending", "exec-recent-terminal"} {
		if _, err := repo.GetByExecutionID(ctx, id); err != nil {
			t.Fatalf("%s should be retained: %v", id, err)
		}
	}
	if _, err := repo.GetByExecutionID(ctx, "exec-old-terminal"); err == nil {
		t.Fatal("expired terminal record should be deleted")
	}
}

func TestExecutionExpireStale(t *testing.T) {
	t.Parallel()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "execution_expire.db") + "?_busy_timeout=5000",
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

	backdate := func(executionID string, updatedAt time.Time) {
		t.Helper()
		if err := store.db.Model(&model.ExecutionRecord{}).
			Where("execution_id = ?", executionID).
			UpdateColumn("updated_at", updatedAt).Error; err != nil {
			t.Fatalf("backdate %s: %v", executionID, err)
		}
	}

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	if err := repo.Create(ctx, "exec-young-pending", "USER_LIST", "trace-young", "node-a", "node-b"); err != nil {
		t.Fatalf("create young: %v", err)
	}
	if err := repo.Create(ctx, "exec-old-pending", "USER_LIST", "trace-old-pending", "node-a", "node-b"); err != nil {
		t.Fatalf("create old pending: %v", err)
	}
	backdate("exec-old-pending", old)

	if err := repo.Create(ctx, "exec-old-dispatched", "USER_LIST", "trace-old-dispatched", "node-a", "node-b"); err != nil {
		t.Fatalf("create old dispatched: %v", err)
	}
	if err := repo.MarkDispatched(ctx, "trace-old-dispatched"); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	backdate("exec-old-dispatched", old)

	if err := repo.Create(ctx, "exec-old-ok", "USER_LIST", "trace-old-ok", "node-a", "node-b"); err != nil {
		t.Fatalf("create old ok: %v", err)
	}
	if _, err := repo.MarkTerminal(ctx, "trace-old-ok", model.ExecutionStatusSucceeded, 0, ""); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	backdate("exec-old-ok", old)

	n, err := repo.ExpireStale(ctx, now.Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 2 {
		t.Fatalf("expired = %d, want 2", n)
	}

	young, err := repo.GetByExecutionID(ctx, "exec-young-pending")
	if err != nil {
		t.Fatalf("young: %v", err)
	}
	if young.Status != model.ExecutionStatusPending {
		t.Fatalf("young status = %s, want pending", young.Status)
	}

	for _, id := range []string{"exec-old-pending", "exec-old-dispatched"} {
		got, err := repo.GetByExecutionID(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got.Status != model.ExecutionStatusTimedOut || got.SystemError != abandonedNonTerminalError || got.FinishedAt == nil {
			t.Fatalf("%s = status %s err %q finished %v", id, got.Status, got.SystemError, got.FinishedAt)
		}
	}

	okRec, err := repo.GetByExecutionID(ctx, "exec-old-ok")
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if okRec.Status != model.ExecutionStatusSucceeded {
		t.Fatalf("succeeded status = %s, want succeeded", okRec.Status)
	}
}
