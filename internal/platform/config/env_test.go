package config

import (
	"log/slog"
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
