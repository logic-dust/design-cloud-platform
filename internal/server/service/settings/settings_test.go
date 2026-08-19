package settings

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/service/settings/schema"
)

// absTestPath 返回当前平台上确实绝对的测试路径：POSIX 字面量在 Windows 上不是绝对路径。
func absTestPath(parts ...string) string {
	return filepath.Join(append([]string{os.TempDir()}, parts...)...)
}

func newSettingsServiceForTest(t *testing.T) (*Service, *db.SettingRepo, *audit.Service, *db.Store) {
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
	if err := EnsureDefaults(store); err != nil {
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

	svc, err := NewService(repos.Setting, auditSvc, nil)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	return svc, repos.Setting, auditSvc, store
}

func TestNewService_NilAudit(t *testing.T) {
	_, err := NewService(&db.SettingRepo{}, nil, nil)
	if err == nil {
		t.Error("expected error when audit service is nil")
	}
}

func TestGetAndUpdateByDomain(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 1. Platform Settings
	val, err := svc.GetByDomain(ctx, schema.DomainPlatform)
	if err != nil {
		t.Fatalf("failed to get platform settings: %v", err)
	}
	psDefault, ok := val.(schema.PlatformSettings)
	if !ok || psDefault.Name != "Design Cloud Platform" {
		t.Errorf("expected default platform settings, got %+v", val)
	}

	platformJSON := json.RawMessage(`{"name":"Updated Name","description":"Desc"}`)
	err = svc.UpdateByDomain(ctx, schema.DomainPlatform, platformJSON)
	if err != nil {
		t.Fatalf("failed to update platform settings: %v", err)
	}

	val, err = svc.GetByDomain(ctx, schema.DomainPlatform)
	if err != nil {
		t.Fatalf("failed to get platform settings: %v", err)
	}
	psUpdated := val.(schema.PlatformSettings)
	if psUpdated.Name != "Updated Name" {
		t.Errorf("expected platform name to be 'Updated Name', got %s", psUpdated.Name)
	}

	// 2. Auth Settings
	authJSON := json.RawMessage(`{"sessionTTL":3600,"maxLoginSessionsPerUser":10,"maxUserLoginAttempts":10,"maxIPLoginAttempts":50,"userBanDuration":600,"ipBanDuration":3600,"userSyncInterval":600,"idMappingFoundCacheTTL":86400,"idMappingNotFoundCacheTTL":600}`)
	err = svc.UpdateByDomain(ctx, schema.DomainAuth, authJSON)
	if err != nil {
		t.Fatalf("failed to update auth settings: %v", err)
	}
	val, err = svc.GetAuthSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get auth settings: %v", err)
	}
	authVal := val.(schema.AuthSettings)
	if authVal.SessionTTL != 3600 {
		t.Errorf("expected SessionTTL to be 3600, got %d", authVal.SessionTTL)
	}
	if authVal.UserSyncInterval != 600 {
		t.Errorf("expected UserSyncInterval to be 600, got %d", authVal.UserSyncInterval)
	}

	// 3. Connection Settings
	connectionJSON := json.RawMessage(`{"maxConnectionsPerUser":5}`)
	err = svc.UpdateByDomain(ctx, schema.DomainConnection, connectionJSON)
	if err != nil {
		t.Fatalf("failed to update connection settings: %v", err)
	}
	val, err = svc.GetConnectionSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get connection settings: %v", err)
	}
	connVal := val.(schema.ConnectionSettings)
	if connVal.MaxConnectionsPerUser != 5 {
		t.Errorf("expected MaxConnectionsPerUser to be 5, got %d", connVal.MaxConnectionsPerUser)
	}

	// 4. Tunnel Settings
	tunnelJSON := json.RawMessage(`{"enabled":true,"maxTunnels":15,"maxTunnelsPerUser":5,"otpTTL":1800,"vncEnabled":true,"rdpEnabled":false,"sshEnabled":false}`)
	err = svc.UpdateByDomain(ctx, schema.DomainTunnel, tunnelJSON)
	if err != nil {
		t.Fatalf("failed to update tunnel settings: %v", err)
	}
	val, err = svc.GetTunnelSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get tunnel settings: %v", err)
	}
	tunnelVal := val.(schema.TunnelSettings)
	if tunnelVal.MaxTunnels != 15 || !tunnelVal.VncEnabled || tunnelVal.RdpEnabled || tunnelVal.SshEnabled {
		t.Errorf("unexpected tunnel settings: %+v", tunnelVal)
	}

	// 5. File Settings
	fileJSON := json.RawMessage(`{"enabled":true,"downloadEnabled":false,"uploadEnabled":true,"requiresApproval":true,"downloadApprovalTTL":3600,"uploadMaxSizeBytes":10485760,"locations":[]}`)
	err = svc.UpdateByDomain(ctx, schema.DomainFile, fileJSON)
	if err != nil {
		t.Fatalf("failed to update file settings: %v", err)
	}
	val, err = svc.GetFileSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get file settings: %v", err)
	}
	if _, ok := val.(schema.FileSettings); !ok {
		t.Fatalf("expected file settings")
	}

	// 6. Agent Settings
	agentJSON := []byte(`{
		"launcherPath": "` + filepath.ToSlash(absTestPath("launcher")) + `",
		"vnc": {
			"binDir": "` + filepath.ToSlash(absTestPath("bin")) + `",
			"scanInterval": 300,
			"scanEnabled": true
		},
		"monitor": {
			"nvidiaSmiPath": "",
			"diskMounts": [],
			"resourceReportEnabled": true,
			"resourceReportInterval": 60
		}
	}`)
	err = svc.UpdateByDomain(ctx, schema.DomainAgent, agentJSON)
	if err != nil {
		t.Fatalf("failed to update agent settings: %v", err)
	}
	agentVal, err := svc.GetAgentSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get agent settings: %v", err)
	}
	if agentVal.LauncherPath != filepath.ToSlash(absTestPath("launcher")) {
		t.Errorf("expected LauncherPath to be /usr/bin/launcher, got %s", agentVal.LauncherPath)
	}
	if agentVal.VNC.BinDir != filepath.ToSlash(absTestPath("bin")) {
		t.Errorf("expected VNC.BinDir to be /usr/bin, got %s", agentVal.VNC.BinDir)
	}

	// MaxConnectionsPerUser getter helper
	maxConns, err := svc.MaxConnectionsPerUser(ctx)
	if err != nil {
		t.Fatalf("failed to get MaxConnectionsPerUser: %v", err)
	}
	if maxConns != 5 {
		t.Errorf("expected MaxConnectionsPerUser to be 5, got %d", maxConns)
	}
}

func TestGetAndUpdateInvalidDomain(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	_, err := svc.GetByDomain(ctx, "invalid-domain")
	if err == nil {
		t.Error("expected error for invalid domain in GetByDomain")
	}

	err = svc.UpdateByDomain(ctx, "invalid-domain", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for invalid domain in UpdateByDomain")
	}
}

func TestUpdateByDomain_ValidationError(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 叠加语义下"缺字段"不再是错误（未携带字段保持现值），真正非法的是未知字段。
	// 这里以未知字段作为拒绝路径的代表：payload 不得携带 schema 之外的键。
	badPlatformJSON := json.RawMessage(`{"notARealField":"x"}`)
	err := svc.UpdateByDomain(ctx, schema.DomainPlatform, badPlatformJSON)
	if err == nil {
		t.Error("expected validation error for unknown field")
	}

	// 校验失败不得产生副作用：默认值原样保留。
	got, err := svc.GetPlatformSettings(ctx)
	if err != nil {
		t.Fatalf("get platform settings: %v", err)
	}
	def, _ := schema.DefaultSettings(schema.DomainPlatform)
	defaults := def.(schema.PlatformSettings)
	if !reflect.DeepEqual(got, defaults) {
		t.Errorf("rejected update must not mutate stored settings, got %+v", got)
	}
}

func TestGetByDomainAll(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	domains := schema.AllDomains()
	for _, domain := range domains {
		_, err := svc.GetByDomain(ctx, domain)
		if err != nil {
			t.Errorf("expected success for domain: %s, got %v", domain, err)
		}
	}
}

func TestErrors_DBClosed(t *testing.T) {
	svc, _, _, store := newSettingsServiceForTest(t)
	ctx := context.Background()

	// Close store to force DB errors
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	// 1. loadTyped error
	_, err := svc.GetPlatformSettings(ctx)
	if err == nil {
		t.Error("expected error for GetPlatformSettings when DB is closed")
	}

	// 2. MaxConnectionsPerUser error
	_, err = svc.MaxConnectionsPerUser(ctx)
	if err == nil {
		t.Error("expected error for MaxConnectionsPerUser when DB is closed")
	}
}
