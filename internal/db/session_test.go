package db

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func newSessionRepoForTest(t *testing.T) (*Store, *SessionRepo) {
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
	return store, NewRepos(store).Session
}

func TestSessionRenewReturnsNotFoundWhenNoActiveRowUpdated(t *testing.T) {
	t.Parallel()

	_, repo := newSessionRepoForTest(t)
	err := repo.Renew(context.Background(), 404, time.Now().Add(time.Hour))
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("renew missing session error = %v, want ErrNotFound", err)
	}
}

func TestSessionRenewDoesNotRenewExpiredSession(t *testing.T) {
	t.Parallel()

	_, repo := newSessionRepoForTest(t)
	session := &model.Session{
		UserID:    1,
		Username:  "hpcadmin",
		Token:     "expired-token",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	err := repo.Renew(context.Background(), session.ID, time.Now().Add(time.Hour))
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("renew expired session error = %v, want ErrNotFound", err)
	}
}

func TestSessionListIncludesRoleAndScopes(t *testing.T) {
	t.Parallel()

	_, repo := newSessionRepoForTest(t)
	session := &model.Session{
		UserID:      42,
		Username:    "scoped_user",
		Token:       "scoped-token",
		IP:          "10.0.0.2",
		Role:        model.RoleNormal,
		UserType:    model.UserTypeSystem,
		AdminScopes: model.AdminScopes(model.ScopeAll),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	sessions, _, err := repo.List(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].Role != model.RoleNormal {
		t.Fatalf("role = %v, want %v", sessions[0].Role, model.RoleNormal)
	}
	if sessions[0].AdminScopes != model.AdminScopes(model.ScopeAll) {
		t.Fatalf("admin scopes = %v, want %v", sessions[0].AdminScopes, model.ScopeAll)
	}
}
