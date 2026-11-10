package config

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestExampleContentRoundTrip 验证 GetExampleContent 生成的配置能被反解析（round-trip）
func TestExampleContentRoundTrip(t *testing.T) {
	text, err := GetExampleContent()
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
	if len(cfg.Cluster.Servers) != 2 {
		t.Errorf("len(Servers) = %d, want 2", len(cfg.Cluster.Servers))
	}
	if cfg.Cluster.Servers[0] != "cent7" || cfg.Cluster.Servers[1] != "arch" {
		t.Errorf("Servers = %+v, want [cent7, arch]", cfg.Cluster.Servers)
	}
	if cfg.Database.Type != DatabaseTypePostgres {
		t.Errorf("Database.Type = %q, want postgres", cfg.Database.Type)
	}
	if cfg.Database.Name != "dce" {
		t.Errorf("Database.Name = %q, want dce", cfg.Database.Name)
	}
	if cfg.DataDir != defaultPlatformDataDir() {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, defaultPlatformDataDir())
	}
	if cfg.Log.Dir != "/var/log/design-cloud" {
		t.Errorf("Log.Dir = %q, want /var/log/design-cloud", cfg.Log.Dir)
	}
	if cfg.Database.PasswordFile != "/etc/design-cloud/dbpwd" {
		t.Errorf("PasswordFile = %q, want /etc/design-cloud/dbpwd", cfg.Database.PasswordFile)
	}
	if cfg.User.Ldap.BindPasswordFile != "/etc/design-cloud/ldapbindpwd" {
		t.Errorf("BindPasswordFile = %q, want /etc/design-cloud/ldapbindpwd", cfg.User.Ldap.BindPasswordFile)
	}
}
