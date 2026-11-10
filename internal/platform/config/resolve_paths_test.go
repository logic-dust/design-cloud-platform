package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_InheritedFHSDataDirUnlessCleared(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// Omit data_dir → Load keeps getDefault()'s packaged FHS DataDir.
	content := `
[log]
dir = "logs"
app_level = "INFO"
access_level = "INFO"
[portal]
port = "8443"
[cluster]
port = 52255
servers = ["nonexistent-host-for-agent-role"]
[database]
type = "sqlite"
path = "dce.sqlite.db"
password_file = ""
max_idle_conns = 10
max_open_conns = 25
[user]
auth_mode = "local"
[user.ldap]
bind_password_file = ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.DataDir != "/var/lib/design-cloud" {
		t.Fatalf("omitted data_dir must inherit FHS default: got %q", cfg.Runtime.DataDir)
	}
	if cfg.Runtime.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want %q", cfg.Runtime.BaseDir, dir)
	}
}

func TestResolve_RelativeSecretsJoinBaseDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
data_dir = ""
[log]
dir = "logs"
app_level = "INFO"
access_level = "INFO"
[portal]
port = "8443"
[cluster]
port = 52255
servers = ["nonexistent-host-for-agent-role"]
[database]
type = "sqlite"
path = "dce.sqlite.db"
password_file = ""
max_idle_conns = 10
max_open_conns = 25
[user]
auth_mode = "local"
`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want %q", cfg.Runtime.BaseDir, dir)
	}
	if cfg.Runtime.DataDir != dir {
		t.Fatalf("empty data_dir must fall back to BaseDir: got %q", cfg.Runtime.DataDir)
	}
	if cfg.Log.Dir != filepath.Join(dir, "logs") {
		t.Fatalf("Log.Dir = %q, want under BaseDir", cfg.Log.Dir)
	}
	if got := cfg.ClusterCACertPath(); got != filepath.Join(dir, "certs", "cluster", "ca.crt") {
		t.Fatalf("CA under DataDir(=BaseDir): %q", got)
	}
}

func TestResolve_ExplicitDataDirForCerts(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "state")
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
data_dir = "` + dataDir + `"
[log]
dir = "/var/log/design-cloud"
app_level = "INFO"
access_level = "INFO"
[portal]
port = "8443"
[cluster]
port = 52255
servers = ["nonexistent-host-for-agent-role"]
[database]
type = "sqlite"
path = "dce.sqlite.db"
password_file = ""
max_idle_conns = 10
max_open_conns = 25
[user]
auth_mode = "local"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want %q", cfg.Runtime.BaseDir, dir)
	}
	if cfg.Runtime.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", cfg.Runtime.DataDir, dataDir)
	}
	if got := cfg.ClusterNodeCertPath("peer"); got != filepath.Join(dataDir, "certs", "cluster", "peer", "peer.crt") {
		t.Fatalf("cert path = %q", got)
	}
	// Relative sqlite still joins BaseDir, not DataDir
	if cfg.Database.Path != filepath.Join(dir, "dce.sqlite.db") {
		t.Fatalf("sqlite path = %q, want under BaseDir", cfg.Database.Path)
	}
}

func TestResolve_HalfConfiguredPortalCertFails(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// Only cert is set: resolve() must not silently supply the missing key.
	content := `
[portal]
port = "8443"
cert = "portal.crt"
[cluster]
port = 52255
servers = ["` + hostname + `"]
[database]
password_file = ""
[user]
auth_mode = "local"
[user.ldap]
bind_password_file = ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "cert and key must both be set") {
		t.Fatalf("half-configured portal cert/key must fail validation, got %v", err)
	}
}

func TestResolve_RelativePasswordFileJoinsBaseDir(t *testing.T) {
	dir := t.TempDir()
	pwd := filepath.Join(dir, "dbpwd")
	if err := os.WriteFile(pwd, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
[log]
dir = "logs"
app_level = "INFO"
access_level = "INFO"
[portal]
port = "8443"
[cluster]
port = 52255
servers = ["` + hostname + `"]
[database]
type = "sqlite"
path = "dce.sqlite.db"
password_file = "dbpwd"
max_idle_conns = 10
max_open_conns = 25
[user]
auth_mode = "local"
[user.ldap]
bind_password_file = ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.PasswordFile != pwd {
		t.Fatalf("PasswordFile = %q, want %q", cfg.Database.PasswordFile, pwd)
	}
	if cfg.Database.Password != "secret" {
		t.Fatalf("Password = %q, want secret", cfg.Database.Password)
	}
}
