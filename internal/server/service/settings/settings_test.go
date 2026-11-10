package settings

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

	svc, err := NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	return svc, repos.Setting, auditSvc, store
}

func TestNewService_NilAudit(t *testing.T) {
	_, err := NewService(&db.SettingRepo{}, nil, nil, nil, nil, nil)
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
	authJSON := json.RawMessage(`{"sessionTTL":3600,"maxSessionsPerUser":5,"maxUserLoginAttempts":10,"maxIPLoginAttempts":50,"userBanDuration":600,"ipBanDuration":3600,"userSyncInterval":600,"idMappingFoundCacheTTL":86400,"idMappingNotFoundCacheTTL":600}`)
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

	// 4. Connection settings (including nested proxy)
	connectionJSON := json.RawMessage(`{"maxPerUserCap":8,"proxy":{"enabled":true,"maxProxyConnections":15,"maxProxyConnectionsPerUser":5,"vncEnabled":true,"rdpEnabled":false,"sshEnabled":false}}`)
	err = svc.UpdateByDomain(ctx, schema.DomainConnection, connectionJSON)
	if err != nil {
		t.Fatalf("failed to update connection settings: %v", err)
	}
	val, err = svc.GetConnectionSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get connection settings: %v", err)
	}
	connVal := val.(schema.ConnectionSettings)
	if connVal.MaxPerUserCap != 8 {
		t.Errorf("expected MaxPerUserCap to be 8, got %d", connVal.MaxPerUserCap)
	}
	proxyVal := connVal.Proxy
	if proxyVal.MaxProxyConnections != 15 || !proxyVal.VncEnabled || proxyVal.RdpEnabled || proxyVal.SshEnabled {
		t.Errorf("unexpected proxy settings: %+v", proxyVal)
	}

	// 5. File Settings
	fileJSON := json.RawMessage(`{"enabled":true,"downloadEnabled":false,"uploadEnabled":true,"requiresApproval":true,"downloadApprovalTTL":3600,"uploadMaxSizeBytes":10485760,"listMaxEntries":999,"locations":[]}`)
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
		"execBinPath": "` + filepath.ToSlash(absTestPath("dcexec")) + `",
		"vnc": {
			"binDir": "` + filepath.ToSlash(absTestPath("bin")) + `",
			"scanInterval": 300,
			"scanEnabled": true
		},
		"monitor": {
			"nvidiaSmiPath": "",
			"diskMounts": [],
			"metricsEnabled": true,
			"metricsInterval": 60
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
	if agentVal.ExecBinPath != filepath.ToSlash(absTestPath("dcexec")) {
		t.Errorf("expected ExecBinPath to be /usr/bin/dcexec, got %s", agentVal.ExecBinPath)
	}
	if agentVal.VNC.BinDir != filepath.ToSlash(absTestPath("bin")) {
		t.Errorf("expected VNC.BinDir to be /usr/bin, got %s", agentVal.VNC.BinDir)
	}

	// MaxPerUserCap getter helper
	cap, err := svc.MaxPerUserCap(ctx)
	if err != nil {
		t.Fatalf("failed to get MaxPerUserCap: %v", err)
	}
	if cap != 8 {
		t.Errorf("expected MaxPerUserCap to be 8, got %d", cap)
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

	// 2. MaxPerUserCap error
	_, err = svc.MaxPerUserCap(ctx)
	if err == nil {
		t.Error("expected error for MaxPerUserCap when DB is closed")
	}
}

// 锁定「新装全局默认」下发与 Agent 本地 NewSettingsManager 默认一致。
func TestGetAgentSettingsForNodeMatchesBootstrapDefaults(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	got, err := svc.GetAgentSettingsForNode(ctx, "fresh-node")
	if err != nil {
		t.Fatalf("GetAgentSettingsForNode: %v", err)
	}

	agentDef, ok := schema.DefaultSettings(schema.DomainAgent)
	if !ok {
		t.Fatal("missing DomainAgent defaults")
	}
	wantAgent := agentDef.(schema.AgentSettings)

	if got.GetExecBinPath() != wantAgent.ExecBinPath || got.GetExecBinPath() != contract.DefaultExecBinPath {
		t.Errorf("execBinPath: got %q want %q", got.GetExecBinPath(), wantAgent.ExecBinPath)
	}
	if got.GetVnc().GetBinDir() != wantAgent.VNC.BinDir {
		t.Errorf("vnc.binDir: got %q want %q", got.GetVnc().GetBinDir(), wantAgent.VNC.BinDir)
	}
	if int(got.GetVnc().GetScanInterval()) != wantAgent.VNC.ScanInterval {
		t.Errorf("vnc.scanInterval: got %d want %d", got.GetVnc().GetScanInterval(), wantAgent.VNC.ScanInterval)
	}
	if got.GetVnc().GetScanEnabled() != wantAgent.VNC.ScanEnabled {
		t.Errorf("vnc.scanEnabled: got %v want %v", got.GetVnc().GetScanEnabled(), wantAgent.VNC.ScanEnabled)
	}
	if got.GetMonitor().GetMetricsEnabled() != wantAgent.Monitor.MetricsEnabled {
		t.Errorf("metricsEnabled: got %v want %v", got.GetMonitor().GetMetricsEnabled(), wantAgent.Monitor.MetricsEnabled)
	}
	if int(got.GetMonitor().GetMetricsInterval()) != wantAgent.Monitor.MetricsInterval {
		t.Errorf("metricsInterval: got %d want %d", got.GetMonitor().GetMetricsInterval(), wantAgent.Monitor.MetricsInterval)
	}
	if int(got.GetProbeInterval()) != contract.DefaultStorageProbeInterval {
		t.Errorf("probeInterval: got %d want %d", got.GetProbeInterval(), contract.DefaultStorageProbeInterval)
	}
	if len(got.GetStorages()) != 0 {
		t.Errorf("expected no storages on fresh install, got %d", len(got.GetStorages()))
	}
}

func TestBuildNodeEffectiveSettings_UsesDBOverrideNotCache(t *testing.T) {
	svc, _, _, store := newSettingsServiceForTest(t)
	ctx := context.Background()
	repos := db.NewRepos(store)

	const nodeID = "eff-node"
	dbPath := absTestPath("from-db", "dcexec")
	cachePath := absTestPath("from-cache", "dcexec")

	require.NoError(t, repos.Node.Create(ctx, &model.Node{
		NodeID: nodeID,
		Settings: map[string]any{
			"agent": map[string]any{"execBinPath": dbPath},
		},
	}))

	svc.nodeOverrideCache.SetWithTTL(nodeID, &schema.NodeSettings{
		Agent: &schema.NodeAgentOverride{ExecBinPath: cachePath},
	}, time.Hour)

	dbOverride, err := schema.DecodeNodeSettingsFromMap(map[string]any{
		"agent": map[string]any{"execBinPath": dbPath},
	})
	require.NoError(t, err)

	eff, err := svc.BuildNodeEffectiveSettings(ctx, nodeID, dbOverride, true)
	require.NoError(t, err)
	require.Equal(t, dbPath, eff.Agent.ExecBinPath,
		"useDBOverride=true must merge the DB override, not nodeOverrideCache")

	cachedEff, err := svc.BuildNodeEffectiveSettings(ctx, nodeID, nil, false)
	require.NoError(t, err)
	require.Equal(t, cachePath, cachedEff.Agent.ExecBinPath,
		"useDBOverride=false still reads nodeOverrideCache")
}

func TestBumpNodeOverridesTopicTx_IncrementsVersion(t *testing.T) {
	svc, _, _, store := newSettingsServiceForTest(t)
	ctx := context.Background()
	repos := db.NewRepos(store)

	before, err := repos.CacheVersion.GetAll(ctx)
	require.NoError(t, err)

	require.NoError(t, store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return svc.BumpNodeOverridesTopicTx(ctx, tx)
	}))

	after, err := repos.CacheVersion.GetAll(ctx)
	require.NoError(t, err)
	require.Equal(t, before[db.CacheVersionTopicNodeOverrides]+1, after[db.CacheVersionTopicNodeOverrides])
}

func TestInvalidateNodeOverride_ClearsCacheEntry(t *testing.T) {
	svc, _, _, store := newSettingsServiceForTest(t)
	ctx := context.Background()
	repos := db.NewRepos(store)

	const nodeID = "inv-node"
	livePath := absTestPath("live", "dcexec")
	require.NoError(t, repos.Node.Create(ctx, &model.Node{
		NodeID: nodeID,
		Settings: map[string]any{
			"agent": map[string]any{"execBinPath": livePath},
		},
	}))

	svc.nodeOverrideCache.SetWithTTL(nodeID, &schema.NodeSettings{
		Agent: &schema.NodeAgentOverride{ExecBinPath: absTestPath("stale", "dcexec")},
	}, time.Hour)

	svc.InvalidateNodeOverride(nodeID)
	got, err := svc.getNodeOverride(ctx, nodeID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Agent)
	require.Equal(t, livePath, got.Agent.ExecBinPath)
}

// PUT 语义：叠加（局部更新）。
func TestUpdateByDomain_PartialPayloadKeepsExistingValues(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"ipBanDuration":7200}`)))
	got, err := svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 7200, got.IPBanDuration)
	require.Equal(t, 86400, got.UserSyncInterval, "未携带字段应为 schema 默认值")

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"maxUserLoginAttempts":9}`)))
	got, err = svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 9, got.MaxUserLoginAttempts)
	require.Equal(t, 7200, got.IPBanDuration, "未携带字段必须保持库中现值，不得被清零")

	err = svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"ipBanDuration":1}`))
	if err == nil {
		t.Fatal("expected validation error for ipBanDuration=1")
	}
	var contractErr contract.DomainError
	if !errors.As(err, &contractErr) {
		t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
	}
	if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %v", contractErr.HTTPStatus(), err)
	}
	got, err = svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 7200, got.IPBanDuration, "被拒绝的修改不得部分生效")

	err = svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"idMappingNotFoundCacheTTL":999999}`))
	if err == nil {
		t.Fatal("expected validation error for idMappingNotFoundCacheTTL=999999")
	}
	var contractErr2 contract.DomainError
	if !errors.As(err, &contractErr2) {
		t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
	}
	if contractErr2.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %v", contractErr2.HTTPStatus(), err)
	}
}

func TestUpdateByDomain_ExplicitZeroIsApplied(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"userSyncInterval":3600}`)))
	got, err := svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 3600, got.UserSyncInterval)

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"userSyncInterval":0}`)))
	got, err = svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, got.UserSyncInterval, "显式传 0 表示禁用，必须生效")
}

func TestUpdateByDomain_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	err := svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"identityFoundCacheTTL":86400}`))
	require.Error(t, err, "unknown field must be rejected")
}

func TestUpdateByDomain_RejectsZeroOnIntervalsWithoutAllowZero(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	err := svc.UpdateByDomain(ctx, schema.DomainScheduler, []byte(`{"jobCacheTTL":0}`))
	require.Error(t, err, "jobCacheTTL=0 must be rejected since allowzero was removed")

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainScheduler, []byte(`{"jobCacheTTL":60}`)))
	got, err := svc.GetSchedulerSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 60, got.JobCacheTTL)
}

func TestUpdateByDomain_ConnectionProxyPartialMerge(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainConnection, []byte(`{"proxy":{"rdpEnabled":false}}`)))
	got, err := svc.GetConnectionSettings(ctx)
	require.NoError(t, err)
	require.False(t, got.Proxy.RdpEnabled, "rdpEnabled should be set to false")
	require.True(t, got.Proxy.VncEnabled, "vncEnabled should remain true from defaults")
	require.True(t, got.Proxy.SshEnabled, "sshEnabled should remain true from defaults")
	require.True(t, got.Proxy.Enabled, "enabled should remain true from defaults")

	err = svc.UpdateByDomain(ctx, schema.DomainConnection, []byte(`{"proxy":{"allowedProtocols":["vnc"]}}`))
	require.Error(t, err, "unknown nested field must be rejected")
}

func TestUpdateByDomain_DesktopPartialMerge(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"linuxEnabled":false}`)))
	got, err := svc.GetDesktopSettings(ctx)
	require.NoError(t, err)
	require.False(t, got.LinuxEnabled, "linuxEnabled should be set to false")
	require.True(t, got.WindowsEnabled, "windowsEnabled should remain true from defaults")
	require.True(t, got.MacosEnabled, "macosEnabled should remain true from defaults")
	require.True(t, got.ReleasesEnabled, "releasesEnabled should remain true from defaults")

	err = svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"platforms":["windows"]}`))
	require.Error(t, err, "unknown field must be rejected")
}

func TestGetAgentSettingsForNodeCarriesSchedulerEnvScript(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	got, err := svc.GetAgentSettingsForNode(ctx, "node-a")
	if err != nil {
		t.Fatalf("GetAgentSettingsForNode: %v", err)
	}
	if got.GetSchedulerEnvScript() != "" {
		t.Errorf("default schedulerEnvScript = %q, want empty", got.GetSchedulerEnvScript())
	}

	script := filepath.ToSlash(filepath.Join(t.TempDir(), "profile.lsf"))
	raw := []byte(`{"type":"slurm","envScript":"` + script + `"}`)
	if err := svc.UpdateByDomain(ctx, schema.DomainScheduler, raw); err != nil {
		t.Fatalf("UpdateByDomain(scheduler): %v", err)
	}
	got, err = svc.GetAgentSettingsForNode(ctx, "node-a")
	if err != nil {
		t.Fatalf("GetAgentSettingsForNode: %v", err)
	}
	if got.GetSchedulerEnvScript() != script {
		t.Errorf("schedulerEnvScript = %q, want %q", got.GetSchedulerEnvScript(), script)
	}

	eff, err := svc.GetEffectiveSchedulerSettingsForNode(ctx, "node-a")
	if err != nil {
		t.Fatalf("GetEffectiveSchedulerSettingsForNode: %v", err)
	}
	if eff.Type != schema.JobSchedulerSlurm || eff.EnvScript != script {
		t.Errorf("effective = %+v", eff)
	}
}
