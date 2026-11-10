package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestLoadClusterZeroRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const body = `
[cluster]
servers = ["cent7"]
lease_ttl = 0
renew_interval = 0
execution_retention_days = 0
[database]
type = "postgres"
max_open_conns = 10
max_idle_conns = 5
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to reject lease_ttl/renew_interval/execution_retention_days = 0")
	}
}

func TestPortalRateLimitZeroRejected(t *testing.T) {
	p := Portal{
		Port: "8080", ReadHeaderTimeout: 8, IdleTimeout: 60,
		RateLimit: 0, RateBurst: 600,
		TransferRateLimit: 10, TransferRateBurst: 30,
		WSRateLimit: 10, WSRateBurst: 30,
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected validation error for rate_limit=0")
	}
}

func TestEnvOverrides_LoggingAndMasking(t *testing.T) {
	t.Setenv("DCE_PORTAL_PORT", "8080")
	t.Setenv("DCE_DATABASE_HOST", "db.example.com")
	t.Setenv("DCE_DATABASE_PASSWORD", "super-secret")

	cfg := getDefault()
	overrides := applyEnvOverrides(&cfg)

	if cfg.Portal.Port != "8080" || cfg.Database.Host != "db.example.com" {
		t.Fatalf("non-sensitive overrides not applied: %+v", cfg)
	}
	if cfg.Database.Password != "super-secret" {
		t.Fatal("database password override not applied to config")
	}

	byKey := make(map[string]envOverride, len(overrides))
	for _, o := range overrides {
		byKey[o.key] = o
	}
	if o, ok := byKey["portal.port"]; !ok || o.sensitive || o.value != "8080" {
		t.Fatalf("portal.port override malformed: %+v", o)
	}
	if o, ok := byKey["database.password"]; !ok || !o.sensitive {
		t.Fatalf("database.password must be recorded as sensitive: %+v", o)
	}
}

func TestEnvOverrides_InvalidLogLevelDoesNotBlockLaterOverrides(t *testing.T) {
	t.Setenv("DCE_LOG_APP_LEVEL", "VERBOSE")
	t.Setenv("DCE_PORTAL_PORT", "9090")
	t.Setenv("DCE_DATABASE_HOST", "db.example.com")

	cfg := getDefault()
	overrides := applyEnvOverrides(&cfg)

	if cfg.Portal.Port != "9090" || cfg.Database.Host != "db.example.com" {
		t.Fatalf("later overrides were skipped: %+v", cfg)
	}
	if cfg.Log.AppLevel != slog.LevelInfo {
		t.Fatalf("invalid level must be ignored, got %v", cfg.Log.AppLevel)
	}
	for _, o := range overrides {
		if o.key == "log.app_level" {
			t.Fatal("invalid level must not be recorded as an override")
		}
	}
}

func TestLoad_UsesDefaultConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	content := `
[cluster]
port = 52255
servers = ["mgt01"]
`
	path := filepath.Join(dir, DefaultConfigFile)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", DefaultConfigFile, err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load from default config.toml: %v", err)
	}
	if cfg.Cluster.Port != DefaultClusterPort {
		t.Fatalf("Cluster.Port = %d, want %d", cfg.Cluster.Port, DefaultClusterPort)
	}
}

// TestExampleContentRoundTrip 验证默认示例配置能被反解析（round-trip）
func TestExampleContentRoundTrip(t *testing.T) {
	text, err := GetExampleContentWithOptions(ExampleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := toml.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatalf("generated example content cannot be parsed: %v", err)
	}
	// 关键字段抽查
	if cfg.Portal.Port != "443" {
		t.Errorf("Portal.Port = %q, want 443", cfg.Portal.Port)
	}
	if cfg.Log.AppLevel.String() != "INFO" {
		t.Errorf("Log.AppLevel = %v, want INFO", cfg.Log.AppLevel)
	}
	if len(cfg.Cluster.Servers) != 1 || cfg.Cluster.Servers[0] != "mgt01" {
		t.Errorf("Servers = %+v, want [mgt01]", cfg.Cluster.Servers)
	}
	if cfg.Database.Type != DatabaseTypePostgres {
		t.Errorf("Database.Type = %q, want postgres", cfg.Database.Type)
	}
	if cfg.Database.Name != "dce" {
		t.Errorf("Database.Name = %q, want dce", cfg.Database.Name)
	}
	if cfg.StateDir != defaultPlatformStateDir() {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, defaultPlatformStateDir())
	}
	if cfg.Log.Dir != "/var/log/design-cloud" {
		t.Errorf("Log.Dir = %q, want /var/log/design-cloud", cfg.Log.Dir)
	}
	if cfg.Portal.Cert != defaultPortalCertPath || cfg.Portal.Key != defaultPortalKeyPath {
		t.Errorf("Portal cert/key = (%q,%q), want (%q,%q)", cfg.Portal.Cert, cfg.Portal.Key, defaultPortalCertPath, defaultPortalKeyPath)
	}
	if cfg.Database.PasswordFile != "/etc/design-cloud/dbpwd" {
		t.Errorf("PasswordFile = %q, want /etc/design-cloud/dbpwd", cfg.Database.PasswordFile)
	}
	if cfg.User.Ldap.BindPasswordFile != "/etc/design-cloud/ldapbindpwd" {
		t.Errorf("BindPasswordFile = %q, want /etc/design-cloud/ldapbindpwd", cfg.User.Ldap.BindPasswordFile)
	}
	if cfg.Portal.RateLimit != 200 || cfg.Portal.TransferRateLimit != 10 || cfg.Portal.WSRateLimit != 10 {
		t.Errorf("portal rate limits = (%d,%d,%d), want (200,10,10)", cfg.Portal.RateLimit, cfg.Portal.TransferRateLimit, cfg.Portal.WSRateLimit)
	}
	if cfg.Cluster.LeaseTTL != DefaultLeaseTTLSeconds || cfg.Cluster.RenewInterval != DefaultRenewIntervalSeconds {
		t.Errorf("cluster lease = (%d,%d), want (%d,%d)", cfg.Cluster.LeaseTTL, cfg.Cluster.RenewInterval, DefaultLeaseTTLSeconds, DefaultRenewIntervalSeconds)
	}
	if cfg.Cluster.ExecutionRetentionDays != DefaultExecutionRetentionDays {
		t.Errorf("ExecutionRetentionDays = %d, want %d", cfg.Cluster.ExecutionRetentionDays, DefaultExecutionRetentionDays)
	}
}

func TestGetExampleContentWithOptions(t *testing.T) {
	text, err := GetExampleContentWithOptions(ExampleOptions{
		Servers:  []string{"mgt01", "mgt02"},
		LogLevel: "DEBUG",
	})
	if err != nil {
		t.Fatalf("GetExampleContentWithOptions failed: %v", err)
	}

	var cfg Config
	if err := toml.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatalf("unmarshal generated config: %v", err)
	}
	if len(cfg.Cluster.Servers) != 2 || cfg.Cluster.Servers[0] != "mgt01" || cfg.Cluster.Servers[1] != "mgt02" {
		t.Errorf("Cluster.Servers = %v, want [mgt01 mgt02]", cfg.Cluster.Servers)
	}
	if cfg.Log.AppLevel != slog.LevelDebug {
		t.Errorf("Log.AppLevel = %v, want DEBUG", cfg.Log.AppLevel)
	}

	if _, err := GetExampleContentWithOptions(ExampleOptions{LogLevel: "TRACE"}); err == nil {
		t.Error("expected error for invalid log level")
	}
	if _, err := GetExampleContentWithOptions(ExampleOptions{Servers: []string{"bad host"}}); err == nil {
		t.Error("expected error for invalid server host")
	}
}
