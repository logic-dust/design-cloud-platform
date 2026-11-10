package config

import (
	"log/slog"

	"github.com/pelletier/go-toml/v2"
)

// ==================== Runtime Defaults ====================

// Values that are also consumed outside this package must live here so that
// getDefault(), validation and runtime consumers share a single source of truth.
const (
	defaultLogDirPath = "/var/log/design-cloud"

	// Effective values when the corresponding cluster config key is 0.
	DefaultLeaseTTLSeconds        = 30
	DefaultRenewIntervalSeconds   = 15
	DefaultExecutionRetentionDays = 7
)

func getDefault() Config {
	return Config{
		// Packaged FHS roots (absolute). Unpackaged collocated deploys may override
		// with relative paths that resolve against BaseDir = dirname(config).
		DataDir: defaultPlatformDataDir(),
		Portal: Portal{
			Host:               "",
			Port:               "443",
			TLS:                false,
			Cert:               "",
			Key:                "",    // 为空时 resolve() 自动补为 <BaseDir>/certs/portal/portal.key
			BehindReverseProxy: false, // 默认不信任反向代理（不读取 X-Forwarded-For）
			ReadHeaderTimeout:  8, ReadTimeout: 60, IdleTimeout: 60,
			RateLimit: 200, RateBurst: 600,
			TransferRateLimit: 10, TransferRateBurst: 30,
			WSRateLimit: 10, WSRateBurst: 30,
			WebSocketHost: "", // 为空则不校验 WS Host（防 DNS rebinding 关闭）
		},
		Cluster: Cluster{
			Port:                 DefaultClusterPort,
			HeartbeatInterval:    60,
			ConnectTimeout:       30,
			NodeHeartbeatTimeout: 180, // 3x default heartbeat; agent missed 3 beats → offline
			NodeCheckInterval:    120, // scan for stale nodes every 120s
			TLSServerName:        "",  // 为空则不校验 gRPC 服务端 SNI
			Servers:              []string{"cent7", "arch"},
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
				Server:           "ldap://cent7.dc.vm:389",
				UserBaseDN:       "cn=users,cn=accounts,dc=dev,dc=vm",
				BindDN:           "uid=ldapbind,cn=users,dc=dev,dc=vm", // uid=ldapbind,cn=sysaccounts,cn=etc,dc=dev,dc=vm
				BindPassword:     "",
				BindPasswordFile: "/etc/design-cloud/ldapbindpwd",
				IdAttribute:      "uid",
				UserFilter:       "(objectClass=posixAccount)",
				GroupBaseDN:      "cn=groups,dc=dev,dc=vm",
				GroupFilter:      "(objectClass=posixGroup)",
				PasswordModify:   true,
				TLSCAPath:        "",
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

// GetExampleContent returns a documented example configuration with default values.
func GetExampleContent() (string, error) {
	d := getDefault()
	bytes, err := toml.Marshal(d)
	if err != nil {
		return "", err
	}
	return "# Auto-generated full configuration\n\n" + string(bytes), nil
}
