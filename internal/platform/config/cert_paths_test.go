package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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

	// Pure function: no filesystem side effects.
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("ResolveClusterCertPaths must not create directories (stat err=%v)", err)
	}

	if _, err := ResolveClusterCertPaths(CertInput{DataDir: tmp}); err == nil {
		t.Fatal("expected error when node ID is empty")
	}
}

func TestResolveClusterCertPathsDataDirPriority(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")

	got, err := ResolveClusterCertPaths(CertInput{NodeID: "cn01", DataDir: dataDir})
	if err != nil {
		t.Fatalf("ResolveClusterCertPaths: %v", err)
	}
	if got.DataDir != dataDir {
		t.Fatalf("DataDir override: got %q want %q", got.DataDir, dataDir)
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
