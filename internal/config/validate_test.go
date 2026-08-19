package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// absPath 返回当前平台上确实绝对的测试路径。POSIX 字面量（/opt/lsf/env.sh）在
// Windows 上不是绝对路径，会让"合法配置"用例误报；os.TempDir() 在两个平台都绝对。
func absPath(parts ...string) string {
	return filepath.Join(append([]string{os.TempDir()}, parts...)...)
}

// ==================== Cluster.Servers Validate ====================

func TestCluster_Validate_Servers(t *testing.T) {
	tests := []struct {
		name    string
		servers []string
		wantErr string
	}{
		{name: "empty server entry", servers: []string{""}, wantErr: "servers[0] cannot be empty"},
		{name: "whitespace server entry", servers: []string{"   "}, wantErr: "servers[0] cannot be empty"},
		{name: "valid server", servers: []string{"node01"}, wantErr: ""},
		{name: "valid pattern", servers: []string{"ctrl[01-02]"}, wantErr: ""},
		{name: "invalid pattern", servers: []string{"ctrl[05-02]"}, wantErr: "start number is greater than end number"},
		{name: "invalid host with port", servers: []string{"node01:8080"}, wantErr: "must not include a port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Cluster{
				Port:                 DefaultClusterPort,
				HeartbeatInterval:    60,
				ConnectTimeout:       10,
				NodeHeartbeatTimeout: 180,
				NodeCheckInterval:    120,
				Servers:              tt.servers,
			}
			err := c.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// ==================== Portal.Validate ====================

func TestPortal_Validate(t *testing.T) {
	tests := []struct {
		name    string
		portal  Portal
		wantErr string
	}{
		{
			name:    "empty port",
			portal:  Portal{ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "port is required",
		},
		{
			name:    "read_header_timeout too low",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 4, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "read_header_timeout must be in range 5-15 seconds",
		},
		{
			name:    "read_header_timeout too high",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 16, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "read_header_timeout must be in range 5-15 seconds",
		},
		{
			name:    "read_header_timeout at lower bound",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 5, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "",
		},
		{
			name:    "read_header_timeout at upper bound",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 15, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "",
		},
		{
			name:    "idle_timeout too low",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 9, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "idle_timeout must be in range 10-180 seconds",
		},
		{
			name:    "idle_timeout too high",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 181, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "idle_timeout must be in range 10-180 seconds",
		},
		{
			name:    "idle_timeout at lower bound",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 10, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "",
		},
		{
			name:    "idle_timeout at upper bound",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 180, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "",
		},
		{
			name:    "rate_limit too low",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 0, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "rate_limit must be in range 1-10000 req/s",
		},
		{
			name:    "rate_burst too low",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 100, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "rate_burst must be >= rate_limit and <= 20000",
		},
		{
			name:    "TLS cert only but no key",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30, TLS: true, Cert: "/path/to/cert", Key: ""},
			wantErr: "cert and key must both be set or both be empty",
		},
		{
			name:    "TLS key only but no cert",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30, TLS: true, Cert: "", Key: "/path/to/key"},
			wantErr: "cert and key must both be set or both be empty",
		},
		{
			name:    "TLS both empty",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30, TLS: true, Cert: "", Key: ""},
			wantErr: "",
		},
		{
			name:    "TLS both set",
			portal:  Portal{Port: "8080", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30, TLS: true, Cert: "/path/to/cert", Key: "/path/to/key"},
			wantErr: "",
		},
		{
			name:    "TLS disabled still rejects cert/key mismatch",
			portal:  Portal{Port: "443", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30, TLS: false, Cert: "only-cert", Key: ""},
			wantErr: "cert and key must both be set or both be empty",
		},
		{
			name:    "valid minimal",
			portal:  Portal{Port: "443", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.portal.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

// ==================== Cluster.Validate ====================

func TestCluster_Validate(t *testing.T) {
	validCluster := Cluster{
		Servers: []string{"node01"},
		Port:    52255,

		HeartbeatInterval:    60,
		ConnectTimeout:       10,
		NodeHeartbeatTimeout: 180,
		NodeCheckInterval:    120,
	}

	tests := []struct {
		name    string
		cluster Cluster
		wantErr string
	}{
		{
			name:    "servers with empty hosts",
			cluster: Cluster{Servers: []string{""}, Port: 52255, HeartbeatInterval: 60, ConnectTimeout: 10, NodeHeartbeatTimeout: 180, NodeCheckInterval: 120},
			wantErr: "servers[0] cannot be empty",
		},
		{
			name:    "port too low",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.Port = 1023 }),
			wantErr: "cluster.port must be in range 1024",
		},
		{
			name:    "port too high",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.Port = 65536 }),
			wantErr: "cluster.port must be in range 1024",
		},
		{
			name:    "port at lower bound",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.Port = 1024 }),
			wantErr: "",
		},
		{
			name:    "port at upper bound",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.Port = 65535 }),
			wantErr: "",
		},
		{
			name:    "heartbeat_interval too low",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.HeartbeatInterval = 9 }),
			wantErr: "cluster.heartbeat_interval must be 10",
		},
		{
			name:    "heartbeat_interval too high",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.HeartbeatInterval = 301 }),
			wantErr: "cluster.heartbeat_interval must be 10",
		},
		{
			name:    "connect_timeout too low",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.ConnectTimeout = 2 }),
			wantErr: "cluster.connect_timeout must be 3",
		},
		{
			name:    "connect_timeout too high",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.ConnectTimeout = 61 }),
			wantErr: "cluster.connect_timeout must be 3",
		},
		{
			name:    "node_heartbeat_timeout too low",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.NodeHeartbeatTimeout = 29 }),
			wantErr: "cluster.node_heartbeat_timeout must be 30",
		},
		{
			name:    "node_heartbeat_timeout too high",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.NodeHeartbeatTimeout = 601 }),
			wantErr: "cluster.node_heartbeat_timeout must be 30",
		},
		{
			name:    "node_heartbeat_timeout < heartbeat_interval",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.NodeHeartbeatTimeout = 50; c.HeartbeatInterval = 100 }),
			wantErr: "cluster.node_heartbeat_timeout (50) must be >= heartbeat_interval (100)",
		},
		{
			name:    "node_heartbeat_timeout == heartbeat_interval",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.NodeHeartbeatTimeout = 60; c.HeartbeatInterval = 60 }),
			wantErr: "",
		},
		{
			name:    "node_check_interval too low",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.NodeCheckInterval = 9 }),
			wantErr: "cluster.node_check_interval must be 10",
		},
		{
			name:    "node_check_interval too high",
			cluster: patchCluster(validCluster, func(c *Cluster) { c.NodeCheckInterval = 301 }),
			wantErr: "cluster.node_check_interval must be 10",
		},
		{
			name:    "valid cluster",
			cluster: validCluster,
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cluster.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// patchCluster returns a copy of the cluster with modifications applied.
func patchCluster(base Cluster, fn func(*Cluster)) Cluster {
	c := base
	fn(&c)
	return c
}

// ==================== Database.Validate (pure logic, no filesystem) ====================

func TestDatabase_Validate_PureLogic(t *testing.T) {
	tests := []struct {
		name     string
		database Database
		wantErr  string
	}{
		{
			name:     "empty type",
			database: Database{Type: "", MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "type is required",
		},
		{
			name:     "sqlite without path",
			database: Database{Type: DatabaseTypeSQLite, Path: "", MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "path is required for sqlite",
		},
		{
			name:     "sqlite with path",
			database: Database{Type: DatabaseTypeSQLite, Path: "/data/test.db", MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "",
		},
		{
			name:     "postgres without path (ok)",
			database: Database{Type: DatabaseTypePostgres, Path: "", MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "",
		},
		{
			name:     "max_open_conns zero",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 0, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "database.max_open_conns must be > 0",
		},
		{
			name:     "max_open_conns negative",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: -1, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "database.max_open_conns must be > 0",
		},
		{
			name:     "conn_max_lifetime zero (valid)",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 5},
			wantErr:  "",
		},
		{
			name:     "conn_max_lifetime negative",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 10, ConnMaxLifetime: -1, MaxIdleConns: 5},
			wantErr:  "database.conn_max_lifetime must be >= 600 (10 minutes) or 0 (unlimited)",
		},
		{
			name:     "max_idle_conns > max_open_conns",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 11},
			wantErr:  "database.max_idle_conns cannot be greater than database.max_open_conns",
		},
		{
			name:     "max_idle_conns == max_open_conns",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 10, ConnMaxLifetime: 0, MaxIdleConns: 10},
			wantErr:  "",
		},
		{
			name:     "valid database",
			database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 100, ConnMaxLifetime: 0, MaxIdleConns: 10},
			wantErr:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.database.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// ==================== Database.Validate (with password file) ====================

func TestDatabase_Validate_WithPasswordFile(t *testing.T) {
	// Create a temp password file with correct permissions (0600)
	passwordFile := filepath.Join(t.TempDir(), "dbpwd")
	require.NoError(t, os.WriteFile(passwordFile, []byte("secret"), 0600))

	t.Run("valid password file", func(t *testing.T) {
		db := Database{
			Type:            DatabaseTypePostgres,
			PasswordFile:    passwordFile,
			MaxOpenConns:    10,
			ConnMaxLifetime: 0,
			MaxIdleConns:    5,
		}
		assert.NoError(t, db.Validate())
	})

	t.Run("password file not accessible", func(t *testing.T) {
		db := Database{
			Type:            DatabaseTypePostgres,
			PasswordFile:    "/nonexistent/path",
			MaxOpenConns:    10,
			ConnMaxLifetime: 0,
			MaxIdleConns:    5,
		}
		err := db.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database.password_file not accessible")
	})

}

// ==================== User.Validate (pure logic, no filesystem) ====================

func TestUser_Validate_PureLogic(t *testing.T) {
	tests := []struct {
		name    string
		user    User
		wantErr string
	}{
		{
			name:    "local auth mode (no LDAP validation)",
			user:    User{AuthMode: UserAuthModeLocal},
			wantErr: "",
		},
		{
			name:    "ldap empty server",
			user:    User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{Server: "", UserBaseDN: "cn=users,dc=example,dc=com"}},
			wantErr: "ldap.server is required when auth_mode=ldap",
		},
		{
			name:    "ldap server without ldap:// prefix",
			user:    User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{Server: "example.com:389", UserBaseDN: "cn=users,dc=example,dc=com"}},
			wantErr: `ldap.server must be a valid ldap:// or ldaps:// URL`,
		},
		{
			name:    "ldap missing user_base_dn",
			user:    User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{Server: "ldap://ldap.example.com:389", UserBaseDN: ""}},
			wantErr: "ldap.user_base_dn is required when auth_mode=ldap",
		},
		{
			name: "ldap id_attribute with filter chars",
			user: User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{
				Server:      "ldap://ldap.example.com:389",
				UserBaseDN:  "cn=users,dc=example,dc=com",
				IdAttribute: "(uid=admin)",
			}},
			wantErr: "ldap.id_attribute must be a plain attribute name",
		},
		{
			name: "ldap id_attribute valid",
			user: User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{
				Server:      "ldap://ldap.example.com:389",
				UserBaseDN:  "cn=users,dc=example,dc=com",
				IdAttribute: "uid",
			}},
			wantErr: "",
		},
		{
			name: "ldap bind_password without bind_dn",
			user: User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{
				Server:       "ldap://ldap.example.com:389",
				UserBaseDN:   "cn=users,dc=example,dc=com",
				BindPassword: "secret",
				BindDN:       "",
			}},
			wantErr: "ldap.bind_dn is required when bind_password or bind_password_file is set",
		},
		{
			name: "ldap bind_password_file without bind_dn",
			user: User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{
				Server:           "ldap://ldap.example.com:389",
				UserBaseDN:       "cn=users,dc=example,dc=com",
				BindPasswordFile: "/path/to/pwd",
				BindDN:           "",
			}},
			wantErr: "ldap.bind_dn is required when bind_password or bind_password_file is set",
		},
		{
			name: "ldap valid full config",
			user: User{AuthMode: UserAuthModeLDAP, Ldap: LdapConfig{
				Server:       "ldap://ldap.example.com:389",
				UserBaseDN:   "cn=users,dc=example,dc=com",
				BindDN:       "cn=admin,dc=example,dc=com",
				BindPassword: "secret",
			}},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.user.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// ==================== Log.Validate ====================

func TestLog_Validate(t *testing.T) {
	tests := []struct {
		name    string
		log     Log
		wantErr string
	}{
		{name: "max_age zero", log: Log{MaxAge: 0, MaxSize: 10, MaxBackups: 5}, wantErr: "log.max_age must be > 0"},
		{name: "max_age negative", log: Log{MaxAge: -1, MaxSize: 10, MaxBackups: 5}, wantErr: "log.max_age must be > 0"},
		{name: "max_size zero", log: Log{MaxAge: 30, MaxSize: 0, MaxBackups: 5}, wantErr: "log.max_size must be > 0"},
		{name: "max_size negative", log: Log{MaxAge: 30, MaxSize: -1, MaxBackups: 5}, wantErr: "log.max_size must be > 0"},
		{name: "max_backups zero", log: Log{MaxAge: 30, MaxSize: 10, MaxBackups: 0}, wantErr: "log.max_backups must be > 0"},
		{name: "max_backups negative", log: Log{MaxAge: 30, MaxSize: 10, MaxBackups: -1}, wantErr: "log.max_backups must be > 0"},
		{name: "all valid", log: Log{MaxAge: 30, MaxSize: 100, MaxBackups: 10}, wantErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.log.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// ==================== Scheduler.Validate ====================

func TestScheduler_Validate(t *testing.T) {
	tests := []struct {
		name      string
		scheduler Scheduler
		wantErr   string
	}{
		{
			name:      "invalid name",
			scheduler: Scheduler{Type: "invalid", EnvScript: "/opt/lsf/env.sh"},
			wantErr:   `scheduler.type must be one of: lsf, slurm, openlava; got "invalid"`,
		},
		{
			name:      "empty env_script",
			scheduler: Scheduler{Type: JobSchedulerLSF, EnvScript: ""},
			wantErr:   "scheduler.env_script is required",
		},
		{
			name:      "relative env_script path",
			scheduler: Scheduler{Type: JobSchedulerLSF, EnvScript: "scripts/env.sh"},
			wantErr:   `scheduler.env_script must be an absolute path; got "scripts/env.sh"`,
		},
		{
			name:      "valid LSF",
			scheduler: Scheduler{Type: JobSchedulerLSF, EnvScript: absPath("lsf", "env.sh")},
			wantErr:   "",
		},
		{
			name:      "valid slurm",
			scheduler: Scheduler{Type: JobSchedulerSlurm, EnvScript: absPath("slurm", "env.sh")},
			wantErr:   "",
		},
		{
			name:      "valid openlava",
			scheduler: Scheduler{Type: JobSchedulerOpenLava, EnvScript: absPath("openlava", "env.sh")},
			wantErr:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scheduler.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

// ==================== checkSecretFilePermissions ====================

func TestCheckSecretFilePermissions(t *testing.T) {
	t.Run("valid 0600 file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(file, []byte("secret"), 0600))
		err := checkSecretFilePermissions("test_secret", file)
		assert.NoError(t, err)
	})

	t.Run("valid 0700 file (owner rwx, no group/other)", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(file, []byte("secret"), 0700))
		err := checkSecretFilePermissions("test_secret", file)
		assert.NoError(t, err)
	})

	t.Run("non-existent file", func(t *testing.T) {
		err := checkSecretFilePermissions("test_secret", "/nonexistent/secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test_secret not accessible")
	})

	t.Run("directory instead of file", func(t *testing.T) {
		dir := t.TempDir()
		err := checkSecretFilePermissions("test_secret", dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test_secret must be a regular file")
	})

}

// ==================== validateConfigFilePermissions ====================

func TestValidateConfigFilePermissions(t *testing.T) {
	t.Run("valid 0600 file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(file, []byte("key=value"), 0600))
		err := validateConfigFilePermissions(file)
		assert.NoError(t, err)
	})

	t.Run("non-existent file", func(t *testing.T) {
		err := validateConfigFilePermissions("/nonexistent/config.toml")
		require.Error(t, err)
	})

}

// ==================== Config.Validate ====================

func TestConfig_Validate_Common(t *testing.T) {
	// Common validation: Log, Cluster — always checked regardless of role
	base := Config{
		Log: Log{MaxAge: 30, MaxSize: 100, MaxBackups: 10},
		Cluster: Cluster{
			Servers: []string{"node01"},
			Port:    52255,

			HeartbeatInterval:    60,
			ConnectTimeout:       10,
			NodeHeartbeatTimeout: 180,
			NodeCheckInterval:    120,
		},
	}

	t.Run("empty servers failure", func(t *testing.T) {
		cfg := base
		cfg.Cluster.Servers = nil
		cfg.Runtime.Servers = nil
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cluster.servers: at least one cluster server is required")
	})

	t.Run("log validation failure", func(t *testing.T) {
		cfg := base
		cfg.Log.MaxAge = 0
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "log: log.max_age must be > 0")
	})

	t.Run("cluster validation failure", func(t *testing.T) {
		cfg := base
		cfg.Cluster.Port = 0
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cluster:")
	})
}

func TestConfig_Validate_ServerRole(t *testing.T) {
	base := Config{
		Runtime: RuntimeState{ServerEnabled: true},
		Log:     Log{MaxAge: 30, MaxSize: 100, MaxBackups: 10},
		Cluster: Cluster{
			Servers: []string{"node01"},
			Port:    52255,

			HeartbeatInterval:    60,
			ConnectTimeout:       10,
			NodeHeartbeatTimeout: 180,
			NodeCheckInterval:    120,
		},

		Portal:   Portal{Port: "443", ReadHeaderTimeout: 10, IdleTimeout: 120, RateLimit: 200, RateBurst: 600, TransferRateLimit: 10, TransferRateBurst: 30, WSRateLimit: 10, WSRateBurst: 30},
		Database: Database{Type: DatabaseTypePostgres, MaxOpenConns: 100, ConnMaxLifetime: 0, MaxIdleConns: 10},
		User:     User{AuthMode: UserAuthModeLocal},
		// Scheduler 驱动由 server 装配消费（service/scheduler.NewService），随 ServerEnabled 校验。
		Scheduler: Scheduler{Type: JobSchedulerLSF, EnvScript: absPath("lsf", "env.sh")},
	}

	t.Run("all server fields valid", func(t *testing.T) {
		err := base.Validate()
		assert.NoError(t, err)
	})

	t.Run("portal validation failure when server enabled", func(t *testing.T) {
		cfg := base
		cfg.Portal.Port = ""
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "portal:")
	})

	t.Run("portal NOT validated when server disabled", func(t *testing.T) {
		cfg := base
		cfg.Runtime.ServerEnabled = false
		cfg.Portal.Port = ""
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("database validation failure when server enabled", func(t *testing.T) {
		cfg := base
		cfg.Database.Type = ""
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database:")
	})

	t.Run("user validation failure when server enabled", func(t *testing.T) {
		cfg := base
		cfg.User.AuthMode = UserAuthModeLDAP
		cfg.User.Ldap = LdapConfig{} // empty server should fail
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user:")
	})

	t.Run("scheduler validation failure when server enabled", func(t *testing.T) {
		cfg := base
		cfg.Scheduler.Type = "invalid"
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scheduler:")
	})
}

func TestConfig_Validate_AgentRole(t *testing.T) {
	base := Config{
		Runtime: RuntimeState{ServerEnabled: false},
		Log:     Log{MaxAge: 30, MaxSize: 100, MaxBackups: 10},
		Cluster: Cluster{
			Servers: []string{"node01"},
			Port:    52255,

			HeartbeatInterval:    60,
			ConnectTimeout:       10,
			NodeHeartbeatTimeout: 180,
			NodeCheckInterval:    120,
		},

		Scheduler: Scheduler{Type: JobSchedulerLSF, EnvScript: "/opt/lsf/env.sh"},
	}

	t.Run("all agent fields valid", func(t *testing.T) {
		err := base.Validate()
		assert.NoError(t, err)
	})

	t.Run("scheduler NOT validated for agent role (consumer is server)", func(t *testing.T) {
		cfg := base
		cfg.Scheduler.Type = "invalid" // agent 不消费 Scheduler，不应报错
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("server-only components unvalidated for agent role", func(t *testing.T) {
		cfg := base
		// Portal, Database, User, Scheduler all invalid — skipped for pure agent role
		cfg.Portal = Portal{Port: ""}
		cfg.Database = Database{Type: ""}
		cfg.User = User{AuthMode: UserAuthModeLDAP}
		cfg.Scheduler = Scheduler{Type: "invalid", EnvScript: ""}
		err := cfg.Validate()
		assert.NoError(t, err)
	})
}
