package user

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// setupTestCore creates a Service backed by an in-memory SQLite store for tests.
func setupTestCore(t *testing.T) (*Service, *db.Repos, *settings.Service, *db.Store) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repos := db.NewRepos(store)

	tmp := t.TempDir()
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tmp, "audit.log"),
		SecurityLogPath: filepath.Join(tmp, "security.log"),
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}

	svc := &Service{
		provider:       nil, // caller sets via core.provider = mockProv
		logger:         slog.Default(),
		userRepo:       repos.User,
		leaseRepo:      repos.TaskLease,
		cacheVersion:   repos.CacheVersion,
		auditSvc:       auditSvc,
		authSettings:   settingsSvc,
		idMappingCache: newIDMappingCache(),
	}
	return svc, repos, settingsSvc, store
}

type mockProvider struct {
	getUserFunc        func(ctx context.Context, username string) (*userprovider.SystemUser, error)
	listUsersFunc      func(ctx context.Context) ([]userprovider.SystemUser, error)
	usersByIDsFunc     func(ctx context.Context, uids []uint32) (map[uint32]string, error)
	groupsByIDsFunc    func(ctx context.Context, gids []uint32) (map[uint32]string, error)
	changePasswordFunc func(ctx context.Context, username, oldPassword, newPassword string) error
	resetPasswordFunc  func(ctx context.Context, username, newPassword string) error
}

func (m *mockProvider) GetUser(ctx context.Context, username string) (*userprovider.SystemUser, error) {
	if m.getUserFunc != nil {
		return m.getUserFunc(ctx, username)
	}
	return nil, nil
}

func (m *mockProvider) ListUsers(ctx context.Context) ([]userprovider.SystemUser, error) {
	if m.listUsersFunc != nil {
		return m.listUsersFunc(ctx)
	}
	return nil, nil
}

func (m *mockProvider) ListGroups(ctx context.Context) (map[string][]string, error) {
	return nil, nil
}

func (m *mockProvider) ListUserGroups(ctx context.Context) (map[string][]string, error) {
	return nil, nil
}

func (m *mockProvider) GetGroupMembers(ctx context.Context, groupName string) ([]string, error) {
	return nil, nil
}

func (m *mockProvider) UsersByIDs(ctx context.Context, uids []uint32) (map[uint32]string, error) {
	if m.usersByIDsFunc != nil {
		return m.usersByIDsFunc(ctx, uids)
	}
	return map[uint32]string{}, nil
}

func (m *mockProvider) GroupsByIDs(ctx context.Context, gids []uint32) (map[uint32]string, error) {
	if m.groupsByIDsFunc != nil {
		return m.groupsByIDsFunc(ctx, gids)
	}
	return map[uint32]string{}, nil
}

func (m *mockProvider) VerifyPassword(ctx context.Context, username, password string) error {
	return nil
}

func (m *mockProvider) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	if m.changePasswordFunc != nil {
		return m.changePasswordFunc(ctx, username, oldPassword, newPassword)
	}
	return nil
}

func (m *mockProvider) ResetPassword(ctx context.Context, username, newPassword string) error {
	if m.resetPasswordFunc != nil {
		return m.resetPasswordFunc(ctx, username, newPassword)
	}
	return nil
}

func TestServiceGet_AppUserFromDB(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	providerCalled := false
	mockProv := &mockProvider{
		getUserFunc: func(ctx context.Context, username string) (*userprovider.SystemUser, error) {
			providerCalled = true
			return nil, nil
		},
	}
	core.provider = mockProv

	// App user exists in DB only (no system account).
	u := model.User{
		Username: "appuser",
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
	}
	require.NoError(t, repos.User.Create(context.Background(), &u))

	got, err := core.Get(context.Background(), "appuser")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "appuser", got.Username)
	assert.Equal(t, uint(1), got.ID)
	assert.Equal(t, "active", got.Status)
	assert.False(t, providerCalled, "app user must not hit the system provider")
}

func TestServiceGet_SystemUserDelegatesToProvider(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	want := &userprovider.SystemUser{
		ID:       42,
		Username: "sysuser",
		Uid:      "1001",
		Gid:      "1001",
		Home:     "/home/sysuser",
		Shell:    "/bin/bash",
		Status:   "active",
	}
	mockProv := &mockProvider{
		getUserFunc: func(ctx context.Context, username string) (*userprovider.SystemUser, error) {
			return want, nil
		},
	}
	core.provider = mockProv

	// System user exists in DB as UserTypeSystem.
	u := model.User{
		Username: "sysuser",
		Status:   model.UserActive,
		Type:     model.UserTypeSystem,
	}
	require.NoError(t, repos.User.Create(context.Background(), &u))

	got, err := core.Get(context.Background(), "sysuser")
	require.NoError(t, err)
	assert.Same(t, want, got, "system user must come from the provider")
}

func TestServiceResetPassword_AppUser(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	mockProv := &mockProvider{}
	core.provider = mockProv

	username := "appuser"
	// Create app user in DB
	u := model.User{
		Username: username,
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
		Password: "hashed_password",
	}
	err := repos.User.Create(context.Background(), &u)
	require.NoError(t, err)

	core.provider = mockProv
	svc := core
	operator := &model.Session{Role: model.RoleSuperAdmin}
	res, err := svc.AdminResetPassword(context.Background(), operator, username)
	assert.NoError(t, err)
	assert.NotEmpty(t, res)

	// Verify status in DB
	dbUser, err := repos.User.GetByName(context.Background(), username)
	assert.NoError(t, err)
	assert.Equal(t, model.UserPasswordExpired, dbUser.Status)
}

func TestServiceResetPassword_SystemUser_Exists(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	mockProv := &mockProvider{}
	core.provider = mockProv

	username := "sysuser"
	// Create system user in DB
	u := model.User{
		Username: username,
		Status:   model.UserActive,
		Type:     model.UserTypeSystem,
	}
	err := repos.User.Create(context.Background(), &u)
	require.NoError(t, err)

	mockProv.resetPasswordFunc = func(ctx context.Context, uname, newPwd string) error {
		return nil
	}

	core.provider = mockProv
	svc := core
	operator := &model.Session{Role: model.RoleSuperAdmin}
	res, err := svc.AdminResetPassword(context.Background(), operator, username)
	assert.NoError(t, err)
	assert.NotEmpty(t, res)

	// Verify status in DB
	dbUser, err := repos.User.GetByName(context.Background(), username)
	assert.NoError(t, err)
	assert.Equal(t, model.UserPasswordExpired, dbUser.Status)
}

func TestServiceResetPassword_SystemUser_NotExists(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	mockProv := &mockProvider{}
	core.provider = mockProv

	username := "newsysuser"

	mockProv.resetPasswordFunc = func(ctx context.Context, uname, newPwd string) error {
		return nil
	}

	core.provider = mockProv
	svc := core
	operator := &model.Session{Role: model.RoleSuperAdmin}
	res, err := svc.AdminResetPassword(context.Background(), operator, username)
	assert.NoError(t, err)
	assert.NotEmpty(t, res)

	// Verify user created in DB
	dbUser, err := repos.User.GetByName(context.Background(), username)
	assert.NoError(t, err)
	assert.Equal(t, model.UserPasswordExpired, dbUser.Status)
	assert.Equal(t, model.UserTypeSystem, dbUser.Type)
}

func TestServiceChangePassword_AppUser_Success(t *testing.T) {
	t.Parallel()
	core, repos, settingsSvc, _ := setupTestCore(t)

	username := "appuser"
	oldPwd := "old_password"
	newPwd := "new_password123"

	hashedOldPwd, err := bcrypt.GenerateFromPassword([]byte(oldPwd), bcrypt.DefaultCost)
	require.NoError(t, err)

	u := model.User{
		Username: username,
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
		Password: string(hashedOldPwd),
	}
	err = repos.User.Create(context.Background(), &u)
	require.NoError(t, err)

	// Set valid auth settings
	authJSON := []byte(`{
		"userBanDuration": 600,
		"ipBanDuration":   3600,
		"sessionTTL":      3600,
		"maxIPLoginAttempts": 15,
		"maxUserLoginAttempts": 7,
		"maxSessionsPerUser": 5,
		"idMappingFoundCacheTTL": 86400,
		"idMappingNotFoundCacheTTL": 600
	}`)
	err = settingsSvc.UpdateByDomain(context.Background(), schema.DomainAuth, authJSON)
	require.NoError(t, err)

	svc := core
	err = svc.ChangePassword(context.Background(), username, oldPwd, newPwd)
	assert.NoError(t, err)

	// Verify password updated in DB
	dbUser, err := repos.User.GetByName(context.Background(), username)
	assert.NoError(t, err)

	err = bcrypt.CompareHashAndPassword([]byte(dbUser.Password), []byte(newPwd))
	assert.NoError(t, err)
}

func TestServiceChangePassword_EvictsSessions(t *testing.T) {
	t.Parallel()

	// App user path
	t.Run("app user", func(t *testing.T) {
		t.Parallel()
		core, repos, settingsSvc, _ := setupTestCore(t)
		evicted := false
		core.deleteSessionsByUsername = func(_ context.Context, username string) error {
			evicted = true
			assert.Equal(t, "appuser", username)
			return nil
		}

		username, oldPwd, newPwd := "appuser", "old_password", "new_password123"
		hashed, err := bcrypt.GenerateFromPassword([]byte(oldPwd), bcrypt.DefaultCost)
		require.NoError(t, err)
		require.NoError(t, repos.User.Create(context.Background(), &model.User{
			Username: username, Status: model.UserActive,
			Type: model.UserTypeApp, Password: string(hashed),
		}))
		require.NoError(t, settingsSvc.UpdateByDomain(context.Background(), schema.DomainAuth,
			[]byte(`{"userBanDuration":600,"ipBanDuration":3600,"sessionTTL":3600,"maxIPLoginAttempts":15,"maxUserLoginAttempts":7,"maxSessionsPerUser":5,"idMappingFoundCacheTTL":86400,"idMappingNotFoundCacheTTL":600}`)))

		require.NoError(t, core.ChangePassword(context.Background(), username, oldPwd, newPwd))
		assert.True(t, evicted, "app user password change must evict sessions")
	})

	// System user path
	t.Run("system user", func(t *testing.T) {
		t.Parallel()
		core, repos, _, _ := setupTestCore(t)
		evicted := false
		core.deleteSessionsByUsername = func(_ context.Context, username string) error {
			evicted = true
			assert.Equal(t, "sysuser", username)
			return nil
		}
		core.provider = &mockProvider{
			changePasswordFunc: func(ctx context.Context, username, oldPassword, newPassword string) error {
				return nil
			},
		}

		require.NoError(t, repos.User.Create(context.Background(), &model.User{
			Username: "sysuser", Status: model.UserActive, Type: model.UserTypeSystem,
		}))

		require.NoError(t, core.ChangePassword(context.Background(), "sysuser", "old", "new123456"))
		assert.True(t, evicted, "system user password change must evict sessions")
	})
}

func TestServiceChangePassword_AppUser_WrongOldPassword(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	username := "appuser"
	oldPwd := "old_password"
	wrongOldPwd := "wrong_password"
	newPwd := "new_password123"

	hashedOldPwd, err := bcrypt.GenerateFromPassword([]byte(oldPwd), bcrypt.DefaultCost)
	require.NoError(t, err)

	u := model.User{
		Username: username,
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
		Password: string(hashedOldPwd),
	}
	err = repos.User.Create(context.Background(), &u)
	require.NoError(t, err)

	svc := core
	err = svc.ChangePassword(context.Background(), username, wrongOldPwd, newPwd)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, userprovider.ErrUserPasswordIncorrect))
}

func TestServiceChangePassword_SystemUser_Success(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	mockProv := &mockProvider{}
	core.provider = mockProv

	username := "sysuser"
	// Create system user in DB
	u := model.User{
		Username: username,
		Status:   model.UserActive,
		Type:     model.UserTypeSystem,
	}
	err := repos.User.Create(context.Background(), &u)
	require.NoError(t, err)

	mockProv.changePasswordFunc = func(ctx context.Context, uname, old, newPwd string) error {
		return nil
	}

	core.provider = mockProv
	svc := core
	err = svc.ChangePassword(context.Background(), username, "old_pwd", "new_pwd")
	assert.NoError(t, err)
}

func TestServiceAdminUpdate_Success(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	username := "testuser"
	u := model.User{
		Username:       username,
		Status:         model.UserActive,
		DownloadAccess: true,
		UploadAccess:   true,
	}
	err := repos.User.Create(context.Background(), &u)
	require.NoError(t, err)

	req := AdminUserUpdateRequest{
		Status:         new(int(model.UserDisabled)),
		DownloadAccess: new(false),
		UploadAccess:   new(false),
		MaxConnections: new(uint(5)),
	}

	svc := core
	operator := &model.Session{Role: model.RoleSuperAdmin}
	res, err := svc.AdminUpdate(context.Background(), operator, username, req)
	assert.NoError(t, err)
	assert.Equal(t, username, res.Username)
	assert.Equal(t, model.UserDisabled, res.Status)

	// Verify in DB
	dbUser, err := repos.User.GetByName(context.Background(), username)
	assert.NoError(t, err)
	assert.Equal(t, model.UserDisabled, dbUser.Status)
	assert.False(t, dbUser.DownloadAccess)
	assert.False(t, dbUser.UploadAccess)
	assert.Equal(t, uint(5), dbUser.MaxConnections)

	// Test reset to inherit (0)
	reqReset := AdminUserUpdateRequest{
		MaxConnections: new(uint(0)),
	}
	_, err = svc.AdminUpdate(context.Background(), operator, username, reqReset)
	assert.NoError(t, err)

	dbUserReset, err := repos.User.GetByName(context.Background(), username)
	assert.NoError(t, err)
	assert.Equal(t, uint(0), dbUserReset.MaxConnections)

	// Test invalid value
	reqInvalid := AdminUserUpdateRequest{
		MaxConnections: new(uint(101)),
	}
	_, err = svc.AdminUpdate(context.Background(), operator, username, reqInvalid)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "maxConnections must be 0")
}

// ---------------------------------------------------------------------------
// Scope tests (SetScopes / SetScopesByNames)
// ---------------------------------------------------------------------------

func TestSetScope_RejectsUnknownBit(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	u := model.User{Username: "invalidscope", Status: model.UserActive, Role: model.RoleNormal}
	require.NoError(t, repos.User.Create(context.Background(), &u))

	err := core.SetScopes(context.Background(), "invalidscope", model.AdminScopes(1<<13))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrInvalidScope))
}

type mockSessionInvalidator struct {
	deleteFn func(context.Context, string) error
}

func (m mockSessionInvalidator) InvalidateByUsername(username string) {}
func (m mockSessionInvalidator) Delete(ctx context.Context, username string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, username)
	}
	return nil
}

func TestSetScope_DeletesExistingSessions(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)
	ctx := context.Background()

	u := model.User{Username: "scoped_user", Status: model.UserActive, Role: model.RoleNormal}
	require.NoError(t, repos.User.Create(ctx, &u))
	dbUser, err := repos.User.GetByName(ctx, "scoped_user")
	require.NoError(t, err)

	require.NoError(t, repos.Session.Create(ctx, &model.Session{
		UserID:    dbUser.ID,
		Username:  dbUser.Username,
		Token:     "scoped_user-token",
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	revoker := mockSessionInvalidator{
		deleteFn: func(ctx context.Context, username string) error {
			return repos.Session.DeleteByUsername(ctx, username)
		},
	}
	core.deleteSessionsByUsername = revoker.Delete
	core.invalidateSessionsByUsername = revoker.InvalidateByUsername

	err = core.SetScopes(ctx, "scoped_user", model.AdminScopes(model.ScopeAll))
	require.NoError(t, err)

	count, err := repos.Session.CountByUser(ctx, dbUser.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestSetPermissionsByNames(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	u := model.User{Username: "namedscope", Status: model.UserActive, Role: model.RoleNormal}
	require.NoError(t, repos.User.Create(context.Background(), &u))

	err := core.SetScopesByNames(context.Background(), "namedscope", []string{"systemadmin"})
	require.NoError(t, err)

	dbUser, err := repos.User.GetByName(context.Background(), "namedscope")
	require.NoError(t, err)
	assert.Equal(t, model.AdminScopes(model.ScopeAll), dbUser.AdminScopes)
}

func TestSetScope_RejectsAdminRole(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	u := model.User{Username: "adminuser", Status: model.UserActive, Role: model.RoleSystemAdmin}
	require.NoError(t, repos.User.Create(context.Background(), &u))

	err := core.SetScopes(context.Background(), "adminuser", model.AdminScopes(model.ScopeAll))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrScopeRequiresNormalRole))
}

func TestSetScope_AllowsClearingAdminRole(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)

	u := model.User{
		Username:    "adminclear",
		Status:      model.UserActive,
		Role:        model.RoleSystemAdmin,
		AdminScopes: model.AdminScopes(model.ScopeAll),
	}
	require.NoError(t, repos.User.Create(context.Background(), &u))

	err := core.SetScopesByNames(context.Background(), "adminclear", nil)
	require.NoError(t, err)

	dbUser, err := repos.User.GetByName(context.Background(), "adminclear")
	require.NoError(t, err)
	assert.Equal(t, model.AdminScopes(0), dbUser.AdminScopes)
}
