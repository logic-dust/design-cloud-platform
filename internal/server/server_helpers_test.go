package server

import (
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
)

// newTestSQLiteStore opens a migrated SQLite store and repos for package server tests.
func newTestSQLiteStore(t *testing.T) (*db.Store, *db.Repos) {
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
	if err := settings.EnsureDefaults(store); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}
	return store, db.NewRepos(store)
}

func newTestAuditService(t *testing.T, nodeID string) *audit.Service {
	t.Helper()
	dir := t.TempDir()
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(dir, "audit.log"),
		SecurityLogPath: filepath.Join(dir, "security.log"),
		NodeID:          nodeID,
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() {
		if err := auditSvc.Close(); err != nil {
			t.Fatalf("close audit service: %v", err)
		}
	})
	return auditSvc
}

func newTestSettingsService(t *testing.T) *settings.Service {
	t.Helper()
	_, repos := newTestSQLiteStore(t)
	auditSvc := newTestAuditService(t, "test-node")
	svc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	return svc
}
