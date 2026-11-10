package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// resolve is the single entry point for deriving all runtime state.
// It orchestrates: base directory resolution, path resolution,
// node identity (hostname), cluster role discovery, and secret loading.
func (c *Config) resolve(configFilePath string) error {
	// --- 1. Resolve config file's base directory
	absConfigPath, err := filepath.Abs(configFilePath)
	if err != nil {
		return fmt.Errorf("resolve config file path (%s): %w", configFilePath, err)
	}
	baseDir := filepath.Dir(absConfigPath)

	// --- 2. Resolve relative paths (Log.Dir, cert paths, etc.)
	r := func(p *string) {
		if p == nil || *p == "" || filepath.IsAbs(*p) {
			return
		}
		*p = filepath.Join(baseDir, *p)
	}

	r(&c.StateDir)
	r(&c.DataDir)
	r(&c.Log.Dir)
	r(&c.Portal.Cert)
	r(&c.Portal.Key)
	r(&c.Database.Path)
	r(&c.Database.PasswordFile)
	r(&c.User.Ldap.BindPasswordFile)
	r(&c.User.Ldap.TLSCAPath)

	// --- 3. Compute derived Runtime paths from resolved paths
	c.Runtime.BaseDir = baseDir
	c.Runtime.StateRoot = cmp.Or(c.StateDir, baseDir)
	c.Runtime.DataRoot = cmp.Or(c.DataDir, filepath.Join(c.Runtime.StateRoot, DataSubdirName))

	// Fill Portal cert/key defaults only when both are unset. A half-configured
	// pair must stay half-configured so Validate() can reject it.
	if c.Portal.Cert == "" && c.Portal.Key == "" {
		c.Portal.Cert, c.Portal.Key = portalTLSUnder(baseDir)
	}

	// --- 4. Set NodeID to hostname
	nodeID, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}
	c.Runtime.NodeID = nodeID

	// --- 5. Resolve cluster role from topology (patterns validated before caching)
	expanded, err := expandClusterServers(c.Cluster.Servers)
	if err != nil {
		return err
	}
	for _, h := range expanded {
		if !slices.Contains(c.Runtime.Servers, h) {
			c.Runtime.Servers = append(c.Runtime.Servers, h)
		}
	}
	if slices.Contains(expanded, c.Runtime.NodeID) {
		c.Runtime.ServerEnabled = true
	}

	// --- 6. Load secrets for server nodes
	if c.Runtime.ServerEnabled {
		if err := c.loadSecrets(); err != nil {
			return fmt.Errorf("load secrets: %w", err)
		}
	}

	return nil
}

// ==================== Secret Loading ====================

// loadSecrets populates Password and BindPassword from their password files.
// It is the single permission-check point: files are verified immediately before
// being read, and skipped entirely when the value came from the environment.
func (c *Config) loadSecrets() error {
	if c.Database.Password == "" && c.Database.PasswordFile != "" {
		if err := checkSecretFilePermissions("database.password_file", c.Database.PasswordFile); err != nil {
			return err
		}
		data, err := os.ReadFile(c.Database.PasswordFile)
		if err != nil {
			return fmt.Errorf("database password file: %w", err)
		}
		c.Database.Password = strings.TrimSpace(string(data))
	}

	if c.User.Ldap.BindPassword == "" && c.User.Ldap.BindPasswordFile != "" {
		if err := checkSecretFilePermissions("ldap.bind_password_file", c.User.Ldap.BindPasswordFile); err != nil {
			return err
		}
		pwd, err := os.ReadFile(c.User.Ldap.BindPasswordFile)
		if err != nil {
			return fmt.Errorf("ldap bind password file: %w", err)
		}
		c.User.Ldap.BindPassword = strings.TrimSpace(string(pwd))
	}

	return nil
}

// ==================== Log Paths ====================

func (c *Config) AuditLogPath() string {
	return filepath.Join(c.Log.Dir, fmt.Sprintf("audit.%s.log", c.Runtime.NodeID))
}

func (c *Config) SecurityLogPath() string {
	return filepath.Join(c.Log.Dir, fmt.Sprintf("security.%s.log", c.Runtime.NodeID))
}

// ==================== Cert / Data Path Layout ====================
//
// Single formula for Portal TLS under BaseDir and cluster mTLS under StateRoot.
// ResolveClusterCertPaths is the join/CLI entry; Cluster*Path methods are the
// Config accessors (with optional join-mode Runtime path cache).

// CertInput carries the path discovery inputs (CLI --state-dir; no file overrides).
type CertInput struct {
	StateDir string // persistent state root (--state-dir)
	NodeID   string // node hostname (strictly os.Hostname())
}

// ResolvedCertPaths holds the final resolved physical file paths.
type ResolvedCertPaths struct {
	StateDir string // persistent state root
	CertDir  string // node certificate directory (<dataDir>/certs/cluster/<nodeID>)
	Cert     string // absolute path of the node certificate file
	Key      string // absolute path of the node private key file
	CA       string // absolute path of the CA root certificate file
}

// defaultPlatformStateDir returns the deterministic platform default state root.
// Windows: %PROGRAMDATA%\DesignCloudEngine (falls back to C:\ProgramData when unset);
// other platforms: /var/lib/design-cloud.
func defaultPlatformStateDir() string {
	if runtime.GOOS == "windows" {
		base := cmp.Or(os.Getenv("PROGRAMDATA"), `C:\ProgramData`)
		return filepath.Join(base, "DesignCloudEngine")
	}
	return "/var/lib/design-cloud"
}

func portalTLSUnder(baseDir string) (cert, key string) {
	dir := filepath.Join(baseDir, "certs", "portal")
	return filepath.Join(dir, "portal.crt"), filepath.Join(dir, "portal.key")
}

// clusterCertLayout is the sole formula for <dataDir>/certs/cluster/...
func clusterCertLayout(stateDir, nodeID string) ResolvedCertPaths {
	clusterDir := filepath.Join(stateDir, "certs", "cluster")
	certDir := filepath.Join(clusterDir, nodeID)
	return ResolvedCertPaths{
		StateDir: stateDir,
		CertDir:  certDir,
		Cert:     filepath.Join(certDir, fmt.Sprintf("%s.crt", nodeID)),
		Key:      filepath.Join(certDir, fmt.Sprintf("%s.key", nodeID)),
		CA:       filepath.Join(clusterDir, "ca.crt"),
	}
}

func clusterCAKeyPath(stateDir string) string {
	return filepath.Join(stateDir, "certs", "cluster", "ca", "ca.key")
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

	stateDir := cmp.Or(in.StateDir, defaultPlatformStateDir())
	if abs, err := filepath.Abs(stateDir); err == nil {
		stateDir = abs
	}
	return clusterCertLayout(stateDir, in.NodeID), nil
}

// ClusterCACertPath returns the shared cluster CA public key path:
// <StateRoot>/certs/cluster/ca.crt (flat, traversable by every node).
func (c *Config) ClusterCACertPath() string {
	if c.Runtime.CAPath != "" {
		return c.Runtime.CAPath
	}
	stateRoot := cmp.Or(c.Runtime.StateRoot, defaultPlatformStateDir())
	return clusterCertLayout(stateRoot, c.Runtime.NodeID).CA
}

// ClusterCAKeyPath returns the path to the cluster CA key:
// <StateRoot>/certs/cluster/ca/ca.key.
func (c *Config) ClusterCAKeyPath() string {
	return clusterCAKeyPath(c.Runtime.StateRoot)
}

// ClusterNodeCertPath returns the certificate path for a specific node:
// <StateRoot>/certs/cluster/<nodeID>/<nodeID>.crt.
// When Runtime.CertPath is set (join-mode path cache for the local node), it is
// used only for the local NodeID — never for peer node IDs the server signs for.
func (c *Config) ClusterNodeCertPath(nodeID string) string {
	if nodeID == c.Runtime.NodeID && c.Runtime.CertPath != "" {
		return c.Runtime.CertPath
	}
	return clusterCertLayout(c.Runtime.StateRoot, nodeID).Cert
}

// ClusterNodeKeyPath returns the key path for a specific node:
// <StateRoot>/certs/cluster/<nodeID>/<nodeID>.key.
func (c *Config) ClusterNodeKeyPath(nodeID string) string {
	if nodeID == c.Runtime.NodeID && c.Runtime.KeyPath != "" {
		return c.Runtime.KeyPath
	}
	return clusterCertLayout(c.Runtime.StateRoot, nodeID).Key
}

// ==================== Data / State Paths ====================

// DataSubdirName is the default directory under StateRoot for Portal-managed blobs when data_dir is omitted.
const DataSubdirName = "data"

// DesktopSubdirName is the desktop client release subtree under the resolved data_dir root.
const DesktopSubdirName = "desktop"

// DesktopReleaseDir returns the absolute path for Velopack releases and installers.
func (c *Config) DesktopReleaseDir() string {
	return filepath.Join(c.ResolvedDataRoot(), DesktopSubdirName)
}

// DesktopPluginsDir returns the sidecar plugin publish root (<desktop>/plugins).
func (c *Config) DesktopPluginsDir() string {
	return filepath.Join(c.DesktopReleaseDir(), "plugins")
}

// ResolvedDataRoot returns the resolved Portal data root (always set after resolve).
func (c *Config) ResolvedDataRoot() string {
	if c.Runtime.DataRoot != "" {
		return c.Runtime.DataRoot
	}
	return filepath.Join(cmp.Or(c.Runtime.StateRoot, defaultPlatformStateDir()), DataSubdirName)
}

// ResolvedStateRoot returns the resolved persistent state root.
func (c *Config) ResolvedStateRoot() string {
	return cmp.Or(c.Runtime.StateRoot, defaultPlatformStateDir())
}
