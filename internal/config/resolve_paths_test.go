package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolve_InheritedFHSStateDirUnlessCleared(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// Omit state_dir → Load keeps getDefault()'s packaged FHS state_dir.
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
	if cfg.Runtime.StateRoot != "/var/lib/design-cloud" {
		t.Fatalf("omitted state_dir must inherit FHS default: got %q", cfg.Runtime.StateRoot)
	}
	if cfg.Runtime.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want %q", cfg.Runtime.BaseDir, dir)
	}
}

func TestResolve_RelativeSecretsJoinBaseDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
state_dir = ""
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
	if cfg.Runtime.StateRoot != dir {
		t.Fatalf("empty state_dir must fall back to BaseDir: got %q", cfg.Runtime.StateRoot)
	}
	if cfg.Log.Dir != filepath.Join(dir, "logs") {
		t.Fatalf("Log.Dir = %q, want under BaseDir", cfg.Log.Dir)
	}
	if got := cfg.ClusterCACertPath(); got != filepath.Join(dir, "certs", "cluster", "ca.crt") {
		t.Fatalf("CA under state_dir(=BaseDir): %q", got)
	}
}

func TestResolve_ExplicitStateDirForCerts(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "state")
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
state_dir = "` + dataDir + `"
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
	if cfg.Runtime.StateRoot != dataDir {
		t.Fatalf("StateRoot = %q, want %q", cfg.Runtime.StateRoot, dataDir)
	}
	if got := cfg.ClusterNodeCertPath("peer"); got != filepath.Join(dataDir, "certs", "cluster", "peer", "peer.crt") {
		t.Fatalf("cert path = %q", got)
	}
	// Relative sqlite still joins BaseDir, not state_dir
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
	// Only cert is set (key 显式清空以覆盖 getDefault 的打包路径): resolve() must not silently supply the missing key.
	content := `
[portal]
port = "8443"
cert = "portal.crt"
key = ""
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

func TestResolveClusterCertPaths(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")

	got, err := ResolveClusterCertPaths(CertInput{NodeID: "cn01", StateDir: dataDir})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	clusterDir := filepath.Join(dataDir, "certs", "cluster")
	certDir := filepath.Join(clusterDir, "cn01")
	if got.StateDir != dataDir || got.CertDir != certDir {
		t.Fatalf("dirs = %q/%q, want %q/%q", got.StateDir, got.CertDir, dataDir, certDir)
	}
	if want := filepath.Join(certDir, "cn01.crt"); got.Cert != want {
		t.Fatalf("Cert = %q, want %q", got.Cert, want)
	}
	if want := filepath.Join(certDir, "cn01.key"); got.Key != want {
		t.Fatalf("Key = %q, want %q", got.Key, want)
	}
	if want := filepath.Join(clusterDir, "ca.crt"); got.CA != want {
		t.Fatalf("CA = %q, want %q", got.CA, want)
	}

	// Pure function: no filesystem side effects.
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("ResolveClusterCertPaths must not create directories (stat err=%v)", err)
	}

	if _, err := ResolveClusterCertPaths(CertInput{StateDir: tmp}); err == nil {
		t.Fatal("expected error when node ID is empty")
	}
}

func TestResolveClusterCertPathsStateDirPriority(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")

	got, err := ResolveClusterCertPaths(CertInput{NodeID: "cn01", StateDir: dataDir})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	if got.StateDir != dataDir {
		t.Fatalf("StateDir override: got %q want %q", got.StateDir, dataDir)
	}

	got, err = ResolveClusterCertPaths(CertInput{NodeID: "cn01"})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	if got.StateDir != defaultPlatformStateDir() {
		t.Fatalf("platform default fallback: got %q want %q", got.StateDir, defaultPlatformStateDir())
	}
}

func TestClusterCertPathLocalCache(t *testing.T) {
	cfg := getDefault()
	cfg.Runtime = RuntimeState{
		NodeID:    "cn01",
		BaseDir:   "/etc/design-cloud",
		StateRoot: "/data",
		CertPath:  "/data/certs/cluster/cn01/cn01.crt",
		KeyPath:   "/data/certs/cluster/cn01/cn01.key",
		CAPath:    "/data/certs/cluster/ca.crt",
	}

	if got := cfg.ClusterNodeCertPath("cn01"); got != cfg.Runtime.CertPath {
		t.Fatalf("local path cache not applied: %q", got)
	}
	if got := cfg.ClusterNodeCertPath("cn02"); got != filepath.Join("/data", "certs", "cluster", "cn02", "cn02.crt") {
		t.Fatalf("local cache leaked to another node: %q", got)
	}
	if got := cfg.ClusterNodeKeyPath("cn02"); got != filepath.Join("/data", "certs", "cluster", "cn02", "cn02.key") {
		t.Fatalf("key cache leaked to another node: %q", got)
	}
	if got := cfg.ClusterCACertPath(); got != cfg.Runtime.CAPath {
		t.Fatalf("CA path cache not applied: %q", got)
	}

	cfg.Runtime.CAPath = ""
	if got := cfg.ClusterCACertPath(); got != filepath.Join("/data", "certs", "cluster", "ca.crt") {
		t.Fatalf("flat CA cert path: %q", got)
	}
	if got := cfg.ClusterCAKeyPath(); got != filepath.Join("/data", "certs", "cluster", "ca", "ca.key") {
		t.Fatalf("nested CA key path: %q", got)
	}
}

func TestDefaultPlatformStateDir(t *testing.T) {
	got := defaultPlatformStateDir()
	if runtime.GOOS == "windows" {
		if got == "" {
			t.Fatal("windows default data dir must not be empty")
		}
		return
	}
	if got != "/var/lib/design-cloud" {
		t.Fatalf("default data dir = %q, want /var/lib/design-cloud", got)
	}
}

func TestResolve_DataRootDefaultUnderStateDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
state_dir = "/var/lib/design-cloud"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/var/lib/design-cloud", DataSubdirName)
	if cfg.Runtime.DataRoot != want {
		t.Fatalf("DataRoot = %q, want %q", cfg.Runtime.DataRoot, want)
	}
	wantDesktop := filepath.Join(want, DesktopSubdirName)
	if got := cfg.DesktopReleaseDir(); got != wantDesktop {
		t.Fatalf("DesktopReleaseDir = %q, want %q", got, wantDesktop)
	}
}

func TestResolve_DataDirRelativeJoinsBaseDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
state_dir = ""
data_dir = "portal-data"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "portal-data")
	if cfg.Runtime.DataRoot != want {
		t.Fatalf("DataRoot = %q, want %q", cfg.Runtime.DataRoot, want)
	}
}
