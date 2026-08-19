package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"design-cloud-engine/internal/common/host"
)

func (c *Config) Validate() error {
	// File-mode topology lives in Cluster.Servers; join-mode targets live in Runtime.Servers.
	if len(c.Cluster.Servers) == 0 && len(c.Runtime.Servers) == 0 {
		return fmt.Errorf("cluster.servers: at least one cluster server is required")
	}

	// Common validation (required for all roles)
	if err := c.Log.Validate(); err != nil {
		return fmt.Errorf("log: %w", err)
	}
	if err := c.Cluster.Validate(); err != nil {
		return fmt.Errorf("cluster: %w", err)
	}

	// Server-specific validation
	if c.Runtime.ServerEnabled {
		if err := c.Portal.Validate(); err != nil {
			return fmt.Errorf("portal: %w", err)
		}
		if err := c.Database.Validate(); err != nil {
			return fmt.Errorf("database: %w", err)
		}
		if err := c.User.Validate(); err != nil {
			return fmt.Errorf("user: %w", err)
		}
		// Scheduler 驱动由 server 装配消费（service/scheduler.NewService 经 dispatcher
		// 在节点上执行作业），因此随 ServerEnabled 校验；纯 agent 节点不消费该配置。
		if err := c.Scheduler.Validate(); err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
	}

	return nil
}

func (a *Portal) Validate() error {
	if a.Port == "" {
		return fmt.Errorf("port is required")
	}

	if a.ReadHeaderTimeout < 5 || a.ReadHeaderTimeout > 15 {
		return fmt.Errorf("read_header_timeout must be in range 5-15 seconds")
	}

	if a.ReadTimeout < 0 || (a.ReadTimeout > 0 && a.ReadTimeout < 30) || a.ReadTimeout > 300 {
		return fmt.Errorf("read_timeout must be 0 (disabled) or in range 30-300 seconds")
	}

	if a.IdleTimeout < 10 || a.IdleTimeout > 180 {
		return fmt.Errorf("idle_timeout must be in range 10-180 seconds")
	}

	if a.RateLimit < 1 || a.RateLimit > 10000 {
		return fmt.Errorf("rate_limit must be in range 1-10000 req/s")
	}

	if a.RateBurst < int(a.RateLimit) || a.RateBurst > 20000 {
		return fmt.Errorf("rate_burst must be >= rate_limit and <= 20000")
	}

	if a.TransferRateLimit < 1 || a.TransferRateLimit > 500 {
		return fmt.Errorf("transfer_rate_limit must be in range 1-500 req/s")
	}

	if a.TransferRateBurst < int(a.TransferRateLimit) || a.TransferRateBurst > 1000 {
		return fmt.Errorf("transfer_rate_burst must be >= transfer_rate_limit and <= 1000")
	}

	if a.WSRateLimit < 1 || a.WSRateLimit > 500 {
		return fmt.Errorf("ws_rate_limit must be in range 1-500 req/s")
	}

	if a.WSRateBurst < int(a.WSRateLimit) || a.WSRateBurst > 1000 {
		return fmt.Errorf("ws_rate_burst must be >= ws_rate_limit and <= 1000")
	}

	// Check cert/key consistency: both must be set or both empty (even when TLS
	// is off, mismatched configuration is a configuration smell we should reject).
	if (a.Cert == "") != (a.Key == "") {
		return fmt.Errorf("cert and key must both be set or both be empty")
	}

	if !a.TLS {
		return nil
	}
	return nil
}

func (c *Cluster) Validate() error {
	for i, srv := range c.Servers {
		if strings.TrimSpace(srv) == "" {
			return fmt.Errorf("servers[%d] cannot be empty", i)
		}
		expanded, err := host.ExpandHostPattern(srv)
		if err != nil {
			return fmt.Errorf("servers[%d] invalid pattern %q: %w", i, srv, err)
		}
		if len(expanded) == 0 {
			return fmt.Errorf("servers[%d] pattern %q expanded to no hosts", i, srv)
		}
		for _, h := range expanded {
			if err := host.ValidateHost(h); err != nil {
				return fmt.Errorf("servers[%d] expanded host %q invalid: %w", i, h, err)
			}
		}
	}

	// Port range
	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("cluster.port must be in range 1024–65535; got %d", c.Port)
	}

	// Heartbeat interval: 10–300s
	if c.HeartbeatInterval < 10 || c.HeartbeatInterval > 300 {
		return fmt.Errorf("cluster.heartbeat_interval must be 10–300 (seconds); got %d", c.HeartbeatInterval)
	}

	// Connect timeout: 3–60s
	if c.ConnectTimeout < 3 || c.ConnectTimeout > 60 {
		return fmt.Errorf("cluster.connect_timeout must be 3–60 (seconds); got %d", c.ConnectTimeout)
	}

	// Node heartbeat timeout: 30–600s, must be >= heartbeat_interval
	if c.NodeHeartbeatTimeout < 30 || c.NodeHeartbeatTimeout > 600 {
		return fmt.Errorf("cluster.node_heartbeat_timeout must be 30–600 (seconds); got %d", c.NodeHeartbeatTimeout)
	}
	if c.NodeHeartbeatTimeout < c.HeartbeatInterval {
		return fmt.Errorf("cluster.node_heartbeat_timeout (%d) must be >= heartbeat_interval (%d)", c.NodeHeartbeatTimeout, c.HeartbeatInterval)
	}

	// Node check interval: 10–300s
	if c.NodeCheckInterval < 10 || c.NodeCheckInterval > 300 {
		return fmt.Errorf("cluster.node_check_interval must be 10–300 (seconds); got %d", c.NodeCheckInterval)
	}

	return nil
}

func (d *Database) Validate() error {
	if d.Type == "" {
		return fmt.Errorf("type is required")
	}
	if d.Type == DatabaseTypeSQLite && d.Path == "" {
		return fmt.Errorf("path is required for sqlite")
	}
	if d.PasswordFile != "" {
		if err := checkSecretFilePermissions("database.password_file", d.PasswordFile); err != nil {
			return err
		}
	}
	if d.MaxOpenConns <= 0 {
		return fmt.Errorf("database.max_open_conns must be > 0 to prevent unlimited connections")
	}
	if d.MaxOpenConns > 500 {
		return fmt.Errorf("database.max_open_conns must be <= 500")
	}
	if d.ConnMaxLifetime != 0 && d.ConnMaxLifetime < 600 {
		return fmt.Errorf("database.conn_max_lifetime must be >= 600 (10 minutes) or 0 (unlimited)")
	}
	if d.ConnMaxLifetime > 86400 {
		return fmt.Errorf("database.conn_max_lifetime must be <= 86400 (24 hours)")
	}
	if d.MaxIdleConns > d.MaxOpenConns {
		return fmt.Errorf("database.max_idle_conns cannot be greater than database.max_open_conns")
	}

	// ssl_mode 枚举校验（仅 Postgres）
	if d.SSLMode != "" {
		validModes := map[string]bool{"disable": true, "allow": true, "prefer": true, "require": true, "verify-ca": true, "verify-full": true}
		if !validModes[d.SSLMode] {
			return fmt.Errorf("database.ssl_mode: invalid value %q, must be one of disable/allow/prefer/require/verify-ca/verify-full", d.SSLMode)
		}
		if d.SSLMode == "disable" {
			slog.Warn("DATABASE SSL/TLS IS DISABLED — all data between server and database travels in plaintext. Set ssl_mode to 'require' (or 'verify-full' with ssl_ca_file) in production.")
		}
	}

	return nil
}

func (u *User) Validate() error {
	if u.AuthMode != UserAuthModeLDAP {
		return nil
	}

	ldapServer := u.Ldap.Server
	if ldapServer == "" {
		return fmt.Errorf("ldap.server is required when auth_mode=ldap")
	}

	// Validate server URL format (must start with ldap:// or ldaps://)
	ldapURL := strings.ToLower(ldapServer)
	if !strings.HasPrefix(ldapURL, "ldap://") && !strings.HasPrefix(ldapURL, "ldaps://") {
		return fmt.Errorf("ldap.server must be a valid ldap:// or ldaps:// URL; got %q", ldapServer)
	}

	if u.Ldap.UserBaseDN == "" {
		return fmt.Errorf("ldap.user_base_dn is required when auth_mode=ldap")
	}
	if idAttr := u.Ldap.IdAttribute; idAttr != "" && strings.ContainsAny(idAttr, "=()&|!*\\") {
		return fmt.Errorf("ldap.id_attribute must be a plain attribute name (e.g. uid), not a filter expression")
	}

	// Validate bind credentials
	if (u.Ldap.BindPassword != "" || u.Ldap.BindPasswordFile != "") && u.Ldap.BindDN == "" {
		return fmt.Errorf("ldap.bind_dn is required when bind_password or bind_password_file is set")
	}

	// Validate password file permissions if set
	if u.Ldap.BindPasswordFile != "" {
		if err := checkSecretFilePermissions("ldap.bind_password_file", u.Ldap.BindPasswordFile); err != nil {
			return err
		}
	}

	// Validate TLS CA path if set
	if u.Ldap.TLSCAPath != "" {
		if _, err := os.Stat(u.Ldap.TLSCAPath); err != nil {
			return fmt.Errorf("ldap.tls_ca_path: %w", err)
		}
	}

	return nil
}

func (l *Log) Validate() error {
	if l.MaxAge <= 0 {
		return fmt.Errorf("log.max_age must be > 0 to prevent infinite log retention")
	}
	if l.MaxSize <= 0 {
		return fmt.Errorf("log.max_size must be > 0 to prevent unconstrained log file growth")
	}
	if l.MaxBackups <= 0 {
		return fmt.Errorf("log.max_backups must be > 0 to prevent infinite log backups")
	}
	return nil
}

// Validate checks Scheduler configuration for agent nodes.
func (j *Scheduler) Validate() error {
	if j.Type != JobSchedulerLSF && j.Type != JobSchedulerSlurm && j.Type != JobSchedulerOpenLava {
		return fmt.Errorf("scheduler.type must be one of: lsf, slurm, openlava; got %q", j.Type)
	}
	if j.EnvScript == "" {
		return fmt.Errorf("scheduler.env_script is required")
	}
	if !filepath.IsAbs(j.EnvScript) {
		return fmt.Errorf("scheduler.env_script must be an absolute path; got %q", j.EnvScript)
	}
	return nil
}
