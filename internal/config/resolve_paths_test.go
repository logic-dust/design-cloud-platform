package config

import (
	"os"
	"path/filepath"
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
max_age = 30
max_size = 100
max_backups = 10
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
[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
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
max_age = 30
max_size = 100
max_backups = 10
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
[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
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
max_age = 30
max_size = 100
max_backups = 10
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
[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
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
max_age = 30
max_size = 100
max_backups = 10
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
[scheduler]
type = "lsf"
env_script = "/share/apps/lsf/conf/profile.lsf"
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
