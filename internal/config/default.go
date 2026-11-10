package config

import (
	"fmt"
	"log/slog"

	"github.com/pelletier/go-toml/v2"
)

// ==================== Runtime Defaults ====================

// Values that are also consumed outside this package must live here so that
// getDefault(), validation and runtime consumers share a single source of truth.
const (
	defaultLogDirPath = "/var/log/design-cloud"
	// Packaged Portal TLS under ConfigDir（密钥同树）；集群 mTLS 在 state_dir=/var/lib/design-cloud。
	defaultPortalCertPath = "/etc/design-cloud/certs/portal/portal.crt"
	defaultPortalKeyPath  = "/etc/design-cloud/certs/portal/portal.key"

	DefaultLeaseTTLSeconds        = 30
	DefaultRenewIntervalSeconds   = 15
	DefaultExecutionRetentionDays = 7
)

func getDefault() Config {
	return Config{
		// Packaged FHS roots (absolute). Unpackaged collocated deploys may override
		// with relative paths that resolve against BaseDir = dirname(config).
		StateDir: defaultPlatformStateDir(),
		Portal: Portal{
			Host:               "",
			Port:               "443",
			TLS:                false,
			Cert:               defaultPortalCertPath,
			Key:                defaultPortalKeyPath,
			BehindReverseProxy: false, // 默认不信任反向代理（不读取 X-Forwarded-For）
			ReadHeaderTimeout:  8, ReadTimeout: 60, IdleTimeout: 60,
			RateLimit: 200, RateBurst: 600,
			TransferRateLimit: 10, TransferRateBurst: 30,
			WSRateLimit: 10, WSRateBurst: 30,
			WebSocketHost: "", // 为空则不校验 WS Host（防 DNS rebinding 关闭）
		},
		Cluster: Cluster{
			Port:                   DefaultClusterPort,
			HeartbeatInterval:      60,
			ConnectTimeout:         30,
			NodeHeartbeatTimeout:   180, // 3x default heartbeat; agent missed 3 beats → offline
			NodeCheckInterval:      120, // scan for stale nodes every 120s
			TLSServerName:          "",  // 为空则不校验 gRPC 服务端 SNI
			Servers:                []string{"mgt01"},
			LeaseTTL:               DefaultLeaseTTLSeconds,
			RenewInterval:          DefaultRenewIntervalSeconds,
			ExecutionRetentionDays: DefaultExecutionRetentionDays,
		},
		Database: Database{
			Type:            DatabaseTypePostgres,
			Path:            "/var/lib/design-cloud/dce.sqlite.db",
			Host:            "127.0.0.1",
			Port:            "5432",
			User:            "dce",
			Password:        "",
			PasswordFile:    "/etc/design-cloud/dbpwd",
			Name:            "dce",
			SSLMode:         "disable",
			SSLCAFile:       "", // 为空则使用系统信任链
			MaxIdleConns:    10,
			MaxOpenConns:    25,
			ConnMaxLifetime: 0, // 0 = 不限制，信任 Go + PG 默认行为
		},
		Admin: Admin{
			EnableThreeRoles: false, // 默认单一超级管理员模式
		},
		User: User{
			AuthMode: UserAuthModeLDAP,
			Ldap: LdapConfig{
				Server:           "ldaps://cent7.dc.vm:636",
				UserBaseDN:       "cn=users,cn=accounts,dc=dev,dc=vm",
				BindDN:           "uid=ldapbind,cn=sysaccounts,cn=etc,dc=dev,dc=vm", //uid=ldapbind,cn=users,dc=dev,dc=vm",
				BindPassword:     "",
				BindPasswordFile: "/etc/design-cloud/ldapbindpwd",
				IdAttribute:      "uid",
				UserFilter:       "(objectClass=posixAccount)",
				GroupBaseDN:      "cn=groups,dc=dev,dc=vm",
				GroupFilter:      "(objectClass=posixGroup)",
				PasswordModify:   true,
				TLSCAPath:        "/etc/ipa/ca.crt",
			},
		},
		Log: Log{
			Dir:         defaultLogDirPath,
			AppLevel:    slog.LevelInfo,
			AccessLevel: slog.LevelInfo,
		},
	}
}

// ==================== Config File Generation ====================

// ExampleOptions customizes the generated example configuration.
type ExampleOptions struct {
	Servers  []string // Optional override for cluster.servers
	LogLevel string   // Optional override for log.app_level (DEBUG/INFO/WARN/ERROR)
}

// GetExampleContentWithOptions returns an example configuration with optional
// overrides for cluster.servers and log.app_level.
func GetExampleContentWithOptions(opts ExampleOptions) (string, error) {
	d := getDefault()
	if len(opts.Servers) > 0 {
		if _, err := expandClusterServers(opts.Servers); err != nil {
			return "", fmt.Errorf("invalid servers: %w", err)
		}
		d.Cluster.Servers = append([]string(nil), opts.Servers...)
	}
	if opts.LogLevel != "" {
		level, ok := parseLogLevel(opts.LogLevel)
		if !ok {
			return "", fmt.Errorf("invalid log level %q: must be DEBUG, INFO, WARN or ERROR", opts.LogLevel)
		}
		d.Log.AppLevel = level
	}
	bytes, err := toml.Marshal(d)
	if err != nil {
		return "", err
	}
	return "# Auto-generated full configuration\n\n" + string(bytes), nil
}
