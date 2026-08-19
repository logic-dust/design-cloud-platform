package role

import (
	"context"
	"errors"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/model"
)

// mockUserRepo is a minimal mock for testing role.Service.
type mockUserRepo struct {
	getByNameFn         func(ctx context.Context, username string) (model.User, error)
	updateFn            func(ctx context.Context, user *model.User) error
	listMembersByRoleFn func(ctx context.Context, role model.Role, limit, offset int) ([]string, int64, error)
}

func (m *mockUserRepo) GetByName(ctx context.Context, username string) (model.User, error) {
	return m.getByNameFn(ctx, username)
}

func (m *mockUserRepo) Update(ctx context.Context, user *model.User) error {
	return m.updateFn(ctx, user)
}

func (m *mockUserRepo) ListMembersByRole(ctx context.Context, role model.Role, limit, offset int) ([]string, int64, error) {
	return m.listMembersByRoleFn(ctx, role, limit, offset)
}

func newTestService(t *testing.T, repo *mockUserRepo) *Service {
	t.Helper()
	dir := t.TempDir()
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  dir + "/audit.log",
		SecurityLogPath: dir + "/security.log",
	})
	if err != nil {
		t.Fatalf("create audit service: %v", err)
	}
	// Windows 无法删除仍被打开的文件，须在 TempDir 清理前关闭日志句柄。
	t.Cleanup(func() { _ = auditSvc.Close() })
	return &Service{
		userRepo: repo,
		auditSvc: auditSvc,
	}
}

func TestSetUserRole_InvalidRole(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "testuser", model.Role(99))
	if err == nil {
		t.Fatal("expected error for invalid role")
	}
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("got %v, want ErrInvalidRole", err)
	}
}

func TestSetUserRole_CannotSetSuperAdmin(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "testuser", model.RoleSuperAdmin)
	if err == nil {
		t.Fatal("expected error for SuperAdmin role")
	}
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("got %v, want ErrInvalidRole", err)
	}
}

func TestSetUserRole_SystemAdminPrefixRequired(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "john", model.RoleSystemAdmin)
	if err == nil {
		t.Fatal("expected error for missing systemadmin_ prefix")
	}
	if !errors.Is(err, ErrAdminUsernamePrefixRequired) {
		t.Fatalf("got %v, want ErrAdminUsernamePrefixRequired", err)
	}
}

func TestSetUserRole_SystemAdminValidPrefix(t *testing.T) {
	t.Parallel()

	called := false
	repo := &mockUserRepo{
		getByNameFn: func(ctx context.Context, username string) (model.User, error) {
			return model.User{Username: username, ID: 1}, nil
		},
		updateFn: func(ctx context.Context, user *model.User) error {
			called = true
			if user.Role != model.RoleSystemAdmin {
				t.Errorf("role = %v, want SystemAdmin", user.Role)
			}
			return nil
		},
	}
	svc := newTestService(t, repo)

	// "systemadmin" alone is valid
	_, err := svc.SetUserRole(context.Background(), "systemadmin", model.RoleSystemAdmin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("Update was not called")
	}

	// Both the exact role name and prefix are allowed
	called = false
	_, err = svc.SetUserRole(context.Background(), "systemadmin_john", model.RoleSystemAdmin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("Update was not called")
	}
}

func TestSetUserRole_SecurityAdminPrefixRequired(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "john", model.RoleSecurityAdmin)
	if err == nil {
		t.Fatal("expected error for missing securityadmin_ prefix")
	}
	if !errors.Is(err, ErrAdminUsernamePrefixRequired) {
		t.Fatalf("got %v, want ErrAdminUsernamePrefixRequired", err)
	}
}

func TestSetUserRole_SecurityAdminValidPrefix(t *testing.T) {
	t.Parallel()

	called := false
	repo := &mockUserRepo{
		getByNameFn: func(ctx context.Context, username string) (model.User, error) {
			return model.User{Username: username, ID: 1}, nil
		},
		updateFn: func(ctx context.Context, user *model.User) error {
			called = true
			return nil
		},
	}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "securityadmin_jane", model.RoleSecurityAdmin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("Update was not called")
	}
}

func TestSetUserRole_AuditAdminPrefixRequired(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "john", model.RoleAuditAdmin)
	if err == nil {
		t.Fatal("expected error for missing auditadmin_ prefix")
	}
	if !errors.Is(err, ErrAdminUsernamePrefixRequired) {
		t.Fatalf("got %v, want ErrAdminUsernamePrefixRequired", err)
	}
}

func TestSetUserRole_NormalUserNoPrefixCheck(t *testing.T) {
	t.Parallel()

	called := false
	repo := &mockUserRepo{
		getByNameFn: func(ctx context.Context, username string) (model.User, error) {
			return model.User{Username: username, ID: 1}, nil
		},
		updateFn: func(ctx context.Context, user *model.User) error {
			called = true
			return nil
		},
	}
	svc := newTestService(t, repo)

	// Normal user should not require any prefix
	_, err := svc.SetUserRole(context.Background(), "john", model.RoleNormal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("Update was not called")
	}
}

func TestSetUserRole_UserNotFound(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{
		getByNameFn: func(ctx context.Context, username string) (model.User, error) {
			return model.User{}, errors.New("user not found")
		},
	}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "systemadmin", model.RoleSystemAdmin)
	if err == nil {
		t.Fatal("expected error when user not found")
	}
	if !strings.Contains(err.Error(), "get user") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetUserRole_ClearsAdminScopes(t *testing.T) {
	t.Parallel()

	var updatedUser model.User
	repo := &mockUserRepo{
		getByNameFn: func(ctx context.Context, username string) (model.User, error) {
			return model.User{Username: username, ID: 1, AdminScopes: 0xFF}, nil
		},
		updateFn: func(ctx context.Context, user *model.User) error {
			updatedUser = *user
			return nil
		},
	}
	svc := newTestService(t, repo)

	_, err := svc.SetUserRole(context.Background(), "systemadmin", model.RoleSystemAdmin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updatedUser.AdminScopes != 0 {
		t.Errorf("AdminScopes = %d, want 0 (cleared)", updatedUser.AdminScopes)
	}
}

func TestListRoleMembers(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{
		listMembersByRoleFn: func(ctx context.Context, role model.Role, limit, offset int) ([]string, int64, error) {
			if role != model.RoleSecurityAdmin {
				t.Errorf("role = %v, want SecurityAdmin", role)
			}
			if limit != 50 || offset != 10 {
				t.Errorf("limit/offset = %d/%d, want 50/10", limit, offset)
			}
			return []string{"sec1", "sec2"}, 2, nil
		},
	}
	svc := newTestService(t, repo)

	members, total, err := svc.ListRoleMembers(context.Background(), model.RoleSecurityAdmin, 50, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 || len(members) != 2 || members[0] != "sec1" {
		t.Errorf("got members=%v total=%d, want [sec1 sec2] total=2", members, total)
	}
}

func TestListRoleMembers_InvalidRole(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	svc := newTestService(t, repo)

	_, _, err := svc.ListRoleMembers(context.Background(), model.Role(99), 50, 0)
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("got %v, want ErrInvalidRole", err)
	}
}
