package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
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

func TestUserDeleteRemovesConnections(t *testing.T) {
	t.Parallel()

	store, repo := newUserRepoForTest(t)
	ctx := context.Background()
	repos := NewRepos(store)
	alice := model.User{Username: "alice", Status: model.UserActive}
	bob := model.User{Username: "bob", Status: model.UserActive}
	if err := repo.Create(ctx, &alice); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := repo.Create(ctx, &bob); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if err := repos.Connection.Create(ctx, &model.Connection{
		Username: "alice",
		Protocol: contract.SSHProtocol,
		NodeID:   "node-a",
		Port:     22,
		Status:   model.ConnectionRunning,
	}); err != nil {
		t.Fatalf("create alice connection: %v", err)
	}
	if err := repos.Connection.Create(ctx, &model.Connection{
		Username: "bob",
		Protocol: contract.SSHProtocol,
		NodeID:   "node-a",
		Port:     22,
		Status:   model.ConnectionRunning,
	}); err != nil {
		t.Fatalf("create bob connection: %v", err)
	}
	if err := repo.Delete(ctx, "alice"); err != nil {
		t.Fatalf("delete alice: %v", err)
	}
	if _, err := repo.GetByName(ctx, "alice"); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("alice lookup err = %v, want not found", err)
	}
	aliceConns, err := repos.Connection.ListByUser(ctx, "alice")
	if err != nil {
		t.Fatalf("list alice connections: %v", err)
	}
	if len(aliceConns) != 0 {
		t.Fatalf("alice connections = %d, want 0", len(aliceConns))
	}
	bobConns, err := repos.Connection.ListByUser(ctx, "bob")
	if err != nil {
		t.Fatalf("list bob connections: %v", err)
	}
	if len(bobConns) != 1 {
		t.Fatalf("bob connections = %d, want 1", len(bobConns))
	}
}

func TestSearchAssignees(t *testing.T) {
	t.Parallel()

	_, repo := newUserRepoForTest(t)
	ctx := context.Background()
	users := []model.User{
		{Username: "xbob", Status: model.UserActive, Role: model.RoleNormal},
		{Username: "Bob", Status: model.UserActive, Role: model.RoleNormal},
		{Username: "abob", Status: model.UserActive, Role: model.RoleNormal},
		{Username: "bobby", Status: model.UserActive, Role: model.RoleNormal},
		{Username: "bobdisabled", Status: model.UserDisabled, Role: model.RoleNormal},
		{Username: "bobadmin", Status: model.UserActive, Role: model.RoleSuperAdmin},
		{Username: "bobscoped", Status: model.UserActive, Role: model.RoleNormal, AdminScopes: model.AdminScopes(model.ScopeAll)},
		{Username: "a_b", Status: model.UserActive, Role: model.RoleNormal},
		{Username: "axb", Status: model.UserActive, Role: model.RoleNormal},
	}
	for i := range users {
		if err := repo.Create(ctx, &users[i]); err != nil {
			t.Fatalf("create %s: %v", users[i].Username, err)
		}
	}

	names := func(q string, limit int) []string {
		t.Helper()
		got, err := repo.SearchAssignees(ctx, q, limit)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		out := make([]string, 0, len(got))
		for _, u := range got {
			if u.ID == 0 {
				t.Fatalf("missing id for %s", u.Username)
			}
			out = append(out, u.Username)
		}
		return out
	}

	if got, want := fmt.Sprint(names("BOB", 20)), "[Bob bobby abob xbob]"; got != want {
		t.Fatalf("search BOB = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(names("bob", 2)), "[Bob bobby]"; got != want {
		t.Fatalf("limit 2 = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(names("a_", 20)), "[a_b]"; got != want {
		t.Fatalf("underscore literal = %s, want %s", got, want)
	}
	if got := names("%%", 20); len(got) != 0 {
		t.Fatalf("percent literal = %v, want none", got)
	}
}

func TestUserListQFiltersUsername(t *testing.T) {
	t.Parallel()

	_, repo := newUserRepoForTest(t)
	ctx := context.Background()
	users := []model.User{
		{Username: "alice", Status: model.UserActive},
		{Username: "bob", Status: model.UserActive},
		{Username: "carol", Status: model.UserActive},
		{Username: "dave", Status: model.UserActive},
		{Username: "eve", Status: model.UserActive},
	}
	for i := range users {
		if err := repo.Create(ctx, &users[i]); err != nil {
			t.Fatalf("create %s: %v", users[i].Username, err)
		}
	}

	page, total, err := repo.List(ctx, 50, 0, "ALI", nil, nil)
	if err != nil {
		t.Fatalf("list q=ALI: %v", err)
	}
	if total != 1 || len(page) != 1 || page[0].Username != "alice" {
		t.Fatalf("q=ALI got total=%d page=%v", total, page)
	}

	page, total, err = repo.List(ctx, 50, 0, "a", nil, nil)
	if err != nil {
		t.Fatalf("list q=a: %v", err)
	}
	if total != 3 {
		t.Fatalf("q=a total=%d, want 3 (alice/carol/dave)", total)
	}

	page, total, err = repo.List(ctx, 1, 1, "a", nil, nil)
	if err != nil {
		t.Fatalf("list q=a page2: %v", err)
	}
	if total != 3 || len(page) != 1 {
		t.Fatalf("offset page total=%d len=%d", total, len(page))
	}

	page, total, err = repo.List(ctx, 50, 0, "__none__", nil, nil)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if total != 0 || len(page) != 0 {
		t.Fatalf("empty q match total=%d len=%d", total, len(page))
	}

	_, total, err = repo.List(ctx, 50, 0, "  ", nil, nil)
	if err != nil {
		t.Fatalf("blank q: %v", err)
	}
	if total != 5 {
		t.Fatalf("blank q total=%d, want 5", total)
	}
}

func TestUserListTypeAndStatusFilters(t *testing.T) {
	t.Parallel()

	_, repo := newUserRepoForTest(t)
	ctx := context.Background()
	sysActive := model.User{Username: "sys-a", Status: model.UserActive, Type: model.UserTypeSystem}
	sysLocked := model.User{Username: "sys-l", Status: model.UserLocked, Type: model.UserTypeSystem}
	appActive := model.User{Username: "app-a", Status: model.UserActive, Type: model.UserTypeApp}
	for _, u := range []*model.User{&sysActive, &sysLocked, &appActive} {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.Username, err)
		}
	}

	tp := model.UserTypeSystem
	page, total, err := repo.List(ctx, 50, 0, "", &tp, nil)
	if err != nil {
		t.Fatalf("type=system: %v", err)
	}
	if total != 2 || len(page) != 2 {
		t.Fatalf("type=system total=%d len=%d", total, len(page))
	}
	for _, u := range page {
		if u.Password != "" {
			t.Fatalf("list must not load password for %s", u.Username)
		}
		if u.Type != model.UserTypeSystem {
			t.Fatalf("unexpected type %v", u.Type)
		}
	}

	st := model.UserActive
	page, total, err = repo.List(ctx, 50, 0, "", &tp, &st)
	if err != nil {
		t.Fatalf("type+status: %v", err)
	}
	if total != 1 || len(page) != 1 || page[0].Username != "sys-a" {
		t.Fatalf("type+status got total=%d page=%v", total, page)
	}
}
