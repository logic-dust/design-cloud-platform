package command

import (
	"context"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"

	"golang.org/x/crypto/bcrypt"
)

type fakeUserPasswordStore struct {
	users   map[string]model.User
	updated *model.User
}

func (f *fakeUserPasswordStore) GetByName(_ context.Context, username string) (model.User, error) {
	u, ok := f.users[username]
	if !ok {
		return model.User{}, contract.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserPasswordStore) Update(_ context.Context, u *model.User) error {
	cp := *u
	f.users[u.Username] = cp
	f.updated = &cp
	return nil
}

type fakeSessionRevoker struct {
	revoked []string
}

func (f *fakeSessionRevoker) DeleteByUsername(_ context.Context, username string) error {
	f.revoked = append(f.revoked, username)
	return nil
}

func TestSetAppUserPassword_RejectsSystemUser(t *testing.T) {
	store := &fakeUserPasswordStore{
		users: map[string]model.User{
			"alice": {ID: 2, Username: "alice", Type: model.UserTypeSystem, Role: model.RoleNormal},
		},
	}
	sessions := &fakeSessionRevoker{}

	err := setAppUserPassword(context.Background(), store, sessions, nil, "alice", "StrongPass!2026", false)
	if err == nil || !strings.Contains(err.Error(), "not an application user") {
		t.Fatalf("expected non-app user error, got %v", err)
	}
	if store.updated != nil || len(sessions.revoked) != 0 {
		t.Fatalf("expected no update or session revocation for system user")
	}
}

func TestSetAppUserPassword_NotFound(t *testing.T) {
	store := &fakeUserPasswordStore{users: map[string]model.User{}}
	err := setAppUserPassword(context.Background(), store, &fakeSessionRevoker{}, nil, "missing", "StrongPass!2026", false)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestSetAppUserPassword_RejectsWeakPassword(t *testing.T) {
	store := &fakeUserPasswordStore{
		users: map[string]model.User{
			model.UsernameBuiltinSuperAdmin: {ID: 1, Username: model.UsernameBuiltinSuperAdmin, Type: model.UserTypeApp, Role: model.RoleSuperAdmin},
		},
	}
	err := setAppUserPassword(context.Background(), store, &fakeSessionRevoker{}, nil, model.UsernameBuiltinSuperAdmin, "123456", false)
	if err == nil {
		t.Fatal("expected password policy error for weak password")
	}
	if store.updated != nil {
		t.Fatal("expected no DB update on policy error")
	}
}

func TestSetAppUserPassword_SuccessAndRevokesSessions(t *testing.T) {
	store := &fakeUserPasswordStore{
		users: map[string]model.User{
			model.UsernameBuiltinSuperAdmin: {
				ID:       1,
				Username: model.UsernameBuiltinSuperAdmin,
				Type:     model.UserTypeApp,
				Role:     model.RoleSuperAdmin,
				Status:   model.UserPasswordExpired,
			},
		},
	}
	sessions := &fakeSessionRevoker{}

	if err := setAppUserPassword(context.Background(), store, sessions, nil, model.UsernameBuiltinSuperAdmin, "StrongPass!2026", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.updated == nil {
		t.Fatal("expected user update")
	}
	if store.updated.Status != model.UserActive {
		t.Fatalf("expected status UserActive, got %d", store.updated.Status)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(store.updated.Password), []byte("StrongPass!2026")); err != nil {
		t.Fatalf("stored hash does not match new password: %v", err)
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != model.UsernameBuiltinSuperAdmin {
		t.Fatalf("expected session revocation for dceadmin, got %v", sessions.revoked)
	}
}

func TestSetAppUserPassword_ExpireSetsExpiredStatus(t *testing.T) {
	store := &fakeUserPasswordStore{
		users: map[string]model.User{
			model.UsernameBuiltinSystemAdmin: {ID: 2, Username: model.UsernameBuiltinSystemAdmin, Type: model.UserTypeApp, Role: model.RoleSystemAdmin, Status: model.UserActive},
		},
	}
	if err := setAppUserPassword(context.Background(), store, &fakeSessionRevoker{}, nil, model.UsernameBuiltinSystemAdmin, "StrongPass!2026", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.updated.Status != model.UserPasswordExpired {
		t.Fatalf("expected status UserPasswordExpired, got %d", store.updated.Status)
	}
}

func TestReadSingleLine(t *testing.T) {
	got, err := readSingleLine(strings.NewReader("MySecret!123\r\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "MySecret!123" {
		t.Fatalf("got %q, want %q", got, "MySecret!123")
	}
}

func TestExecute_UserMissingSubcommand(t *testing.T) {
	if err := Execute(&Flags{}, []string{"user"}); err == nil {
		t.Fatal("expected error for missing user subcommand")
	}
}
