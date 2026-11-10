package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/platform/config"
)

func newTaskLeaseRepo(t *testing.T) *TaskLeaseRepo {
	t.Helper()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "task_lease.db") + "?_busy_timeout=5000",
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
	return NewRepos(store).TaskLease
}

// epoch 语义（refactor v5 §4.3.1）：
// 存活续约不变；新建行、新 Owner 抢占、同 Owner 跨过期重获一律 +1。
func TestTaskLeaseTryAcquireEpochSemantics(t *testing.T) {
	t.Parallel()
	repo := newTaskLeaseRepo(t)
	ctx := context.Background()

	// 新建行：epoch 从 1 开始
	acquired, epoch, err := repo.TryAcquire(ctx, "task", "node-a", 200*time.Millisecond)
	if err != nil || !acquired {
		t.Fatalf("first acquire: acquired=%v err=%v", acquired, err)
	}
	if epoch != 1 {
		t.Fatalf("first acquire epoch = %d, want 1", epoch)
	}

	// 存活续约：epoch 不变
	acquired, epoch, err = repo.TryAcquire(ctx, "task", "node-a", 200*time.Millisecond)
	if err != nil || !acquired {
		t.Fatalf("renew: acquired=%v err=%v", acquired, err)
	}
	if epoch != 1 {
		t.Fatalf("alive renew epoch = %d, want 1", epoch)
	}

	// 他 Owner 存活期不可抢占
	acquired, _, err = repo.TryAcquire(ctx, "task", "node-b", time.Second)
	if err != nil {
		t.Fatalf("contended acquire: %v", err)
	}
	if acquired {
		t.Fatal("expected acquire to fail while node-a holds an alive lease")
	}

	// 租约过期后新 Owner 抢占：epoch+1
	time.Sleep(250 * time.Millisecond)
	acquired, epoch, err = repo.TryAcquire(ctx, "task", "node-b", time.Second)
	if err != nil || !acquired {
		t.Fatalf("takeover: acquired=%v err=%v", acquired, err)
	}
	if epoch != 2 {
		t.Fatalf("takeover epoch = %d, want 2", epoch)
	}
}

// 同 Owner 跨过期重获也必须 +1：Owner 在「租约已过期但尚未察觉」窗口内仍以旧 epoch
// 签发命令，若重获后 epoch 不变，这些旧命令会继续被视为有效。
func TestTaskLeaseTryAcquireSameOwnerReacquireBumpsEpoch(t *testing.T) {
	t.Parallel()
	repo := newTaskLeaseRepo(t)
	ctx := context.Background()

	acquired, epoch, err := repo.TryAcquire(ctx, "task", "node-a", 100*time.Millisecond)
	if err != nil || !acquired || epoch != 1 {
		t.Fatalf("first acquire: acquired=%v epoch=%d err=%v", acquired, epoch, err)
	}

	time.Sleep(150 * time.Millisecond)
	acquired, epoch, err = repo.TryAcquire(ctx, "task", "node-a", time.Second)
	if err != nil || !acquired {
		t.Fatalf("re-acquire: acquired=%v err=%v", acquired, err)
	}
	if epoch != 2 {
		t.Fatalf("same-owner re-acquire epoch = %d, want 2", epoch)
	}
}
