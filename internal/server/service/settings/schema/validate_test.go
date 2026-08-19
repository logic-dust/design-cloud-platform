package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
)

// absTestPath 返回当前平台上确实绝对的测试路径：POSIX 字面量在 Windows 上不是绝对路径。
func absTestPath(parts ...string) string {
	return filepath.Join(append([]string{os.TempDir()}, parts...)...)
}

func TestParseDomainAndAllDomains(t *testing.T) {
	domains := AllDomains()

	// 触发式断言：新增/删除 settings 域必须同步这里。显式列表优于魔法数字——
	// 不一致时能直接打印差异，而 `len != 11` 只会在新增域时给出一个无信息的计数错误。
	want := []Domain{
		DomainPlatform, DomainAuth, DomainConnection, DomainTunnel, DomainFile,
		DomainDashboard, DomainStorage, DomainScheduler, DomainLicense, DomainAgent,
		DomainAlert, DomainDesktop, DomainCert,
	}

	if len(domains) != len(want) {
		t.Fatalf("AllDomains() has %d domains, want %d\n got: %v\nwant: %v",
			len(domains), len(want), domains, want)
	}
	for i := range want {
		if domains[i] != want[i] {
			t.Errorf("AllDomains()[%d] = %q, want %q", i, domains[i], want[i])
		}
	}

	for _, d := range domains {
		parsed, ok := ParseDomain(d.String())
		if !ok || parsed != d {
			t.Errorf("failed to parse domain %s", d)
		}
	}

	_, ok := ParseDomain("invalid_domain")
	if ok {
		t.Error("expected invalid_domain to fail parsing")
	}
}

func TestValidateStructByTags(t *testing.T) {
	type DummyStruct struct {
		Age      int     `validate:"min=10,max=20,allowzero"`
		Score    float64 `validate:"min=0,max=100"`
		Category int     `validate:"enum=1|5|10"`
	}

	valid := DummyStruct{Age: 15, Score: 85.5, Category: 5}
	if err := ValidateStructByTags(valid); err != nil {
		t.Errorf("expected valid, got %v", err)
	}

	validZero := DummyStruct{Age: 0, Score: 0, Category: 1}
	if err := ValidateStructByTags(validZero); err != nil {
		t.Errorf("expected valid zero, got %v", err)
	}

	invalidAge := DummyStruct{Age: 25, Score: 50, Category: 1}
	if err := ValidateStructByTags(invalidAge); err == nil {
		t.Error("expected invalid age")
	}

	invalidEnum := DummyStruct{Age: 15, Score: 50, Category: 2}
	if err := ValidateStructByTags(invalidEnum); err == nil {
		t.Error("expected invalid enum")
	}
}

func TestValidatePlatformSettings(t *testing.T) {
	// Success case
	okSettings := PlatformSettings{
		Name:        "My Platform",
		Description: "A platform",
	}
	if err := validatePlatformSettings(okSettings); err != nil {
		t.Errorf("expected platform settings to be valid, got %v", err)
	}

	// With valid support URL
	withURL := PlatformSettings{
		Name:       "Platform",
		SupportUrl: "https://support.example.com",
	}
	if err := validatePlatformSettings(withURL); err != nil {
		t.Errorf("expected supportUrl to be valid, got %v", err)
	}

	// Empty name failure
	badSettings1 := PlatformSettings{
		Name: "",
	}
	if err := validatePlatformSettings(badSettings1); err == nil {
		t.Error("expected error for empty name")
	}

	// Invalid support URL scheme
	badURL := PlatformSettings{
		Name:       "Platform",
		SupportUrl: "ftp://support.example.com",
	}
	if err := validatePlatformSettings(badURL); err == nil {
		t.Error("expected error for invalid supportUrl scheme")
	}
}

func TestValidateFileSettings(t *testing.T) {
	okSettings := FileSettings{
		Enabled:             true,
		DownloadEnabled:     true,
		UploadEnabled:       true,
		RequiresApproval:    true,
		DownloadApprovalTTL: 3600,
		UploadMaxSizeBytes:  1024 * 1024,
		Locations: []FileLocationSettings{
			{
				Name:    "my-location",
				Root:    absTestPath("data", "my-location"),
				Enabled: true,
			},
		},
	}
	if err := validateFileSettings(okSettings); err != nil {
		t.Errorf("expected file settings to be valid, got %v", err)
	}

	// Invalid upload max size (exceeding 100GB limit)
	badMaxSize := okSettings
	badMaxSize.UploadMaxSizeBytes = UploadMaxSizeBytesLimit + 1
	if err := validateFileSettings(badMaxSize); err == nil {
		t.Error("expected error for uploadMaxSizeBytes exceeding limit")
	}

	// Empty location name
	badLocName := okSettings
	badLocName.Locations = []FileLocationSettings{{Name: "", Root: "/abs"}}
	if err := validateFileSettings(badLocName); err == nil {
		t.Error("expected error for empty location name")
	}

	// Location name invalid pattern
	badLocPattern := okSettings
	badLocPattern.Locations = []FileLocationSettings{{Name: "UPPERCASE", Root: "/abs"}}
	if err := validateFileSettings(badLocPattern); err == nil {
		t.Error("expected error for location name violating regex")
	}

	// Reserved location name
	for _, reserved := range []string{"home", "releases", "trash"} {
		badLocReserved := okSettings
		badLocReserved.Locations = []FileLocationSettings{{Name: reserved, Root: "/abs"}}
		if err := validateFileSettings(badLocReserved); err == nil {
			t.Errorf("expected error for reserved location name %q", reserved)
		}
	}

	// Duplicate location name
	badLocDup := okSettings
	badLocDup.Locations = []FileLocationSettings{
		{Name: "loc-a", Root: "/abs/a"},
		{Name: "loc-a", Root: "/abs/b"},
	}
	if err := validateFileSettings(badLocDup); err == nil {
		t.Error("expected error for duplicate location name")
	}

	// Empty root
	badLocRoot := okSettings
	badLocRoot.Locations = []FileLocationSettings{{Name: "loc-a", Root: ""}}
	if err := validateFileSettings(badLocRoot); err == nil {
		t.Error("expected error for empty location root")
	}

	// Relative root
	badLocRel := okSettings
	badLocRel.Locations = []FileLocationSettings{{Name: "loc-a", Root: "relative/path"}}
	if err := validateFileSettings(badLocRel); err == nil {
		t.Error("expected error for relative location root")
	}

	// Invalid characters in root
	badLocChars := okSettings
	badLocChars.Locations = []FileLocationSettings{{Name: "loc-a", Root: "/abs\tpath"}}
	if err := validateFileSettings(badLocChars); err == nil {
		t.Error("expected error for invalid characters in root")
	}
}

func TestValidateTunnelSettings(t *testing.T) {
	okSettings := TunnelSettings{
		Enabled:           true,
		VncEnabled:        true,
		RdpEnabled:        true,
		SshEnabled:        false,
		MaxTunnels:        10,
		MaxTunnelsPerUser: 5,
		OtpTTL:            1800,
	}
	if err := validateTunnelSettings(okSettings); err != nil {
		t.Errorf("expected tunnel settings to be valid, got %v", err)
	}

	// MaxTunnels allowed value 0
	okSettings2 := okSettings
	okSettings2.MaxTunnels = 0
	if err := validateTunnelSettings(okSettings2); err != nil {
		t.Errorf("expected maxTunnels=0 to be allowed, got %v", err)
	}

	// Invalid MaxTunnels (e.g. -1)
	badMaxTunnels := okSettings
	badMaxTunnels.MaxTunnels = -1
	if err := validateTunnelSettings(badMaxTunnels); err == nil {
		t.Error("expected error for invalid maxTunnels")
	}

	// Invalid OtpTTL (below min 30)
	badOtpTTL := okSettings
	badOtpTTL.OtpTTL = 10
	if err := validateTunnelSettings(badOtpTTL); err == nil {
		t.Error("expected error for otpTTL < 30")
	}
}

func TestValidateAuthSettings(t *testing.T) {
	okSettings := AuthSettings{
		SessionTTL:                86400,
		MaxUserLoginAttempts:      5,
		MaxIPLoginAttempts:        20,
		UserBanDuration:           300,
		IPBanDuration:             86400,
		UserSyncInterval:          3600,
		IDMappingFoundCacheTTL:    86400,
		IDMappingNotFoundCacheTTL: 600,
		LdapWebAdminUrl:           "https://ldap.example.com",
	}
	if err := validateAuthSettings(okSettings); err != nil {
		t.Errorf("expected auth settings to be valid, got %v", err)
	}

	// AllowZero for UserSyncInterval
	okSettings2 := okSettings
	okSettings2.UserSyncInterval = 0
	okSettings2.LdapWebAdminUrl = ""
	if err := validateAuthSettings(okSettings2); err != nil {
		t.Errorf("expected auth settings with UserSyncInterval=0 to be valid, got %v", err)
	}

	badTTL := okSettings
	badTTL.SessionTTL = 0
	if err := validateAuthSettings(badTTL); err == nil {
		t.Error("expected error for invalid sessionTTL (0)")
	}

	badSync := okSettings
	badSync.UserSyncInterval = 10 // Min is 300
	if err := validateAuthSettings(badSync); err == nil {
		t.Error("expected error for too small userSyncInterval")
	}

	badLdap := okSettings
	badLdap.LdapWebAdminUrl = "ftp://invalid"
	if err := validateAuthSettings(badLdap); err == nil {
		t.Error("expected error for invalid ldapWebAdminUrl protocol")
	}

	// --- 身份缓存 TTL（idMappingFoundCacheTTL / idMappingNotFoundCacheTTL） ---

	// 典型配置：正 24h / 负 10min
	okIDMapping := okSettings
	okIDMapping.IDMappingFoundCacheTTL = 86400
	okIDMapping.IDMappingNotFoundCacheTTL = 600
	if err := validateAuthSettings(okIDMapping); err != nil {
		t.Errorf("expected ID mapping cache TTLs to be valid, got %v", err)
	}

	// 显式 0 非法，不能作为默认值哨兵。
	zeroIDMapping := okSettings
	zeroIDMapping.IDMappingFoundCacheTTL = 0
	zeroIDMapping.IDMappingNotFoundCacheTTL = 0
	if err := validateAuthSettings(zeroIDMapping); err == nil {
		t.Error("expected zero ID mapping cache TTLs to be rejected")
	}

	// 负 TTL 等于正 TTL：合法边界
	equalIDMapping := okSettings
	equalIDMapping.IDMappingFoundCacheTTL = 600
	equalIDMapping.IDMappingNotFoundCacheTTL = 600
	if err := validateAuthSettings(equalIDMapping); err != nil {
		t.Errorf("expected negative TTL equal to positive TTL to be valid, got %v", err)
	}

	// 跨字段：负 TTL 显式大于正 TTL → 必须拒绝
	badIDMapping := okSettings
	badIDMapping.IDMappingFoundCacheTTL = 700
	badIDMapping.IDMappingNotFoundCacheTTL = 800
	if err := validateAuthSettings(badIDMapping); err == nil {
		t.Error("expected error when idMappingNotFoundCacheTTL exceeds idMappingFoundCacheTTL")
	}

	// 越界：低于 min=600
	tooSmall := okSettings
	tooSmall.IDMappingFoundCacheTTL = 300
	if err := validateAuthSettings(tooSmall); err == nil {
		t.Error("expected error for idMappingFoundCacheTTL below minimum (600)")
	}

	// 越界：高于 max=604800
	tooBig := okSettings
	tooBig.IDMappingNotFoundCacheTTL = 700000
	if err := validateAuthSettings(tooBig); err == nil {
		t.Error("expected error for idMappingNotFoundCacheTTL above maximum (604800)")
	}
}

func TestValidateConnectionSettings(t *testing.T) {
	okSettings := ConnectionSettings{
		MaxConnectionsPerUser: 2,
	}
	if err := validateConnectionSettings(okSettings); err != nil {
		t.Errorf("expected connection settings to be valid, got %v", err)
	}

	badConnections := okSettings
	badConnections.MaxConnectionsPerUser = -1
	if err := validateConnectionSettings(badConnections); err == nil {
		t.Error("expected error for negative maxConnectionsPerUser")
	}
}

func TestValidateAgentSettings(t *testing.T) {
	okSettings := AgentSettings{
		LauncherPath: absTestPath("launcher"),
		VNC: VNCConfig{
			BinDir:       absTestPath("bin"),
			ScanInterval: 300,
			ScanEnabled:  true,
		},
		Monitor: MonitorConfig{
			NvidiaSmiPath: absTestPath("nvidia-smi"),
		},
	}
	if err := validateAgentSettings(okSettings); err != nil {
		t.Errorf("expected agent settings to be valid, got %v", err)
	}

	okSettingsEmpty := AgentSettings{}
	if err := validateAgentSettings(okSettingsEmpty); err != nil {
		t.Errorf("expected empty agent settings to be valid, got %v", err)
	}

	badLauncher := okSettings
	badLauncher.LauncherPath = "relative/path"
	if err := validateAgentSettings(badLauncher); err == nil {
		t.Error("expected error for relative LauncherPath")
	}

	badVNC := okSettings
	badVNC.VNC.BinDir = "relative/path"
	if err := validateAgentSettings(badVNC); err == nil {
		t.Error("expected error for relative VNC BinDir")
	}

	badNvidia := okSettings
	badNvidia.Monitor.NvidiaSmiPath = "relative/path"
	if err := validateAgentSettings(badNvidia); err == nil {
		t.Error("expected error for relative NvidiaSmiPath")
	}
}

func TestValidateDesktopSettings(t *testing.T) {
	// Success case: valid maxAssets within bounds
	okSettings := DesktopSettings{
		ReleasesEnabled: true,
		WindowsEnabled:  true,
		LinuxEnabled:    true,
		MacosEnabled:    false,
		MaxAssets:       5,
	}
	if err := validateDesktopSettings(okSettings); err != nil {
		t.Errorf("expected desktop settings to be valid, got %v", err)
	}

	// Boundary: maxAssets=1 and maxAssets=100 are both valid
	low := DesktopSettings{ReleasesEnabled: false, WindowsEnabled: true, MaxAssets: 1}
	if err := validateDesktopSettings(low); err != nil {
		t.Errorf("expected maxAssets=1 valid, got %v", err)
	}
	high := DesktopSettings{ReleasesEnabled: true, WindowsEnabled: true, MaxAssets: 100}
	if err := validateDesktopSettings(high); err != nil {
		t.Errorf("expected maxAssets=100 valid, got %v", err)
	}

	// maxAssets=0 violates min=1 (explicit 0 should be rejected; default comes from DefaultSettings)
	zero := DesktopSettings{ReleasesEnabled: true, MaxAssets: 0}
	if err := validateDesktopSettings(zero); err == nil {
		t.Error("expected error for maxAssets=0")
	}

	// maxAssets=101 violates max=100
	over := DesktopSettings{ReleasesEnabled: true, MaxAssets: 101}
	if err := validateDesktopSettings(over); err == nil {
		t.Error("expected error for maxAssets=101")
	}

	// Negative maxAssets rejected
	negative := DesktopSettings{ReleasesEnabled: true, MaxAssets: -3}
	if err := validateDesktopSettings(negative); err == nil {
		t.Error("expected error for negative maxAssets")
	}
}

func TestDecodeForStore(t *testing.T) {
	// Test DomainPlatform Decode
	platformJSON := json.RawMessage(`{"name":"My Platform","description":"Desc"}`)
	v, err := DecodeForStore(DomainPlatform, platformJSON)
	if err != nil {
		t.Fatalf("failed to decode platform settings: %v", err)
	}
	ps, ok := v.(PlatformSettings)
	if !ok || ps.Name != "My Platform" {
		t.Errorf("unexpected decoded PlatformSettings: %+v", v)
	}

	// Platform decode with website settings
	platformWithWebsiteJSON := json.RawMessage(`{"name":"My Platform","description":"Desc","website":{"indexEnabled":false,"dashboardEnabled":true}}`)
	vw, err := DecodeForStore(DomainPlatform, platformWithWebsiteJSON)
	if err != nil {
		t.Fatalf("failed to decode platform settings with website: %v", err)
	}
	psw := vw.(PlatformSettings)
	if psw.Website.IndexEnabled != false || psw.Website.DashboardEnabled != true {
		t.Errorf("unexpected decoded PlatformSettings.Website: %+v", psw.Website)
	}

	// Platform decode with validation error
	badPlatformJSON := json.RawMessage(`{"name":"","description":""}`)
	_, err = DecodeForStore(DomainPlatform, badPlatformJSON)
	if err == nil {
		t.Error("expected validation error for empty platform name")
	}

	// Test DomainFile Decode
	fileJSON := json.RawMessage(`{
		"enabled":true,
		"downloadEnabled":true,
		"uploadEnabled":true,
		"requiresApproval":false,
		"downloadApprovalTTL":3600,
		"uploadMaxSizeBytes":1024,
		"locations":[]
	}`)
	v, err = DecodeForStore(DomainFile, fileJSON)
	if err != nil {
		t.Fatalf("failed to decode file settings: %v", err)
	}
	fsSettings, ok := v.(FileSettings)
	if !ok || fsSettings.UploadMaxSizeBytes != 1024 {
		t.Errorf("unexpected decoded FileSettings: %+v", v)
	}

	// Test DomainTunnel Decode
	tunnelJSON := json.RawMessage(`{"enabled":true,"maxTunnels":10,"maxTunnelsPerUser":5,"otpTTL":1800,"vncEnabled":true,"rdpEnabled":true,"sshEnabled":false}`)
	v, err = DecodeForStore(DomainTunnel, tunnelJSON)
	if err != nil {
		t.Fatalf("failed to decode tunnel settings: %v", err)
	}
	tSettings, ok := v.(TunnelSettings)
	if !ok || tSettings.MaxTunnels != 10 || !tSettings.VncEnabled || !tSettings.RdpEnabled || tSettings.SshEnabled {
		t.Errorf("unexpected decoded TunnelSettings: %+v", v)
	}

	// Tunnel validation error
	badTunnelJSON := json.RawMessage(`{"enabled":true,"maxTunnels":-1,"maxTunnelsPerUser":5,"otpTTL":1800,"vncEnabled":true}`)
	_, err = DecodeForStore(DomainTunnel, badTunnelJSON)
	if err == nil {
		t.Error("expected validation error for bad tunnel sessions")
	}

	// Test DomainAuth Decode
	authJSON := json.RawMessage(`{
		"sessionTTL":3600,
		"maxLoginSessionsPerUser":10,
		"maxUserLoginAttempts":3,
		"maxIPLoginAttempts":10,
		"userBanDuration":600,
		"ipBanDuration":3600,
		"idMappingFoundCacheTTL":86400,
		"idMappingNotFoundCacheTTL":600
	}`)
	v, err = DecodeForStore(DomainAuth, authJSON)
	if err != nil {
		t.Fatalf("failed to decode auth settings: %v", err)
	}
	authSettings, ok := v.(AuthSettings)
	if !ok || authSettings.SessionTTL != 3600 {
		t.Errorf("unexpected decoded AuthSettings: %+v", v)
	}

	// Auth validation error
	badAuthJSON := json.RawMessage(`{
		"sessionTTL":10,
		"maxUserLoginAttempts":5,
		"maxIPLoginAttempts":20,
		"userBanDuration":300,
		"ipBanDuration":300
	}`)
	_, err = DecodeForStore(DomainAuth, badAuthJSON)
	if err == nil {
		t.Error("expected validation error for bad auth session TTL")
	}

	// Test DomainConnection Decode
	connectionJSON := json.RawMessage(`{
		"maxConnectionsPerUser":2
	}`)
	v, err = DecodeForStore(DomainConnection, connectionJSON)
	if err != nil {
		t.Fatalf("failed to decode connection settings: %v", err)
	}
	cSettings, ok := v.(ConnectionSettings)
	if !ok || cSettings.MaxConnectionsPerUser != 2 {
		t.Errorf("unexpected decoded ConnectionSettings: %+v", v)
	}

	// Test DomainDashboard Decode（字段省略时回退零值）
	dashboardJSON := json.RawMessage(`{}`)
	dashV, err := DecodeForStore(DomainDashboard, dashboardJSON)
	if err != nil {
		t.Fatalf("failed to decode dashboard settings (empty): %v", err)
	}
	dSettings, ok := dashV.(DashboardSettings)
	if !ok {
		t.Fatalf("unexpected decoded DashboardSettings: %+v", dashV)
	}
	if dSettings.SchedulerRefreshInterval != 0 || dSettings.NodeStatusRefreshInterval != 0 {
		t.Errorf("expected zero values when fields omitted, got %+v", dSettings)
	}

	// Dashboard validation error（scheduler TTL 低于下限 90）
	badDashboardJSON := json.RawMessage(`{"schedulerRefreshInterval":60}`)
	_, err = DecodeForStore(DomainDashboard, badDashboardJSON)
	if err == nil {
		t.Error("expected validation error for schedulerRefreshInterval below min 90")
	}

	// Test DomainScheduler Decode（字段省略时回退零值）
	schedulerJSON := json.RawMessage(`{}`)
	schedV, err := DecodeForStore(DomainScheduler, schedulerJSON)
	if err != nil {
		t.Fatalf("failed to decode scheduler settings (empty): %v", err)
	}
	schedSettings, ok := schedV.(SchedulerSettings)
	if !ok {
		t.Fatalf("unexpected decoded SchedulerSettings: %+v", schedV)
	}
	if schedSettings.JobCacheTTL != 0 {
		t.Errorf("expected zero-value JobCacheTTL when omitted, got %d", schedSettings.JobCacheTTL)
	}

	// Scheduler validation error（job cache TTL 低于下限 30）
	badSchedulerJSON := json.RawMessage(`{"jobCacheTTL":10}`)
	_, err = DecodeForStore(DomainScheduler, badSchedulerJSON)
	if err == nil {
		t.Error("expected validation error for jobCacheTTL below min 30")
	}

	// Test DomainAgent Decode
	agentJSON := json.RawMessage(`{
		"launcherPath":"` + filepath.ToSlash(absTestPath("launcher")) + `",
		"vnc":{
			"binDir":"` + filepath.ToSlash(absTestPath("bin")) + `",
			"scanInterval":300,
			"scanEnabled":true
		},
		"monitor":{
			"nvidiaSmiPath":"",
			"diskMounts":[],
			"resourceReportEnabled":true,
			"resourceReportInterval":60
		}
	}`)
	v, err = DecodeForStore(DomainAgent, agentJSON)
	if err != nil {
		t.Fatalf("failed to decode agent settings: %v", err)
	}
	agSettings, ok := v.(AgentSettings)
	if !ok || agSettings.LauncherPath != filepath.ToSlash(absTestPath("launcher")) || agSettings.VNC.BinDir != filepath.ToSlash(absTestPath("bin")) {
		t.Errorf("unexpected decoded AgentSettings: %+v", v)
	}

	// Agent validation error (relative vnc binDir)
	badAgentJSON := json.RawMessage(`{
		"launcherPath":"` + filepath.ToSlash(absTestPath("launcher")) + `",
		"vnc":{
			"binDir":"relative",
			"scanInterval":300,
			"scanEnabled":true
		},
		"monitor":{
			"nvidiaSmiPath":"",
			"diskMounts":[],
			"resourceReportEnabled":true,
			"resourceReportInterval":60
		}
	}`)
	_, err = DecodeForStore(DomainAgent, badAgentJSON)
	if err == nil {
		t.Error("expected validation error for relative vnc.binDir in DomainAgent")
	}

	// Test DomainStorage Decode
	storageJSON := json.RawMessage(`{
		"storages":[{
			"id":"project-nfs",
			"name":"Project NFS",
			"type":"nfs",
			"mountPoint":"/mnt/project",
			"probeNodes":["node-1","node-2"]
		}],
		"probeInterval":3600
	}`)
	v, err = DecodeForStore(DomainStorage, storageJSON)
	if err != nil {
		t.Fatalf("failed to decode storage settings: %v", err)
	}
	sSettings, ok := v.(StorageSettings)
	if !ok || len(sSettings.Storages) != 1 || sSettings.Storages[0].ID != "project-nfs" {
		t.Errorf("unexpected decoded StorageSettings: %+v", v)
	}

	// Storage validation error
	badStorageJSON := json.RawMessage(`{
		"storages":[{
			"id":"",
			"name":"Bad",
			"type":"nfs",
			"mountPoint":"/mnt/project",
			"probeNodes":["node-1"]
		}]
	}`)
	_, err = DecodeForStore(DomainStorage, badStorageJSON)
	if err == nil {
		t.Error("expected validation error for empty storage id")
	}

	// Test DomainDesktop Decode
	desktopJSON := json.RawMessage(`{"releasesEnabled":true,"windowsEnabled":true,"linuxEnabled":true,"macosEnabled":false,"maxAssets":5}`)
	v, err = DecodeForStore(DomainDesktop, desktopJSON)
	if err != nil {
		t.Fatalf("failed to decode desktop settings: %v", err)
	}
	deskSettings, ok := v.(DesktopSettings)
	if !ok || !deskSettings.ReleasesEnabled || !deskSettings.WindowsEnabled || deskSettings.MacosEnabled || deskSettings.MaxAssets != 5 {
		t.Errorf("unexpected decoded DesktopSettings: %+v", v)
	}

	// Desktop validation error: maxAssets above the 100 bound
	badDesktopJSON := json.RawMessage(`{"releasesEnabled":true,"windowsEnabled":true,"maxAssets":500}`)
	_, err = DecodeForStore(DomainDesktop, badDesktopJSON)
	if err == nil {
		t.Error("expected validation error for maxAssets=500")
	}

	// Unknown Domain Decode
	v, err = DecodeForStore("unknown", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error for unknown domain: %v", err)
	}
	if v != nil {
		t.Errorf("expected nil result for unknown domain, got %v", v)
	}

	// JSON Unmarshal failure
	_, err = DecodeForStore(DomainPlatform, json.RawMessage(`invalid json`))
	if err == nil {
		t.Error("expected JSON unmarshal error")
	}

	// DisallowUnknownFields / decoder failure (unknown field)
	badFieldsJSON := json.RawMessage(`{"name":"My Platform","description":"Desc","unknown_field":"value"}`)
	_, err = DecodeForStore(DomainPlatform, badFieldsJSON)
	if err == nil {
		t.Error("expected error for unknown fields in payload")
	}

	// Missing required fields failure
	missingFieldsJSON := json.RawMessage(`{"description":"Desc"}`)
	_, err = DecodeForStore(DomainPlatform, missingFieldsJSON)
	if err == nil {
		t.Error("expected error for missing required field (name)")
	}

	// Extra tokens after single object
	extraTokensJSON := json.RawMessage(`{"name":"My Platform","description":"Desc"} {}`)
	_, err = DecodeForStore(DomainPlatform, extraTokensJSON)
	if err == nil {
		t.Error("expected error for trailing JSON tokens")
	}
}

func TestValidateStorageSettings(t *testing.T) {
	okSettings := StorageSettings{
		Storages: []StorageConfig{
			{
				ID:         "project-nfs",
				Name:       "Project NFS",
				Type:       "nfs",
				MountPoint: "/mnt/project",
				ProbeNodes: []string{"node-1", "node-2"},
			},
		},
	}
	if err := validateStorageSettings(okSettings); err != nil {
		t.Errorf("expected storage settings to be valid, got %v", err)
	}

	// Invalid probeInterval (below minimum 180s)
	badProbeInterval := okSettings
	badProbeInterval.ProbeInterval = 10
	if err := validateStorageSettings(badProbeInterval); err == nil {
		t.Error("expected error for probeInterval below minimum limit")
	}

	// Empty id
	badStorageID := okSettings
	badStorageID.Storages[0].ID = ""
	if err := validateStorageSettings(badStorageID); err == nil {
		t.Error("expected error for empty id")
	}

	// Duplicate id
	badDup := okSettings
	badDup.Storages = append(badDup.Storages, okSettings.Storages[0])
	if err := validateStorageSettings(badDup); err == nil {
		t.Error("expected error for duplicate id")
	}

	// Empty name
	badName := okSettings
	badName.Storages[0].Name = ""
	if err := validateStorageSettings(badName); err == nil {
		t.Error("expected error for empty name")
	}

	// Empty mountPoint
	badMount := okSettings
	badMount.Storages[0].MountPoint = ""
	if err := validateStorageSettings(badMount); err == nil {
		t.Error("expected error for empty mountPoint")
	}

	// Empty probeNodes
	badNodes := okSettings
	badNodes.Storages[0].ProbeNodes = []string{}
	if err := validateStorageSettings(badNodes); err == nil {
		t.Error("expected error for empty probeNodes")
	}

	// Invalid type
	badType := okSettings
	badType.Storages[0].Type = "unknown"
	if err := validateStorageSettings(badType); err == nil {
		t.Error("expected error for invalid type")
	}
}

func validAlertSettings() AlertSettings {
	return AlertSettings{
		Enabled:       true,
		CPU:           CPUAlertConfig{Enabled: false, MaxUsedPercent: 0, CooldownInterval: 300},
		Load:          LoadAlertConfig{Enabled: false, MaxLoadPercent: 0, CooldownInterval: 300},
		Memory:        MemoryAlertConfig{Enabled: true, MaxUsedPercent: 85, MinFreeSpace: 0, CooldownInterval: 300},
		Disk:          DiskAlertConfig{Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 300},
		SharedStorage: StorageAlertConfig{Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 1800},
	}
}

func TestValidateAlertSettings(t *testing.T) {
	// 合法：完整配置（Memory=85，Disk.MinFreeSpace=0 且 MaxUsedPercent=90）
	if err := validateAlertSettings(validAlertSettings()); err != nil {
		t.Errorf("expected alert settings to be valid, got %v", err)
	}

	// 越界：Memory.MaxUsedPercent=101 → 非法
	badMemory := validAlertSettings()
	badMemory.Memory.MaxUsedPercent = 101
	if err := validateAlertSettings(badMemory); err == nil {
		t.Error("expected validation error for memory.maxUsedPercent > 100")
	}

	// 越界：CooldownInterval 低于下限 60 → 非法
	badCooldown := validAlertSettings()
	badCooldown.Memory.CooldownInterval = 30
	if err := validateAlertSettings(badCooldown); err == nil {
		t.Error("expected validation error for cooldownInterval < 60")
	}

	// 交叉非法：Memory 启用但两个阈值维度均为 0 → 422
	badMemoryZero := validAlertSettings()
	badMemoryZero.Memory.MaxUsedPercent = 0
	badMemoryZero.Memory.MinFreeSpace = 0
	if err := validateAlertSettings(badMemoryZero); err == nil {
		t.Error("expected cross-field error for memory enabled with zero thresholds")
	}

	// 交叉非法：SharedStorage 启用但两个阈值维度均为 0 → 422
	badStorage := validAlertSettings()
	badStorage.SharedStorage.MaxUsedPercent = 0
	badStorage.SharedStorage.MinFreeSpace = 0
	if err := validateAlertSettings(badStorage); err == nil {
		t.Error("expected cross-field error for sharedStorage enabled with zero thresholds")
	}

	// 交叉非法：Disk 启用但两个阈值维度均为 0 → 422
	badDisk := validAlertSettings()
	badDisk.Disk.MaxUsedPercent = 0
	badDisk.Disk.MinFreeSpace = 0
	if err := validateAlertSettings(badDisk); err == nil {
		t.Error("expected cross-field error for disk enabled with zero thresholds")
	}
}

func TestDefaultSettings(t *testing.T) {
	for _, domain := range AllDomains() {
		val, ok := DefaultSettings(domain)
		if !ok {
			t.Errorf("expected DefaultSettings to succeed for domain: %s", domain)
		}
		if val == nil {
			t.Errorf("expected non-nil default value for domain: %s", domain)
		}

		// 不变量：默认值本身必须通过该域的校验。
		// 默认值会被 EnsureDefaults 直接写入 DB、也是读取侧校验失败时的回退目标——
		// 若默认值非法，"回退到默认"就等于回退到脏数据，且管理员无法通过任何 PUT 修复。
		raw, err := json.Marshal(val)
		if err != nil {
			t.Errorf("marshal defaults for %s: %v", domain, err)
			continue
		}
		decoded, err := DecodeForStore(domain, raw)
		if err != nil {
			t.Errorf("defaults for %s must pass their own validation, got: %v", domain, err)
			continue
		}
		// 按 JSON 比较（而非 DeepEqual）：nil 切片与空切片在语义上等价，
		// DeepEqual 会将其视为不同而误报。
		roundTripped, err := json.Marshal(decoded)
		if err != nil {
			t.Errorf("marshal decoded defaults for %s: %v", domain, err)
			continue
		}
		if !bytes.Equal(raw, roundTripped) {
			t.Errorf("defaults for %s changed when round-tripped through decode: %s -> %s", domain, raw, roundTripped)
		}
	}

	_, ok := DefaultSettings("invalid-domain")
	if ok {
		t.Error("expected DefaultSettings to fail for invalid domain")
	}
}

func TestTunnelProtocolHelpers(t *testing.T) {
	tSettings := TunnelSettings{
		Enabled:    true,
		VncEnabled: true,
		RdpEnabled: false,
		SshEnabled: false,
	}

	// IsProtocolAllowed
	if !tSettings.IsProtocolAllowed(contract.VNCProtocol) {
		t.Error("expected VNC to be allowed")
	}
	if tSettings.IsProtocolAllowed(contract.RDPProtocol) {
		t.Error("expected RDP to be blocked")
	}
	if tSettings.IsProtocolAllowed(contract.SSHProtocol) {
		t.Error("expected SSH to be blocked")
	}
	if tSettings.IsProtocolAllowed("") {
		t.Error("expected empty protocol to be rejected")
	}
	if tSettings.IsProtocolAllowed("UNKNOWN") {
		t.Error("expected unknown protocol to be rejected")
	}
}

func TestDesktopPlatformHelpers(t *testing.T) {
	d := DesktopSettings{
		ReleasesEnabled: true,
		WindowsEnabled:  true,
		LinuxEnabled:    false,
		MacosEnabled:    true,
	}

	if !d.IsPlatformAllowed("win") || !d.IsPlatformAllowed("windows") || !d.IsPlatformAllowed("") {
		t.Error("expected Windows to be allowed")
	}
	if d.IsPlatformAllowed("linux") {
		t.Error("expected Linux to be blocked")
	}
	if !d.IsPlatformAllowed("macos") || !d.IsPlatformAllowed("osx") || !d.IsPlatformAllowed("darwin") {
		t.Error("expected macOS to be allowed")
	}
	if d.IsPlatformAllowed("freebsd") {
		t.Error("expected unknown platform to be blocked")
	}

	// When releasesEnabled is false, all platforms blocked
	d.ReleasesEnabled = false
	if d.IsPlatformAllowed("windows") || d.IsPlatformAllowed("macos") {
		t.Error("expected all platforms blocked when releasesEnabled=false")
	}
}

func TestStorageHelpers(t *testing.T) {
	s := StorageSettings{
		Storages: []StorageConfig{
			{
				Enabled:    true,
				ID:         "nfs-1",
				ProbeNodes: []string{"node-a", "node-b"},
			},
			{
				Enabled:    false,
				ID:         "nfs-2",
				ProbeNodes: []string{"node-a"},
			},
		},
		ProbeInterval: 0, // Should fallback to default 3600
	}

	if s.EffectiveProbeInterval() != StorageProbeIntervalDefault {
		t.Errorf("expected EffectiveProbeInterval=%d, got %d", StorageProbeIntervalDefault, s.EffectiveProbeInterval())
	}
	s.ProbeInterval = 600
	if s.EffectiveProbeInterval() != 600 {
		t.Errorf("expected EffectiveProbeInterval=600, got %d", s.EffectiveProbeInterval())
	}

	if !s.HasEnabled() {
		t.Error("expected HasEnabled to be true")
	}
	enabled := s.EnabledStorages()
	if len(enabled) != 1 || enabled[0].ID != "nfs-1" {
		t.Errorf("expected 1 enabled storage nfs-1, got %+v", enabled)
	}

	if !s.Storages[0].ShouldProbeOnNode("node-a") {
		t.Error("expected node-a to probe nfs-1")
	}
	if s.Storages[0].ShouldProbeOnNode("node-c") {
		t.Error("expected node-c not to probe nfs-1")
	}
	if s.Storages[1].ShouldProbeOnNode("node-a") {
		t.Error("expected disabled storage not to be probed")
	}
}

func TestFileLocationHelpers(t *testing.T) {
	f := FileSettings{
		Locations: []FileLocationSettings{
			{
				Enabled: true,
				Name:    "dataset",
				Root:    "/data/set",
			},
		},
	}

	loc, ok := f.FindLocation("dataset")
	if !ok || loc.Root != "/data/set" {
		t.Errorf("expected to find dataset location, got %+v (ok=%v)", loc, ok)
	}
	_, ok = f.FindLocation("nonexistent")
	if ok {
		t.Error("expected nonexistent location not to be found")
	}
}

func TestLicenseSettingsValidation(t *testing.T) {
	// 1. Default settings
	def, ok := DefaultSettings(DomainLicense)
	if !ok {
		t.Fatal("expected default settings for DomainLicense")
	}
	licDef, ok := def.(LicenseSettings)
	if !ok {
		t.Fatalf("expected LicenseSettings, got %T", def)
	}
	if licDef.StatusCacheTTL != 60 || licDef.FeaturesCacheTTL != 60 {
		t.Errorf("unexpected default license settings: %+v", licDef)
	}

	// 2. Decode empty JSON (allowed, zero values)
	rawEmpty := json.RawMessage(`{}`)
	v, err := DecodeForStore(DomainLicense, rawEmpty)
	if err != nil {
		t.Fatalf("expected empty JSON to be decoded successfully, got err: %v", err)
	}
	licDec, ok := v.(LicenseSettings)
	if !ok {
		t.Fatalf("expected LicenseSettings, got %T", v)
	}
	if licDec.StatusCacheTTL != 0 || licDec.FeaturesCacheTTL != 0 {
		t.Errorf("expected zero values for empty JSON, got %+v", licDec)
	}

	// 3. Valid non-zero values
	rawValid := json.RawMessage(`{"statusCacheTTL": 30, "featuresCacheTTL": 600}`)
	v, err = DecodeForStore(DomainLicense, rawValid)
	if err != nil {
		t.Fatalf("expected valid JSON to pass, got err: %v", err)
	}
	licValid := v.(LicenseSettings)
	if licValid.StatusCacheTTL != 30 || licValid.FeaturesCacheTTL != 600 {
		t.Errorf("unexpected decoded values: %+v", licValid)
	}

	// 4. Invalid statusCacheTTL: below 30
	rawBelowMin := json.RawMessage(`{"statusCacheTTL": 10}`)
	if _, err = DecodeForStore(DomainLicense, rawBelowMin); err == nil {
		t.Error("expected statusCacheTTL=10 to fail validation (below min=30)")
	}

	// 5. Invalid featuresCacheTTL: above 600
	rawAboveMax := json.RawMessage(`{"featuresCacheTTL": 601}`)
	if _, err = DecodeForStore(DomainLicense, rawAboveMax); err == nil {
		t.Error("expected featuresCacheTTL=601 to fail validation (above max=600)")
	}

	// 6. Unknown field rejected
	rawUnknown := json.RawMessage(`{"statusCacheTTL": 60, "unknownField": 123}`)
	if _, err = DecodeForStore(DomainLicense, rawUnknown); err == nil {
		t.Error("expected unknown field to be rejected")
	}
}
