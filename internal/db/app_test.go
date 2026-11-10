package db

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"

	"gorm.io/gorm"
)

func newAppRepoForTest(t *testing.T) (*Store, *AppRepo, *UserRepo) {
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
	repos := NewRepos(store)
	return store, repos.App, repos.User
}

// TestAppRepoDeleteIsHardDelete 验证应用删除是物理删除：
//   - 删除后即使 Unscoped 查询也取不到（无软删残留行）
//   - user_apps 关联行被清理
//   - 同名应用可立即重建（不会被唯一索引残留阻塞）
func TestAppRepoDeleteIsHardDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, appRepo, userRepo := newAppRepoForTest(t)

	user := &model.User{Username: "alice", Status: model.UserActive}
	if err := userRepo.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	app := &model.App{Name: "hard-del-app", Type: model.BatchApp}
	if err := appRepo.Create(ctx, app); err != nil {
		t.Fatalf("create app: %v", err)
	}
	if err := appRepo.SetUsers(ctx, app.ID, []model.User{*user}); err != nil {
		t.Fatalf("set users: %v", err)
	}

	// 关联行存在
	var assocCount int64
	if err := store.db.WithContext(ctx).Table("user_apps").Where("app_id = ?", app.ID).Count(&assocCount).Error; err != nil {
		t.Fatalf("count associations: %v", err)
	}
	if assocCount != 1 {
		t.Fatalf("user_apps rows before delete = %d, want 1", assocCount)
	}

	if err := appRepo.Delete(ctx, app.ID); err != nil {
		t.Fatalf("delete app: %v", err)
	}

	// Unscoped 查询也应该找不到（确认物理删除，而非软删）
	var leftover model.App
	err := store.db.WithContext(ctx).Unscoped().First(&leftover, app.ID).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unscoped lookup after delete err = %v, want record not found", err)
	}

	// 关联行已清理
	var afterCount int64
	if err := store.db.WithContext(ctx).Table("user_apps").Where("app_id = ?", app.ID).Count(&afterCount).Error; err != nil {
		t.Fatalf("count associations after delete: %v", err)
	}
	if afterCount != 0 {
		t.Fatalf("user_apps rows after delete = %d, want 0", afterCount)
	}

	// 同名重建应成功（唯一索引未被残留占用）
	recreated := &model.App{Name: "hard-del-app", Type: model.BatchApp}
	if err := appRepo.Create(ctx, recreated); err != nil {
		t.Fatalf("recreate same-name app after hard delete: %v", err)
	}
	if recreated.ID == app.ID {
		// 主键复用并非错误（取决于序列），但通常是新 ID
		t.Logf("recreated app reused id %d", recreated.ID)
	}
}

func TestAppRepoDeleteMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	_, appRepo, _ := newAppRepoForTest(t)

	if err := appRepo.Delete(ctx, 999999); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("delete missing app err = %v, want contract.ErrNotFound", err)
	}
}
