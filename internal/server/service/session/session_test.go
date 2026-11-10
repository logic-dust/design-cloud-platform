package session

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/push"
	"design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/settings"
	usersvc "design-cloud-engine/internal/server/service/user"

	"golang.org/x/crypto/bcrypt"
)

func TestCreateAllowsMultipleSessionsAndEvictsOldestAtLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repos := newSessionServiceForMultiSessionTest(t)
	const username = "multi-device"
	const password = "correct-password"

	createAppUserForSessionTest(t, repos.User, username, password)
	setMaxSessionsPerUserForTest(t, repos.Setting, 3)

	var tokens []string
	for i := range 4 {
		payload, err := svc.Create(ctx, username, password, LoginClient{IP: "10.0.0.1"}, false, nil)
		if err != nil {
			t.Fatalf("create session #%d: %v", i+1, err)
		}
		if !payload.MultiSessionEnabled {
			t.Fatal("multiSessionEnabled = false, want true when maxSessionsPerUser = 3")
		}
		if multi, err := svc.MultiSessionEnabled(ctx); err != nil || !multi {
			t.Fatalf("MultiSessionEnabled = %v, %v; want true", multi, err)
		}
		tokens = append(tokens, payload.Token)
	}

	session, err := svc.GetByToken(ctx, tokens[1])
	if err != nil {
		t.Fatalf("get user session: %v", err)
	}
	active, err := repos.Session.GetByUserID(ctx, session.UserID)
	if err != nil {
		t.Fatalf("list active sessions: %v", err)
	}
	if got := len(active); got != 3 {
		t.Fatalf("active sessions = %d, want 3", got)
	}

	if _, err := svc.GetByToken(ctx, tokens[0]); !errors.Is(err, ErrInvalidSessionToken) {
		t.Fatalf("oldest token error = %v, want ErrInvalidSessionToken", err)
	}

	for i := 1; i < len(tokens); i++ {
		if _, err := svc.GetByToken(ctx, tokens[i]); err != nil {
			t.Fatalf("session #%d token should remain valid: %v", i+1, err)
		}
	}
}

func TestCreateIncludesPlatformDisabledUserPortalModules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repos := newSessionServiceForMultiSessionTest(t)
	const username = "disabled-modules"
	const password = "correct-password"

	createAppUserForSessionTest(t, repos.User, username, password)
	setPlatformDisabledModulesForTest(t, repos.Setting, "ai", "ticket", "file", "app")

	payload, err := svc.Create(ctx, username, password, LoginClient{IP: "10.0.0.1"}, false, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	got := payload.UI.UserPortal.Modules
	want := []string{"ai", "app", "file", "ticket"}
	if !slices.Equal(got, want) {
		t.Fatalf("userPortal.modules = %v, want %v", got, want)
	}
}

func newSessionServiceForMultiSessionTest(t *testing.T) (*Service, *db.Repos) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "sessions.db"),
	}, logger)
	if err != nil {
		t.Fatalf("new db store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repos := db.NewRepos(store)

	tmpDir := t.TempDir()
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tmpDir, "audit.log"),
		SecurityLogPath: filepath.Join(tmpDir, "security.log"),
		NodeID:          "test-node",
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}

	authFailureStore, err := security.NewBanCache(repos.Ban, logger, nil)
	if err != nil {
		t.Fatalf("new auth failure store: %v", err)
	}
	t.Cleanup(authFailureStore.Stop)

	securitySvc, err := security.NewService(settingsSvc, authFailureStore, logger)
	if err != nil {
		t.Fatalf("new security manager: %v", err)
	}

	userSvc, err := usersvc.NewService(nil, usersvc.Config{
		AuthMode: config.UserAuthModeLDAP,
	}, settingsSvc, repos.User, repos.TaskLease, repos.CacheVersion, nil, logger, auditSvc, nil)
	if err != nil {
		t.Fatalf("new user service: %v", err)
	}

	authMgr, err := NewAuthManager(repos.User, noopProvider{}, securitySvc, logger, auditSvc, false)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}

	evictor, err := NewEvictor(logger, repos.Session, nil, nil)
	if err != nil {
		t.Fatalf("new session evictor: %v", err)
	}

	sessionSvc, err := NewService(logger, authMgr, userSvc, securitySvc, settingsSvc, repos.Session, evictor, push.NoopPublisher{})
	if err != nil {
		t.Fatalf("new session service: %v", err)
	}
	return sessionSvc, repos
}

func createAppUserForSessionTest(t *testing.T, store *db.UserRepo, username, password string) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	user := &model.User{
		Username: username,
		Password: string(hash),
		Status:   model.UserActive,
		Type:     model.UserTypeApp,
		Role:     model.RoleNormal,
	}
	if err := store.Create(context.Background(), user); err != nil {
		t.Fatalf("create app user: %v", err)
	}
}

func setMaxSessionsPerUserForTest(t *testing.T, store *db.SettingRepo, maxSessions int) {
	t.Helper()

	// 从 schema 默认值出发：auth 是整体契约，零值结构体（SessionTTL=0 等）无法通过
	// 校验，读取层会整体回退默认值，测试意图（只改 MaxSessionsPerUser）会失效。
	var auth schema.AuthSettings
	if def, ok := schema.DefaultSettings(schema.DomainAuth); ok {
		auth, _ = def.(schema.AuthSettings)
	}
	if err := store.Load(context.Background(), schema.DomainAuth.String(), &auth); err != nil {
		t.Fatalf("load auth settings: %v", err)
	}
	auth.MaxSessionsPerUser = maxSessions
	if err := store.Save(context.Background(), schema.DomainAuth.String(), auth); err != nil {
		t.Fatalf("save auth settings: %v", err)
	}
}

func setPlatformDisabledModulesForTest(t *testing.T, store *db.SettingRepo, disabled ...string) {
	t.Helper()

	defaultValue, ok := schema.DefaultSettings(schema.DomainPlatform)
	if !ok {
		t.Fatal("platform defaults missing")
	}
	platform := defaultValue.(schema.PlatformSettings)
	for _, module := range disabled {
		platform.DisabledModules[module] = true
	}
	if err := store.Save(context.Background(), schema.DomainPlatform.String(), platform); err != nil {
		t.Fatalf("save platform settings: %v", err)
	}
}

func TestDeleteOwnedByIDs_And_DeleteByIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repos := newSessionServiceForMultiSessionTest(t)
	const userA = "user-a"
	const userB = "user-b"
	const password = "password123"

	createAppUserForSessionTest(t, repos.User, userA, password)
	createAppUserForSessionTest(t, repos.User, userB, password)
	setMaxSessionsPerUserForTest(t, repos.Setting, 5)

	sA1, _ := svc.Create(ctx, userA, password, LoginClient{IP: "10.0.0.1"}, false, nil)
	sA2, _ := svc.Create(ctx, userA, password, LoginClient{IP: "10.0.0.2"}, false, nil)
	sB1, _ := svc.Create(ctx, userB, password, LoginClient{IP: "10.0.0.3"}, false, nil)

	sessA1, _ := svc.GetByToken(ctx, sA1.Token)
	sessA2, _ := svc.GetByToken(ctx, sA2.Token)
	sessB1, _ := svc.GetByToken(ctx, sB1.Token)

	// User A cannot delete User B's session
	err := svc.DeleteOwnedByIDs(ctx, sessA1.UserID, sessA1.ID, sessB1.ID)
	if !errors.Is(err, ErrSessionAccessDenied) {
		t.Fatalf("expected ErrSessionAccessDenied, got %v", err)
	}

	// User A deletes own sessions in batch
	err = svc.DeleteOwnedByIDs(ctx, sessA1.UserID, sessA1.ID, sessA2.ID)
	if err != nil {
		t.Fatalf("user A batch delete owned failed: %v", err)
	}

	// Admin deletes remaining User B session
	err = svc.DeleteByIDs(ctx, sessB1.ID)
	if err != nil {
		t.Fatalf("admin batch delete failed: %v", err)
	}
}

func TestCreateRecordsClientInfoAndMultiSessionFlag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repos := newSessionServiceForMultiSessionTest(t)
	const username = "client-info"
	const password = "correct-password"

	createAppUserForSessionTest(t, repos.User, username, password)
	setMaxSessionsPerUserForTest(t, repos.Setting, 1)

	longHost := "  " + strings.Repeat("h", 300) + "  "
	payload, err := svc.Create(ctx, username, password, LoginClient{IP: "10.0.0.9", Hostname: longHost, Platform: " windows ", OSVersion: " 10.0.19045 ", AppVersion: " v2.3.1 ", DeviceID: " device-abc "}, false, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if payload.MultiSessionEnabled {
		t.Fatal("multiSessionEnabled = true, want false when maxSessionsPerUser = 1")
	}
	session, err := svc.GetByToken(ctx, payload.Token)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got := len([]rune(session.Hostname)); got != model.MaxSessionHostnameLength {
		t.Fatalf("hostname length = %d, want %d", got, model.MaxSessionHostnameLength)
	}
	if session.Platform != "windows" {
		t.Fatalf("platform = %q, want trimmed value", session.Platform)
	}
	if session.OSVersion != "10.0.19045" {
		t.Fatalf("osVersion = %q, want trimmed value", session.OSVersion)
	}
	if session.AppVersion != "v2.3.1" {
		t.Fatalf("appVersion = %q, want trimmed value", session.AppVersion)
	}
	if session.DeviceID != "device-abc" {
		t.Fatalf("deviceId = %q, want trimmed value", session.DeviceID)
	}

}
