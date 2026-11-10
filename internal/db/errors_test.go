package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func TestIsUniqueConstraintError_SQLite(t *testing.T) {
	tmp := t.TempDir()
	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tmp, "unique_test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	repos := NewRepos(store)

	// 插入第一条 connection 占住 (node-1, username: alice, port: 22)
	c1 := model.Connection{
		Username: "alice",
		NodeID:   "node-1",
		Port:     22,
		Protocol: contract.SSHProtocol,
		Status:   model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, &c1); err != nil {
		t.Fatalf("create c1: %v", err)
	}

	// 插入第二条冲突 connection 触发 idx_conn_node_user_port 唯一键约束
	c2 := model.Connection{
		Username: "alice",
		NodeID:   "node-1",
		Port:     22,
		Protocol: contract.SSHProtocol,
		Status:   model.ConnectionRunning,
	}
	err2 := store.db.WithContext(ctx).Create(&c2).Error
	if err2 == nil {
		t.Fatalf("expected unique constraint error, got nil")
	}

	if !IsUniqueConstraintError(err2) {
		t.Fatalf("expected IsUniqueConstraintError to return true, got false for: %v", err2)
	}
}
