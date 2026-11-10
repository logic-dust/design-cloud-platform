package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// ==================== Configless Agent Assembly ====================
//
// This file implements the pure in-memory assembly path used by configless agents:
// no config.toml is read, all inputs come from CLI flags.

// parseServers parses a comma-separated list of cluster server hostnames.
//
// Input must be plain hostnames/IPs (e.g. "mgt01,mgt02" or "2001:db8::1,2001:db8::2");
// the port is always carried by --port, so any host:port form is rejected here.
func parseServers(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	hosts := make([]string, 0, len(parts))

	for _, part := range parts {
		host := strings.TrimSpace(part)
		if host == "" {
			continue
		}

		// Misuse detection: a successful net.SplitHostPort means the port was
		// written into --servers. Covers "mgt01:52255" and "[::1]:52255"; bare
		// IPv6 (multiple colons) fails with "too many colons" and is allowed.
		if _, port, err := net.SplitHostPort(host); err == nil {
			return nil, fmt.Errorf("invalid server %q: port %q must be set via --port, not inside --servers", host, port)
		}
		if strings.HasSuffix(host, ":") {
			return nil, fmt.Errorf("invalid server %q: trailing colon; set the port via --port", host)
		}

		// Normalize bracketed IPv6: buildServerAddresses uses net.JoinHostPort with
		// bare hostnames; keeping brackets would produce "[[::1]]:52255".
		if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
			host = host[1 : len(host)-1]
		}
		// Reject topology patterns: cluster.servers supports host.ExpandHostPattern
		// (e.g. "cn[01-11]"), but --servers is a literal hostname list. Without this
		// guard "mgt[01-02]" would silently build invalid addresses until DNS fails.
		if strings.ContainsAny(host, "[]") {
			return nil, fmt.Errorf("invalid server %q: --servers does not support topology patterns (e.g. cn[01-11]); list every host explicitly", host)
		}
		if host == "" || strings.ContainsAny(host, " \t/") {
			return nil, fmt.Errorf("invalid server %q", part)
		}

		hosts = append(hosts, host)
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no server specified in %q", raw)
	}
	return hosts, nil
}

// NewMinimalAgentConfig assembles a pure agent configuration in memory from the
// cluster server host list, port, certificate discovery inputs and an optional log dir.
// It strictly preserves NodeID == os.Hostname() and never reads a local config.toml.
//
// All defaults are inherited from getDefault() (single source of truth); only
// runtime-specific fields are overridden. Duplicating defaults here would let
// config-file mode and configless mode drift silently.
func NewMinimalAgentConfig(serversRaw string, port int, certIn CertInput, explicitLogDir string) (*Config, error) {
	nodeID, err := os.Hostname()
	if err != nil || strings.TrimSpace(nodeID) == "" {
		return nil, fmt.Errorf("resolve local hostname failed: %w", err)
	}

	servers, err := parseServers(serversRaw)
	if err != nil {
		return nil, err
	}

	// Port range is validated once by Cluster.Validate() below.
	certIn.NodeID = nodeID
	certPaths, err := ResolveClusterCertPaths(certIn)
	if err != nil {
		return nil, err
	}

	// Sole default source: getDefault(). Portal/Database/User defaults
	// are skipped by Validate() when ServerEnabled=false and are never consumed by
	// any agent path. Cluster.Servers stays empty on purpose: join targets live in
	// Runtime.Servers only — do not forge topology identity for Validate.
	cfg := getDefault()
	cfg.Cluster.Port = port
	cfg.Cluster.Servers = nil

	// Log dir: an explicit --log-dir is the only override of getDefault()'s
	// default. Relative overrides are anchored to the process CWD.
	if explicitLogDir != "" {
		logDir := explicitLogDir
		if abs, err := filepath.Abs(logDir); err == nil {
			logDir = abs
		}
		cfg.Log.Dir = logDir
	}
	cfg.Runtime = RuntimeState{
		NodeID:        nodeID,            // strictly os.Hostname(), no case transformation
		ServerEnabled: false,             // role hard-pinned: configless mode never enables Server
		Servers:       servers,           // plain hostname/IP slice (join targets)
		BaseDir:       certPaths.DataDir, // join mode has no config file; BaseDir tracks DataDir for path helpers
		DataDir:       certPaths.DataDir,
		CertPath:      certPaths.Cert,
		KeyPath:       certPaths.Key,
		CAPath:        certPaths.CA,
	}

	// resolve() is deliberately NOT called: the role is pinned, avoiding a compute
	// node being mistaken for a Server when hostname matches the seed list by chance,
	// and avoiding reads of files that do not exist (database password, etc.).
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("minimal agent config validation failed: %w", err)
	}
	return &cfg, nil
}
