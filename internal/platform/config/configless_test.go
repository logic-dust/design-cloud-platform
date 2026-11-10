package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if cfg.Log.Dir != def.Log.Dir || cfg.Log.AppLevel != def.Log.AppLevel || cfg.Log.AccessLevel != def.Log.AccessLevel {
		t.Fatalf("log defaults drifted: got %+v want baseline %+v", cfg.Log, def.Log)
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
