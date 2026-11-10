package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// resolve is the single entry point for deriving all runtime state.
// It orchestrates: base directory resolution, path resolution, executable resolution,
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

	r(&c.DataDir)
	r(&c.Log.Dir)
	r(&c.Portal.Cert)
	r(&c.Portal.Key)
	r(&c.Database.Path)
	r(&c.Database.PasswordFile)
	r(&c.User.Ldap.BindPasswordFile)
	r(&c.User.Ldap.TLSCAPath)

	// --- 3. Compute derived Runtime paths from resolved paths
	// BaseDir = relative-path join root (dirname of config). Not FHS ConfigDir.
	// DataDir = durable state root; empty config data_dir falls back to BaseDir (collocated).
	c.Runtime.BaseDir = baseDir
	c.Runtime.DataDir = cmp.Or(c.DataDir, baseDir)

	// Fill Portal cert/key defaults only when both are unset. A half-configured
	// pair must stay half-configured so Validate() can reject it.
	if c.Portal.Cert == "" && c.Portal.Key == "" {
		c.Portal.Cert = filepath.Join(baseDir, "certs", "portal", "portal.crt")
		c.Portal.Key = filepath.Join(baseDir, "certs", "portal", "portal.key")
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

// ==================== Cluster Cert Paths ====================

// ClusterCACertPath returns the shared cluster CA public key path:
// <DataDir>/certs/cluster/ca.crt (flat, traversable by every node).
func (c *Config) ClusterCACertPath() string {
	if c.Runtime.CAPath != "" {
		return c.Runtime.CAPath
	}
	return filepath.Join(c.Runtime.DataDir, "certs", "cluster", "ca.crt")
}

// ClusterCAKeyPath returns the path to the cluster CA key:
// <DataDir>/certs/cluster/ca/ca.key.
func (c *Config) ClusterCAKeyPath() string {
	return filepath.Join(c.Runtime.DataDir, "certs", "cluster", "ca", "ca.key")
}

// ClusterNodeCertPath returns the certificate path for a specific node:
// <DataDir>/certs/cluster/<nodeID>/<nodeID>.crt.
// When Runtime.CertPath is set (join-mode path cache for the local node), it is
// used only for the local NodeID — never for peer node IDs the server signs for.
func (c *Config) ClusterNodeCertPath(nodeID string) string {
	if nodeID == c.Runtime.NodeID && c.Runtime.CertPath != "" {
		return c.Runtime.CertPath
	}
	return filepath.Join(c.Runtime.DataDir, "certs", "cluster", nodeID, fmt.Sprintf("%s.crt", nodeID))
}

// ClusterNodeKeyPath returns the key path for a specific node:
// <DataDir>/certs/cluster/<nodeID>/<nodeID>.key.
func (c *Config) ClusterNodeKeyPath(nodeID string) string {
	if nodeID == c.Runtime.NodeID && c.Runtime.KeyPath != "" {
		return c.Runtime.KeyPath
	}
	return filepath.Join(c.Runtime.DataDir, "certs", "cluster", nodeID, fmt.Sprintf("%s.key", nodeID))
}
