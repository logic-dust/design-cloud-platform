package db

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
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

func TestSessionListProjectionAndUsernameFilter(t *testing.T) {
	t.Parallel()

	_, repo := newSessionRepoForTest(t)
	older := &model.Session{
		UserID:             1,
		Username:           "bob",
		Token:              "bob-old-secret",
		IP:                 "10.0.0.1",
		Hostname:           "bob-old-host",
		Role:               model.RoleNormal,
		UserType:           model.UserTypeSystem,
		AdminScopes:        model.AdminScopes(model.ScopeSessions),
		PasswordMustChange: false,
		ExpiresAt:          time.Now().Add(time.Hour),
	}
	newer := &model.Session{
		UserID:             1,
		Username:           "bob",
		Token:              "bob-new-secret",
		IP:                 "10.0.0.2",
		Hostname:           "bob-new-host",
		Platform:           "windows",
		OSVersion:          "10.0.19045",
		AppVersion:         "2.3.1",
		DeviceID:           "device-uuid-1",
		Role:               model.RoleNormal,
		UserType:           model.UserTypeSystem,
		AdminScopes:        model.AdminScopes(model.ScopeAll),
		PasswordMustChange: true,
		ExpiresAt:          time.Now().Add(2 * time.Hour),
	}
	other := &model.Session{
		UserID:    2,
		Username:  "alice",
		Token:     "alice-secret",
		IP:        "10.0.0.3",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	for _, session := range []*model.Session{older, other, newer} {
		if err := repo.Create(context.Background(), session); err != nil {
			t.Fatalf("save session: %v", err)
		}
	}

	all, totalAll, err := repo.List(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if totalAll != 3 || len(all) != 3 {
		t.Fatalf("list all = %d/%d, want 3/3", len(all), totalAll)
	}
	for _, got := range all {
		if got.Token != "" {
			t.Fatalf("list must omit token, got %q for id=%d", got.Token, got.ID)
		}
	}

	sessions, total, err := repo.ListByUsername(context.Background(), "bob", 50, 0)
	if err != nil {
		t.Fatalf("list by username: %v", err)
	}
	if total != 2 || len(sessions) != 2 {
		t.Fatalf("bob sessions = %d/%d, want 2/2", len(sessions), total)
	}
	if sessions[0].IP != "10.0.0.2" || sessions[1].IP != "10.0.0.1" {
		t.Fatalf("order by updated_at = [%s, %s], want [10.0.0.2, 10.0.0.1]", sessions[0].IP, sessions[1].IP)
	}
	got := sessions[0]
	if got.Hostname != "bob-new-host" || got.Platform != "windows" || got.OSVersion != "10.0.19045" ||
		got.AppVersion != "2.3.1" || got.DeviceID != "device-uuid-1" {
		t.Fatalf("client metadata not loaded: %+v", got)
	}
	if !got.PasswordMustChange {
		t.Fatal("expected password_must_change=true on newest bob session")
	}
	if got.AdminScopes != model.AdminScopes(model.ScopeAll) {
		t.Fatalf("admin scopes = %v, want ScopeAll", got.AdminScopes)
	}
	if got.Token != "" {
		t.Fatal("username list must omit token")
	}
}
