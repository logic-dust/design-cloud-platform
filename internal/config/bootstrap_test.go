package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseServers(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "single host", raw: "mgt01", want: []string{"mgt01"}},
		{name: "two hosts", raw: "mgt01,mgt02", want: []string{"mgt01", "mgt02"}},
		{name: "spaces trimmed", raw: " mgt01 , mgt02 ", want: []string{"mgt01", "mgt02"}},
		{name: "bare ipv6", raw: "2001:db8::1,2001:db8::2", want: []string{"2001:db8::1", "2001:db8::2"}},
		{name: "bracketed ipv6 normalized", raw: "[2001:db8::1]", want: []string{"2001:db8::1"}},
		{name: "bracketed loopback normalized", raw: "[::1]", want: []string{"::1"}},
		{name: "port in host", raw: "mgt01:52255", wantErr: true},
		{name: "port in bracketed ipv6", raw: "[::1]:52255", wantErr: true},
		{name: "trailing colon", raw: "mgt01:", wantErr: true},
		{name: "pattern rejected", raw: "cn[01-11]", wantErr: true},
		{name: "pattern rejected mixed", raw: "mgt01,mgt[02-03]", wantErr: true},
		{name: "url rejected", raw: "https://mgt01:8443", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
		{name: "only comma", raw: ",", wantErr: true},
		{name: "only blanks and comma", raw: " , ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServers(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseServers(%q) expected error, got %v", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseServers(%q) unexpected error: %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseServers(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveClusterCertPaths(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")

	got, err := ResolveClusterCertPaths(CertInput{NodeID: "cn01", DataDir: dataDir})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	clusterDir := filepath.Join(dataDir, "certs", "cluster")
	certDir := filepath.Join(clusterDir, "cn01")
	if got.DataDir != dataDir || got.CertDir != certDir {
		t.Fatalf("dirs = %q/%q, want %q/%q", got.DataDir, got.CertDir, dataDir, certDir)
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
	if want := filepath.Join(clusterDir, "ca", "ca.key"); got.CAKey != want {
		t.Fatalf("CAKey = %q, want %q", got.CAKey, want)
	}

	// Pure function: no filesystem side effects.
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("ResolveClusterCertPaths must not create directories (stat err=%v)", err)
	}

	if _, err := ResolveClusterCertPaths(CertInput{DataDir: tmp}); err == nil {
		t.Fatal("expected error when node ID is empty")
	}
}

func TestResolveClusterCertPathsBaseDirPriority(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	baseDir := filepath.Join(tmp, "base")

	got, err := ResolveClusterCertPaths(CertInput{NodeID: "cn01", DataDir: dataDir, BaseDir: baseDir})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	if got.DataDir != dataDir {
		t.Fatalf("DataDir should win: got %q want %q", got.DataDir, dataDir)
	}

	got, err = ResolveClusterCertPaths(CertInput{NodeID: "cn01", BaseDir: baseDir})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	if got.DataDir != baseDir {
		t.Fatalf("BaseDir fallback: got %q want %q", got.DataDir, baseDir)
	}

	got, err = ResolveClusterCertPaths(CertInput{NodeID: "cn01"})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	if got.DataDir != defaultPlatformDataDir() {
		t.Fatalf("platform default fallback: got %q want %q", got.DataDir, defaultPlatformDataDir())
	}
}

func TestClusterCertPathLocalCache(t *testing.T) {
	cfg := getDefault()
	cfg.Runtime = RuntimeState{
		NodeID:   "cn01",
		BaseDir:  "/etc/design-cloud",
		DataDir:  "/data",
		CertPath: "/data/certs/cluster/cn01/cn01.crt",
		KeyPath:  "/data/certs/cluster/cn01/cn01.key",
		CAPath:   "/data/certs/cluster/ca.crt",
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

func TestDefaultPlatformDataDir(t *testing.T) {
	got := defaultPlatformDataDir()
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

func TestNewMinimalAgentConfig(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")

	cfg, err := NewMinimalAgentConfig("mgt01,mgt02", DefaultClusterPort, CertInput{DataDir: dataDir, NodeID: "ignored-overridden"}, "")
	if err != nil {
		t.Fatalf("NewMinimalAgentConfig: %v", err)
	}

	if cfg.Runtime.NodeID != hostname {
		t.Fatalf("NodeID = %q, want os.Hostname() %q", cfg.Runtime.NodeID, hostname)
	}
	if cfg.Runtime.ServerEnabled {
		t.Fatalf("role must be pinned to non-server (agent): %+v", cfg.Runtime)
	}
	if !reflect.DeepEqual(cfg.Runtime.Servers, []string{"mgt01", "mgt02"}) {
		t.Fatalf("Servers = %v", cfg.Runtime.Servers)
	}
	if cfg.Cluster.Port != DefaultClusterPort {
		t.Fatalf("port = %d, want default %d", cfg.Cluster.Port, DefaultClusterPort)
	}
	if len(cfg.Cluster.Servers) != 0 {
		t.Fatalf("Cluster.Servers must stay empty in join mode (got %+v); targets live in Runtime.Servers", cfg.Cluster.Servers)
	}
	wantCert := filepath.Join(dataDir, "certs", "cluster", hostname, hostname+".crt")
	if cfg.Runtime.CertPath != wantCert {
		t.Fatalf("CertPath = %q, want %q", cfg.Runtime.CertPath, wantCert)
	}
	if cfg.Runtime.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", cfg.Runtime.DataDir, dataDir)
	}
	if cfg.Log.Dir != "/var/log/design-cloud" {
		t.Fatalf("Log.Dir = %q, want /var/log/design-cloud", cfg.Log.Dir)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("minimal agent config must validate: %v", err)
	}

	// Defaults must be inherited from getDefault() — no second source of truth.
	def := getDefault()
	if cfg.Cluster.HeartbeatInterval != def.Cluster.HeartbeatInterval ||
		cfg.Cluster.ConnectTimeout != def.Cluster.ConnectTimeout ||
		cfg.Cluster.NodeHeartbeatTimeout != def.Cluster.NodeHeartbeatTimeout ||
		cfg.Cluster.NodeCheckInterval != def.Cluster.NodeCheckInterval {
		t.Fatalf("cluster defaults drifted: got %+v want baseline %+v", cfg.Cluster, def.Cluster)
	}
	if cfg.Log.MaxAge != def.Log.MaxAge || cfg.Log.MaxSize != def.Log.MaxSize || cfg.Log.MaxBackups != def.Log.MaxBackups {
		t.Fatalf("log defaults drifted: got %+v want baseline %+v", cfg.Log, def.Log)
	}
	if cfg.Scheduler.Type != def.Scheduler.Type || cfg.Scheduler.EnvScript != def.Scheduler.EnvScript {
		t.Fatalf("scheduler defaults drifted: got %+v want baseline %+v", cfg.Scheduler, def.Scheduler)
	}
}

func TestNewMinimalAgentConfigExplicitPortAndLogDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	logDir := filepath.Join(t.TempDir(), "logs")

	cfg, err := NewMinimalAgentConfig("mgt01", 60000, CertInput{DataDir: dataDir}, logDir)
	if err != nil {
		t.Fatalf("NewMinimalAgentConfig: %v", err)
	}
	if cfg.Cluster.Port != 60000 {
		t.Fatalf("port = %d, want 60000", cfg.Cluster.Port)
	}
	if cfg.Log.Dir != logDir {
		t.Fatalf("Log.Dir = %q, want %q", cfg.Log.Dir, logDir)
	}

	if _, err := NewMinimalAgentConfig("mgt01", 0, CertInput{DataDir: dataDir}, ""); err == nil {
		t.Fatal("port 0 must be rejected: the default is carried by the --port flag, not by this function")
	}
	if _, err := NewMinimalAgentConfig("mgt01", 80, CertInput{DataDir: dataDir}, ""); err == nil {
		t.Fatal("port below 1024 must be rejected")
	}
	if _, err := NewMinimalAgentConfig("mgt01", 70000, CertInput{DataDir: dataDir}, ""); err == nil {
		t.Fatal("port above 65535 must be rejected")
	}
	if _, err := NewMinimalAgentConfig("", 52255, CertInput{DataDir: dataDir}, ""); err == nil {
		t.Fatal("empty servers must be rejected")
	}
}
