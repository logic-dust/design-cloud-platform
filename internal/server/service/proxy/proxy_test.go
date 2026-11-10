package proxy

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
)

func newProxyServiceForTest(t *testing.T) (*Service, *db.SettingRepo, *db.ConnectionRepo, *db.NodeRepo) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
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
	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}

	svc, err := NewService(settingsSvc, repos.Connection, repos.Node, slog.Default())
	if err != nil {
		t.Fatalf("new proxy service: %v", err)
	}

	return svc, repos.Setting, repos.Connection, repos.Node
}

func createProxyTestConnection(t *testing.T, connRepo *db.ConnectionRepo, nodeRepo *db.NodeRepo) model.Connection {
	t.Helper()

	nodeID := "127.0.0.1"
	if err := nodeRepo.Create(context.Background(), &model.Node{
		NodeID:           nodeID,
		DialHost:         nodeID,
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin",
		NodeID:   nodeID,
		Port:     5901,
		Status:   model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return conn
}

func registerProxySessionForTest(ctx context.Context, svc *Service, connectionID uint, requester string) (*ProxyConnection, error) {
	prepared, err := svc.PrepareDataPlaneOpen(ctx, &OpenDataPlaneRequest{ConnectionID: connectionID, Requester: requester})
	if err != nil {
		return nil, err
	}
	return svc.RegisterDataPlaneSession(ctx, prepared, NewProxyConnectionLifecycle(ctx))
}

func saveConnectionProxySettingsForTest(t *testing.T, repo *db.SettingRepo, proxy schema.ProxySettings) {
	t.Helper()
	connSettings := schema.ConnectionSettings{
		MaxPerUserCap: schema.MaxPerUserCapDefault,
		Proxy:         proxy,
	}
	if err := repo.Save(context.Background(), schema.DomainConnection.String(), connSettings); err != nil {
		t.Fatalf("save connection settings: %v", err)
	}
}

func TestPrepareRejectsDisabledProxy(t *testing.T) {
	t.Parallel()

	svc, settingsRepo, connRepo, nodeRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo, nodeRepo)

	proxySettings := defaultProxySettingsForTest(t)
	proxySettings.Enabled = false
	saveConnectionProxySettingsForTest(t, settingsRepo, proxySettings)

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "hpcadmin"})
	if err == nil {
		t.Fatalf("prepare succeeded, want disabled proxy error")
	}
	if _, getErr := svc.GetActiveByConnectionIDAndRequester(conn.ID, "hpcadmin"); getErr == nil {
		t.Fatalf("session was registered even though proxy is disabled")
	}
}

func TestPrepareRejectsDisallowedProtocol(t *testing.T) {
	t.Parallel()

	svc, settingsRepo, connRepo, nodeRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo, nodeRepo)

	proxySettings := defaultProxySettingsForTest(t)
	proxySettings.Enabled = true
	proxySettings.VncEnabled = false
	proxySettings.SshEnabled = true
	saveConnectionProxySettingsForTest(t, settingsRepo, proxySettings)

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "hpcadmin"})
	if err == nil {
		t.Fatalf("prepare succeeded, want disallowed protocol error")
	}
}

func TestPrepareRejectsStoppedConnection(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)

	conn := createProxyTestConnection(t, connRepo, nodeRepo)
	if err := connRepo.UpdateFields(context.Background(), conn.ID, map[string]any{"status": model.ConnectionStopped}); err != nil {
		t.Fatalf("stop connection: %v", err)
	}

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "hpcadmin"})
	if err == nil || !errors.Is(err, ErrNotProxyable) {
		t.Fatalf("error = %v, want ErrNotProxyable for stopped connection", err)
	}
}

func TestPrepareRejectsWrongRequester(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo, nodeRepo)

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "intruder"})
	if err == nil || !errors.Is(err, ErrProxyConnectionForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
}

func TestRegisterTouchesConnectionAccessTime(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo, nodeRepo)

	if _, err := registerProxySessionForTest(context.Background(), svc, conn.ID, "hpcadmin"); err != nil {
		t.Fatalf("register session: %v", err)
	}

	updated, err := connRepo.GetByID(context.Background(), conn.ID)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if updated.LastAccessedAt == nil {
		t.Fatal("last accessed was not updated")
	}
}

func TestPrepareFallsBackToTargetNodeWhenHostEmpty(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	nodeID := "10.0.0.9"
	if err := nodeRepo.Create(context.Background(), &model.Node{
		NodeID:           nodeID,
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin",
		NodeID:   nodeID,
		Port:     5901,
		Status:   model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	prepared, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "hpcadmin"})
	if err != nil {
		t.Fatalf("prepare proxy session: %v", err)
	}
	if prepared.Session.Host != "10.0.0.9" {
		t.Fatalf("session host = %q, want target node fallback", prepared.Session.Host)
	}
}

func defaultProxySettingsForTest(t *testing.T) schema.ProxySettings {
	t.Helper()
	return schema.DefaultProxySettings()
}

func TestPrepareRejectsWhenNodeDisallowsConnections(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	node := model.Node{NodeID: "node-blocked", AllowConnections: true, Status: model.NodeStatusOnline}
	if err := nodeRepo.Create(context.Background(), &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	disallow := false
	if err := nodeRepo.UpdateAdminFields(context.Background(), "node-blocked", db.NodeAdminPatch{AllowConnections: &disallow}); err != nil {
		t.Fatalf("disable connections: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin",
		NodeID:   "node-blocked", Port: 5901, Status: model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "hpcadmin"})
	if err == nil || !errors.Is(err, ErrNodeConnectionsDisallowed) {
		t.Fatalf("error = %v, want ErrNodeConnectionsDisallowed", err)
	}
}

func TestPrepareRejectsWhenNodeInMaintenance(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	node := model.Node{NodeID: "node-maint", AllowConnections: true, Status: model.NodeStatusMaintenance}
	if err := nodeRepo.Create(context.Background(), &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin",
		NodeID:   "node-maint", Port: 5901, Status: model.ConnectionUnknown,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{ConnectionID: conn.ID, Requester: "hpcadmin"})
	if err == nil || !errors.Is(err, ErrNodeInMaintenance) {
		t.Fatalf("error = %v, want ErrNodeInMaintenance", err)
	}
}

func TestCloseByIDIgnoresStaleTeardown(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	conn := createProxyTestConnection(t, connRepo, nodeRepo)

	first, err := registerProxySessionForTest(context.Background(), svc, conn.ID, "hpcadmin")
	if err != nil {
		t.Fatalf("register first: %v", err)
	}
	oldID := first.ID

	second, err := registerProxySessionForTest(context.Background(), svc, conn.ID, "hpcadmin")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.ID == oldID {
		t.Fatal("expected replacement session")
	}

	if err := svc.CloseByID(oldID); !errors.Is(err, ErrProxyConnectionNotFound) {
		t.Fatalf("CloseByID(old) = %v, want not found", err)
	}
	got, err := svc.GetActiveByConnectionIDAndRequester(conn.ID, "hpcadmin")
	if err != nil {
		t.Fatalf("current session: %v", err)
	}
	if got.ID != second.ID {
		t.Fatalf("stale close killed current session")
	}
}

func TestCloseAllByRequester(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	c1 := createProxyTestConnection(t, connRepo, nodeRepo)
	c2 := model.Connection{
		Username: "hpcadmin",
		NodeID:   "127.0.0.1", Port: 5902, Status: model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &c2); err != nil {
		t.Fatalf("create c2: %v", err)
	}
	if _, err := registerProxySessionForTest(context.Background(), svc, c1.ID, "hpcadmin"); err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if _, err := registerProxySessionForTest(context.Background(), svc, c2.ID, "hpcadmin"); err != nil {
		t.Fatalf("create s2: %v", err)
	}
	if err := svc.CloseAllByRequester("hpcadmin"); err != nil {
		t.Fatalf("CloseAllByRequester: %v", err)
	}
	if _, err := svc.GetActiveByConnectionIDAndRequester(c1.ID, "hpcadmin"); !errors.Is(err, ErrProxyConnectionNotFound) {
		t.Fatal("expected c1 proxy gone")
	}
	if _, err := svc.GetActiveByConnectionIDAndRequester(c2.ID, "hpcadmin"); !errors.Is(err, ErrProxyConnectionNotFound) {
		t.Fatal("expected c2 proxy gone")
	}
}

type stubSSHDial struct {
	host string
	port uint
	err  error
}

func (s stubSSHDial) ResolveSSHDialTarget(ctx context.Context, username string, resourceID uint) (string, uint, error) {
	if s.err != nil {
		return "", 0, s.err
	}
	return s.host, s.port, nil
}

func TestPrepareResourceSSH_UsesResolverWithoutConnection(t *testing.T) {
	t.Parallel()

	svc, settingsRepo, _, _ := newProxyServiceForTest(t)
	proxySettings := defaultProxySettingsForTest(t)
	proxySettings.SshEnabled = true
	saveConnectionProxySettingsForTest(t, settingsRepo, proxySettings)
	svc.SetSSHDialResolver(stubSSHDial{host: "10.1.2.3", port: 22})

	prepared, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{
		ResourceID: 34, Requester: "bob",
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.Session.ResourceID != 34 || prepared.Session.ConnectionID != 0 {
		t.Fatalf("session anchors = conn=%d res=%d", prepared.Session.ConnectionID, prepared.Session.ResourceID)
	}
	if prepared.Session.Host != "10.1.2.3" || prepared.Session.Port != 22 {
		t.Fatalf("target = %s:%d", prepared.Session.Host, prepared.Session.Port)
	}
	if prepared.Session.Protocol != contract.SSHProtocol {
		t.Fatalf("protocol = %s", prepared.Session.Protocol)
	}
}

func TestPrepareConnectionSSH_Rejected(t *testing.T) {
	t.Parallel()

	svc, _, connRepo, nodeRepo := newProxyServiceForTest(t)
	if err := nodeRepo.Create(context.Background(), &model.Node{
		NodeID: "ssh-node", DialHost: "10.0.0.1", AllowConnections: true, Status: model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("node: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin", NodeID: "ssh-node", Port: 22,
		Status: model.ConnectionRunning, Protocol: contract.SSHProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("conn: %v", err)
	}

	_, err := svc.PrepareDataPlaneOpen(context.Background(), &OpenDataPlaneRequest{
		ConnectionID: conn.ID, Requester: "hpcadmin",
	})
	if !errors.Is(err, ErrSSHUsesResourceProxy) {
		t.Fatalf("error = %v, want ErrSSHUsesResourceProxy", err)
	}
}
