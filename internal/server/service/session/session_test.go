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
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
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
	setMaxLoginSessionsPerUserForTest(t, repos.Setting, 3)

	var tokens []string
	for i := range 4 {
		payload, err := svc.Create(ctx, username, password, "10.0.0.1", false, nil)
		if err != nil {
			t.Fatalf("create session #%d: %v", i+1, err)
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

	settingsSvc, err := settings.NewService(repos.Setting, auditSvc, nil)
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
	}, settingsSvc, repos.User, repos.TaskLease, repos.CacheVersion, nil, logger, auditSvc)
	if err != nil {
		t.Fatalf("new user service: %v", err)
	}

	authMgr, err := NewAuthManager(repos.User, noopProvider{}, securitySvc, logger, auditSvc, false)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}

	sessionSvc, err := NewService(logger, authMgr, userSvc, securitySvc, settingsSvc, repos.Session, nil, push.NoopPublisher{})
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

func setMaxLoginSessionsPerUserForTest(t *testing.T, store *db.SettingRepo, maxLoginSessions int) {
	t.Helper()

	// 从 schema 默认值出发：auth 是整体契约，零值结构体（SessionTTL=0 等）无法通过
	// 校验，读取层会整体回退默认值，测试意图（只改 MaxLoginSessionsPerUser）会失效。
	var auth schema.AuthSettings
	if def, ok := schema.DefaultSettings(schema.DomainAuth); ok {
		auth, _ = def.(schema.AuthSettings)
	}
	if err := store.Load(context.Background(), schema.DomainAuth.String(), &auth); err != nil {
		t.Fatalf("load auth settings: %v", err)
	}
	auth.MaxLoginSessionsPerUser = maxLoginSessions
	if err := store.Save(context.Background(), schema.DomainAuth.String(), auth); err != nil {
		t.Fatalf("save auth settings: %v", err)
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
	setMaxLoginSessionsPerUserForTest(t, repos.Setting, 10)

	sA1, _ := svc.Create(ctx, userA, password, "10.0.0.1", false, nil)
	sA2, _ := svc.Create(ctx, userA, password, "10.0.0.2", false, nil)
	sB1, _ := svc.Create(ctx, userB, password, "10.0.0.3", false, nil)

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
