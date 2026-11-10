package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CertInput carries the path discovery inputs (CLI data-dir; no file overrides).
type CertInput struct {
	DataDir string // data base directory (--data-dir)
	NodeID  string // node hostname (strictly os.Hostname())
}

// ResolvedCertPaths holds the final resolved physical file paths.
type ResolvedCertPaths struct {
	DataDir string // base directory
	CertDir string // node certificate directory (<dataDir>/certs/cluster/<nodeID>)
	Cert    string // absolute path of the node certificate file
	Key     string // absolute path of the node private key file
	CA      string // absolute path of the CA root certificate file
}

// defaultPlatformDataDir returns the deterministic platform default base directory.
// Windows: %PROGRAMDATA%\DesignCloudEngine (falls back to C:\ProgramData when unset);
// other platforms: /var/lib/design-cloud.
func defaultPlatformDataDir() string {
	if runtime.GOOS == "windows" {
		base := cmp.Or(os.Getenv("PROGRAMDATA"), `C:\ProgramData`)
		return filepath.Join(base, "DesignCloudEngine")
	}
	return "/var/lib/design-cloud"
}

// ResolveClusterCertPaths resolves the standard locations of the cluster mTLS materials.
//
// Contract: pure path resolution, no filesystem IO (no MkdirAll/Stat/Chmod).
// Directory creation and permission ownership are the callers' responsibility
// (node register, InitializeCA, IssueNodeCert) per the permission matrix.
// There is no single-file override: layout under baseDir is the only fact source.
func ResolveClusterCertPaths(in CertInput) (ResolvedCertPaths, error) {
	if strings.TrimSpace(in.NodeID) == "" {
		return ResolvedCertPaths{}, fmt.Errorf("node ID is required to resolve cluster cert paths")
	}

	baseDir := cmp.Or(in.DataDir, defaultPlatformDataDir())
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}

	clusterDir := filepath.Join(baseDir, "certs", "cluster")
	certDir := filepath.Join(clusterDir, in.NodeID)

	return ResolvedCertPaths{
		DataDir: baseDir,
		CertDir: certDir,
		Cert:    filepath.Join(certDir, fmt.Sprintf("%s.crt", in.NodeID)),
		Key:     filepath.Join(certDir, fmt.Sprintf("%s.key", in.NodeID)),
		CA:      filepath.Join(clusterDir, "ca.crt"),
	}, nil
}
