package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
)

func newUserRepoForTest(t *testing.T) (*Store, *UserRepo) {
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
	return store, NewRepos(store).User
}

func TestUpdatePermissionsIfRole_OnlyUpdatesExpectedRole(t *testing.T) {
	t.Parallel()

	_, repo := newUserRepoForTest(t)
	ctx := context.Background()
	normal := model.User{Username: "normal", Status: model.UserActive, Role: model.RoleNormal}
	admin := model.User{Username: "admin", Status: model.UserActive, Role: model.RoleSystemAdmin}
	if err := repo.Create(ctx, &normal); err != nil {
		t.Fatalf("create normal user: %v", err)
	}
	if err := repo.Create(ctx, &admin); err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	updated, err := repo.UpdateScopesIfRole(ctx, "normal", model.RoleNormal, model.AdminScopes(model.ScopeAll))
	if err != nil {
		t.Fatalf("update normal user admin scopes: %v", err)
	}
	if !updated {
		t.Fatal("normal user admin scopes was not updated")
	}

	updated, err = repo.UpdateScopesIfRole(ctx, "admin", model.RoleNormal, model.AdminScopes(model.ScopeAll))
	if err != nil {
		t.Fatalf("update admin user admin scopes: %v", err)
	}
	if updated {
		t.Fatal("admin user admin scopes should not update with normal role condition")
	}

	dbAdmin, err := repo.GetByName(ctx, "admin")
	if err != nil {
		t.Fatalf("load admin user: %v", err)
	}
	if dbAdmin.AdminScopes != 0 {
		t.Fatalf("admin scopes = %v, want 0", dbAdmin.AdminScopes)
	}
}
