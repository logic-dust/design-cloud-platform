package proxy

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
)

func newProxyServiceForTest(t *testing.T) (*Service, *db.SettingRepo, *db.ConnectionRepo) {
	t.Helper()

	store, err := db.NewStore(config.Database{
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
	if err := settings.EnsureDefaults(store); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}

	repos := db.NewRepos(store)
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(t.TempDir(), "audit.log"),
		SecurityLogPath: filepath.Join(t.TempDir(), "security.log"),
		NodeID:          "test-node",
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() {
		if err := auditSvc.Close(); err != nil {
			t.Fatalf("close audit service: %v", err)
		}
	})

	settingsSvc, err := settings.NewService(repos.Setting, auditSvc, nil)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	proxySvc, err := NewService(settingsSvc, repos.Connection, slog.Default())
	if err != nil {
		t.Fatalf("new proxy service: %v", err)
	}
	return proxySvc, repos.Setting, repos.Connection
}

func createProxyTestConnection(t *testing.T, repo *db.ConnectionRepo) model.Connection {
	t.Helper()

	conn := model.Connection{
		Name:     "conn-1",
		Username: "hpcadmin",
		Host:     "node-a",
		Port:     5901,
		Status:   model.ConnectionRunning,
		Source:   model.ConnectionSourceManual,
		Protocol: contract.VNCProtocol,
	}
	if err := repo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return conn
}

func TestCreateRejectsDisabledProxyBeforeCreatingSession(t *testing.T) {
	t.Parallel()

	svc, settingsRepo, connRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo)

	tunnelSettings := defaultTunnelSettingsForTest(t)
	tunnelSettings.Enabled = false
	if err := settingsRepo.Save(context.Background(), schema.DomainTunnel.String(), tunnelSettings); err != nil {
		t.Fatalf("save tunnel settings: %v", err)
	}

	_, err := svc.Create(context.Background(), &CreateTunnelRequest{ConnectionID: conn.ID})
	if err == nil {
		t.Fatalf("create session succeeded, want disabled proxy error")
	}
	if _, getErr := svc.GetActiveByConnectionID(conn.ID); getErr == nil {
		t.Fatalf("session was created even though proxy is disabled")
	}
}

func TestCreateRejectsDisallowedProtocolBeforeCreatingSession(t *testing.T) {
	t.Parallel()

	svc, settingsRepo, connRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo)

	tunnelSettings := defaultTunnelSettingsForTest(t)
	tunnelSettings.Enabled = true
	tunnelSettings.VncEnabled = false
	tunnelSettings.SshEnabled = true
	if err := settingsRepo.Save(context.Background(), schema.DomainTunnel.String(), tunnelSettings); err != nil {
		t.Fatalf("save tunnel settings: %v", err)
	}

	_, err := svc.Create(context.Background(), &CreateTunnelRequest{ConnectionID: conn.ID})
	if err == nil {
		t.Fatalf("create session succeeded, want disallowed protocol error")
	}
	if _, getErr := svc.GetActiveByConnectionID(conn.ID); getErr == nil {
		t.Fatalf("session was created even though protocol is disallowed")
	}
}

func TestCreateTouchesConnectionAccessTime(t *testing.T) {
	t.Parallel()

	svc, _, connRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo)
	before := conn.LastAccessedAt

	if _, err := svc.Create(context.Background(), &CreateTunnelRequest{ConnectionID: conn.ID}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	updated, err := connRepo.GetByID(context.Background(), conn.ID)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if !updated.LastAccessedAt.After(before) {
		t.Fatalf("last accessed was not updated: %v", updated.LastAccessedAt)
	}
}

func TestCreateFallsBackToTargetNodeWhenHostEmpty(t *testing.T) {
	t.Parallel()

	svc, _, connRepo := newProxyServiceForTest(t)
	conn := model.Connection{
		Name:         "conn-with-target-node",
		Username:     "hpcadmin",
		TargetNodeID: "node-a",
		Port:         5901,
		Status:       model.ConnectionRunning,
		Source:       model.ConnectionSourceFixed,
		Protocol:     contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	tunnel, err := svc.Create(context.Background(), &CreateTunnelRequest{ConnectionID: conn.ID})
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if tunnel.Host != "node-a" {
		t.Fatalf("tunnel host = %q, want target node fallback", tunnel.Host)
	}
}

// defaultTunnelSettingsForTest 返回 schema 层面的 tunnel 默认值（有效配置基线）。
func defaultTunnelSettingsForTest(t *testing.T) schema.TunnelSettings {
	t.Helper()
	def, ok := schema.DefaultSettings(schema.DomainTunnel)
	if !ok {
		t.Fatal("no defaults registered for tunnel domain")
	}
	ps, ok := def.(schema.TunnelSettings)
	if !ok {
		t.Fatalf("unexpected default type %T for tunnel domain", def)
	}
	return ps
}
