package config

import (
	"log/slog"
	"os"
	"strings"
)

// 理念：
// 只对必要到一些环境变量支持，不要为了全面支持config的配置项而放开全部环境变量覆盖，避免引入过多不必要的复杂性。
// 应当优先通过配置文件进行配置，环境变量覆盖仅用于特定敏感项（如数据库密码）或部署时需要快速覆盖的核心项（如日志级别、端口等）。

// envOverride records a single environment override for the audit log. Sensitive
// values are masked at log time, never in the recorded config itself.
type envOverride struct {
	key       string
	value     string
	sensitive bool
}

// applyEnvOverrides manually maps a restricted set of DCE_* environment variables
// to the configuration structure and returns the overrides for audit logging.
// The whitelist is intentionally fixed and must not be extended casually.
func applyEnvOverrides(c *Config) []envOverride {
	var overrides []envOverride

	// --- Log Level
	if val := os.Getenv("DCE_LOG_APP_LEVEL"); val != "" {
		var level slog.Level
		valid := true
		switch strings.ToUpper(val) {
		case "DEBUG":
			level = slog.LevelDebug
		case "INFO":
			level = slog.LevelInfo
		case "WARN":
			level = slog.LevelWarn
		case "ERROR":
			level = slog.LevelError
		default:
			// Ignore only this value; the remaining whitelist overrides still apply.
			valid = false
		}
		if valid {
			c.Log.AppLevel = level
			overrides = append(overrides, envOverride{key: "log.app_level", value: val})
		}
	}

	// --- Portal Server
	if val := os.Getenv("DCE_PORTAL_PORT"); val != "" {
		c.Portal.Port = val
		overrides = append(overrides, envOverride{key: "portal.port", value: val})
	}

	// --- Database
	if val := os.Getenv("DCE_DATABASE_HOST"); val != "" {
		c.Database.Host = val
		overrides = append(overrides, envOverride{key: "database.host", value: val})
	}
	if val := os.Getenv("DCE_DATABASE_PORT"); val != "" {
		c.Database.Port = val
		overrides = append(overrides, envOverride{key: "database.port", value: val})
	}
	if val := os.Getenv("DCE_DATABASE_USER"); val != "" {
		c.Database.User = val
		overrides = append(overrides, envOverride{key: "database.user", value: val})
	}
	if val := os.Getenv("DCE_DATABASE_NAME"); val != "" {
		c.Database.Name = val
		overrides = append(overrides, envOverride{key: "database.name", value: val})
	}
	if val := os.Getenv("DCE_DATABASE_PASSWORD"); val != "" {
		c.Database.Password = val
		overrides = append(overrides, envOverride{key: "database.password", sensitive: true})
	}
	if val := os.Getenv("DCE_DATABASE_PASSWORD_FILE"); val != "" {
		c.Database.PasswordFile = val
		overrides = append(overrides, envOverride{key: "database.password_file", value: val})
	}

	// --- User / LDAP
	if val := os.Getenv("DCE_USER_LDAP_BIND_PASSWORD"); val != "" {
		c.User.Ldap.BindPassword = val
		overrides = append(overrides, envOverride{key: "user.ldap.bind_password", sensitive: true})
	}
	if val := os.Getenv("DCE_USER_LDAP_BIND_PASSWORD_FILE"); val != "" {
		c.User.Ldap.BindPasswordFile = val
		overrides = append(overrides, envOverride{key: "user.ldap.bind_password_file", value: val})
	}

	return overrides
}

// logEnvOverrides emits the redacted audit trail for environment overrides.
func logEnvOverrides(overrides []envOverride) {
	for _, o := range overrides {
		value := o.value
		if o.sensitive {
			value = "[REDACTED]"
		}
		slog.Info("config value overridden by environment", "key", o.key, "value", value)
	}
}
