package ticket

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
)

// setupTestCore 创建测试基础设施：SQLite DB + Repos + Audit + Ticket Service
func setupTestCore(t *testing.T) (*Service, *db.Repos) {
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

	svc := NewService(repos.Ticket, repos.TicketProject, repos.User, auditSvc, slog.Default(), push.NoopPublisher{})
	return svc, repos
}

// seedProject 创建测试项目，返回项目 ID
func seedProject(t *testing.T, repos *db.Repos, name string) uint {
	t.Helper()
	p := &model.TicketProject{Name: name}
	if err := repos.TicketProject.Create(context.Background(), p); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return p.ID
}

// seedUser 创建测试用户，返回用户 ID
func seedUser(t *testing.T, repos *db.Repos, name string) uint {
	t.Helper()
	u := &model.User{Username: name, Status: model.UserActive}
	if err := repos.User.Create(context.Background(), u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}
