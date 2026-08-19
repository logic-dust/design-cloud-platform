package session

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/settings"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"golang.org/x/crypto/bcrypt"
)

// fixedVerifier is a passwordVerifier that always returns a preset error.
type fixedVerifier struct{ err error }

func (f fixedVerifier) VerifyPassword(_ context.Context, _, _ string) error { return f.err }

// trackedSecMgr is a securityChecker with configurable IsBanned result and call tracking.
type trackedSecMgr struct {
	banned      bool
	bannedRec   security.BannedRecord
	addedCounts []string
	deletedKeys []string
}

func (f *trackedSecMgr) IsBanned(_ context.Context, _, _ string) (bool, security.BannedRecord) {
	return f.banned, f.bannedRec
}
func (f *trackedSecMgr) AddFailedCount(_ context.Context, keyName string, _ model.BanType, _, _ string) {
	f.addedCounts = append(f.addedCounts, keyName)
}
func (f *trackedSecMgr) DeleteByKey(_ context.Context, keyName string) {
	f.deletedKeys = append(f.deletedKeys, keyName)
}

// newUserStoreForTest creates an isolated SQLite-backed UserStore for tests.
func newUserStoreForTest(t *testing.T) *db.UserRepo {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "users.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db.NewRepos(store).User
}

func newAuthManagerWithMocks(t *testing.T, userStore *db.UserRepo, verifier passwordVerifier, sec securityChecker) *AuthManager {
	t.Helper()
	tmpDir := t.TempDir()
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  tmpDir + "/audit.log",
		SecurityLogPath: tmpDir + "/security.log",
	})
	if err != nil {
		t.Fatalf("audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })
	mgr, err := NewAuthManager(userStore, verifier, sec, slog.New(slog.NewTextHandler(io.Discard, nil)), auditSvc, false)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return mgr
}

func TestAuthSideEffectContextIgnoresParentCancellation(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := authSideEffectContext(parent)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("side-effect context was canceled with parent: %v", ctx.Err())
	default:
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatalf("side-effect context has no timeout")
	}
}

// noopProvider stubs userprovider.Provider — app-user auth bypasses it entirely.
type noopProvider struct{}

func (noopProvider) GetUser(_ context.Context, _ string) (*userprovider.SystemUser, error) {
	return nil, userprovider.ErrUserNotFound
}
func (noopProvider) ListUsers(_ context.Context) ([]userprovider.SystemUser, error) { return nil, nil }
func (noopProvider) ListGroups(_ context.Context) (map[string][]string, error)      { return nil, nil }
func (noopProvider) GetGroupMembers(_ context.Context, _ string) ([]string, error)  { return nil, nil }
func (noopProvider) ListUserGroups(_ context.Context) (map[string][]string, error)  { return nil, nil }
func (noopProvider) UsersByIDs(_ context.Context, _ []uint32) (map[uint32]string, error) {
	return map[uint32]string{}, nil
}
func (noopProvider) GroupsByIDs(_ context.Context, _ []uint32) (map[uint32]string, error) {
	return map[uint32]string{}, nil
}
func (noopProvider) VerifyPassword(_ context.Context, _, _ string) error {
	return userprovider.ErrUserNotFound
}
func (noopProvider) ChangePassword(_ context.Context, _, _, _ string) error { return nil }
func (noopProvider) ResetPassword(_ context.Context, _, _ string) error     { return nil }

// newAuthManagerForTest creates a test environment for auth manager testing.
func newAuthManagerForTest(t *testing.T) (*AuthManager, *db.SessionRepo, *db.UserRepo) {
	t.Helper()

	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tmpDir, "test.db"),
	}, logger)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repos := db.NewRepos(store)

	// Minimal audit service with temp file paths
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tmpDir, "audit.log"),
		SecurityLogPath: filepath.Join(tmpDir, "security.log"),
		NodeID:          "test-node",
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	// Real settings service backed by the same DB
	settingsSvc, err := settings.NewService(repos.Setting, auditSvc, nil)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}

	// Security manager with real settings
	authFailStore, err := security.NewBanCache(repos.Ban, logger, nil)
	if err != nil {
		t.Fatalf("new auth failure store: %v", err)
	}
	secMgr, err := security.NewService(settingsSvc, authFailStore, logger)
	if err != nil {
		t.Fatalf("new security manager: %v", err)
	}

	// passwordVerifier implements passwordVerifier interface via noopProvider
	authMgr, err := NewAuthManager(repos.User, noopProvider{}, secMgr, logger, auditSvc, false)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}

	return authMgr, repos.Session, repos.User
}

// TestAuthAppUserSuccess verifies that a valid app-user password succeeds.
func TestAuthAppUserSuccess(t *testing.T) {
	t.Parallel()

	authMgr, _, userStore := newAuthManagerForTest(t)
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	u := &model.User{
		Username: "appuser",
		Password: string(hash),
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
	}
	if err := userStore.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	_, err = authMgr.Authenticate(ctx, "appuser", "correct-password", "10.0.0.1")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

// TestAuthAppUserWrongPassword verifies that an incorrect password is rejected.
func TestAuthAppUserWrongPassword(t *testing.T) {
	t.Parallel()

	authMgr, _, userStore := newAuthManagerForTest(t)
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("real-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	user := &model.User{
		Username: "appuser2",
		Password: string(hash),
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
	}
	if err := userStore.Create(ctx, user); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	_, err = authMgr.Authenticate(ctx, "appuser2", "wrong-password", "10.0.0.1")
	if err == nil {
		t.Fatal("Authenticate: expected error for wrong password, got nil")
	}
}

// TestAuthRootIsBlocked verifies that root login is explicitly rejected.
func TestAuthRootIsBlocked(t *testing.T) {
	t.Parallel()

	authMgr, _, _ := newAuthManagerForTest(t)
	_, err := authMgr.Authenticate(context.Background(), "root", "anything", "10.0.0.1")
	if err == nil {
		t.Fatal("Authenticate as root: expected error, got nil")
	}
}

// TestAuthNonExistentUser verifies that a non-existent user gets a generic error.
func TestAuthNonExistentUser(t *testing.T) {
	t.Parallel()

	authMgr, _, _ := newAuthManagerForTest(t)
	_, err := authMgr.Authenticate(context.Background(), "ghost", "password", "10.0.0.1")
	if err == nil {
		t.Fatal("Authenticate non-existent user: expected error, got nil")
	}
}

// TestAuth_BannedIP verifies that a banned IP is rejected before any user lookup.
func TestAuth_BannedIP(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	sec := &trackedSecMgr{
		banned:    true,
		bannedRec: security.BannedRecord{Reason: "too many failures"},
	}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{}, sec)

	_, err := mgr.Authenticate(context.Background(), "alice", "pw", "1.2.3.4")
	if err == nil || err.Error() != "too many failures" {
		t.Fatalf("expected ban reason error, got: %v", err)
	}
}

// TestAuth_AppUser_Inactive verifies that an inactive app user is rejected and
// failure counters are incremented.
func TestAuth_AppUser_Inactive(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	user := &model.User{Username: "inactive-app", Password: string(hash), Type: model.UserTypeApp, Status: model.UserDisabled}
	if err := store.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	sec := &trackedSecMgr{}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{}, sec)

	_, err := mgr.Authenticate(context.Background(), "inactive-app", "pw", "1.2.3.4")
	if !errors.Is(err, userprovider.ErrUserNotActive) {
		t.Fatalf("error = %v, want ErrUserNotActive", err)
	}
	if len(sec.addedCounts) == 0 {
		t.Fatal("expected AddFailedCount for inactive user")
	}
}

// TestAuth_AppUser_PasswordExpired verifies the PasswordMustChange flag.
func TestAuth_AppUser_PasswordExpired(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	const pwd = "pw"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.MinCost)
	user := &model.User{Username: "expiredapp", Password: string(hash), Type: model.UserTypeApp, Status: model.UserPasswordExpired}
	if err := store.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	sec := &trackedSecMgr{}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{}, sec)

	result, err := mgr.Authenticate(context.Background(), "expiredapp", pwd, "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.PasswordMustChange {
		t.Fatal("expected PasswordMustChange = true for expired app user")
	}
}

// TestAuth_OSUser_PasswordExpired verifies verifier ErrUserPasswordExpired → PasswordMustChange.
func TestAuth_OSUser_PasswordExpired(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	sec := &trackedSecMgr{}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{err: userprovider.ErrUserPasswordExpired}, sec)

	result, err := mgr.Authenticate(context.Background(), "osuser", "pw", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.PasswordMustChange {
		t.Fatal("expected PasswordMustChange = true for OS user password expired")
	}
}

// TestAuth_OSUser_NotFound verifies not-found is masked as wrong-password to avoid enumeration.
func TestAuth_OSUser_NotFound(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	sec := &trackedSecMgr{}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{err: userprovider.ErrUserNotFound}, sec)

	_, err := mgr.Authenticate(context.Background(), "ghost", "pw", "1.2.3.4")
	if !errors.Is(err, userprovider.ErrUserPasswordIncorrect) {
		t.Fatalf("error = %v, want ErrUserPasswordIncorrect (not-found should be masked)", err)
	}
	if len(sec.addedCounts) == 0 {
		t.Fatal("expected IP AddFailedCount for not-found user")
	}
}

// TestAuth_OSUser_WrongPassword verifies both user+IP counters are incremented.
func TestAuth_OSUser_WrongPassword(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	sec := &trackedSecMgr{}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{err: userprovider.ErrUserPasswordIncorrect}, sec)

	_, err := mgr.Authenticate(context.Background(), "osuser", "wrong", "1.2.3.4")
	if !errors.Is(err, userprovider.ErrUserPasswordIncorrect) {
		t.Fatalf("error = %v, want ErrUserPasswordIncorrect", err)
	}
	if len(sec.addedCounts) < 2 {
		t.Fatalf("expected at least 2 AddFailedCount calls, got %d", len(sec.addedCounts))
	}
}

// TestAuth_OSUser_Success verifies successful OS login clears failure counters.
func TestAuth_OSUser_Success(t *testing.T) {
	t.Parallel()

	store := newUserStoreForTest(t)
	sec := &trackedSecMgr{}
	mgr := newAuthManagerWithMocks(t, store, fixedVerifier{err: nil}, sec)

	result, err := mgr.Authenticate(context.Background(), "osuser", "pw", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PasswordMustChange {
		t.Fatal("expected PasswordMustChange = false for successful OS login")
	}
	if len(sec.deletedKeys) == 0 {
		t.Fatal("expected DeleteByKey after successful OS login")
	}
}
