package config

import (
	"os"
	"path/filepath"
	"testing"
)

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

// 引导类环境变量 DCE_CONFIG_FILE 已废弃：即使设置，也必须仍然读取默认 config.toml。
func TestLoad_IgnoresLegacyConfigFileEnv(t *testing.T) {
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
	t.Setenv("DCE_CONFIG_FILE", filepath.Join(dir, "does-not-exist.toml"))

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load must succeed from default config.toml (legacy env ignored): %v", err)
	}
	if cfg.Cluster.Port != DefaultClusterPort {
		t.Fatalf("Cluster.Port = %d, want %d", cfg.Cluster.Port, DefaultClusterPort)
	}
}
