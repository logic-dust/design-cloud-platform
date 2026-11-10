package config

import (
	"log/slog"
)

// VERSION can be set via ldflags: -ldflags "-X 'design-cloud-engine/internal/config.VERSION=1.0.0'"
var VERSION = "dev"
var DATE = "unknown"
var COMMIT = "unknown"

// AppName can be set via ldflags: -ldflags "-X 'design-cloud-engine/internal/config.AppName=Design Cloud Engine'"
var AppName = "Design Cloud Engine"

// Config is the runtime/domain configuration structure.
type Config struct {
	// DataDir is the durable state root (cluster certs, sqlite, etc.).
	// Empty falls back to the config file directory (Runtime.BaseDir) after resolve.
	// Packaged FHS default is /var/lib/design-cloud — not the same as ConfigDir (/etc/design-cloud).
	DataDir string `toml:"data_dir" comment:"持久状态根目录（集群证书等）；空则回退到配置文件所在目录 BaseDir；打包默认 /var/lib/design-cloud"`

	Log      Log      `toml:"log" comment:"--- 日志配置"`
	Portal   Portal   `toml:"portal" comment:"--- Portal Web服务配置"`
	Cluster  Cluster  `toml:"cluster" comment:"--- 集群节点拓扑与 gRPC 通信配置"`
	Database Database `toml:"database" comment:"--- 数据库连接配置"`
	User     User     `toml:"user" comment:"--- 用户认证配置"`
	Admin    Admin    `toml:"admin" comment:"--- 管理员模式配置"`

	// Runtime-only resolved role state after topology discovery.
	Runtime RuntimeState `toml:"-"`
}

// RuntimeState holds state derived while assembling the effective config.
// config-file mode populates it via resolve(); configless mode builds it directly
// in NewMinimalAgentConfig, which intentionally does not call resolve().
type RuntimeState struct {
	ServerEnabled bool     `toml:"-"`
	NodeID        string   `toml:"-"` // Current node hostname
	Servers       []string `toml:"-"` // Expanded cluster.servers (file mode) or literal --servers (join mode)
	BaseDir       string   `toml:"-"` // Relative-path join root = dirname(abs(config file)); equals DataDir in join mode
	DataDir       string   `toml:"-"` // Durable state root for cluster certs; cmp.Or(config data_dir, BaseDir)

	// Join-mode path cache only: set by NewMinimalAgentConfig, never by resolve().
	// Empty values fall back to the standard layout under DataDir.
	CertPath string `toml:"-"`
	KeyPath  string `toml:"-"`
	CAPath   string `toml:"-"`
}

// DatabaseType represents the type of database
type DatabaseType string

const (
	DatabaseTypeSQLite   DatabaseType = "sqlite"
	DatabaseTypePostgres DatabaseType = "postgres"
)

// UserAuthMode represents the authentication method for users
type UserAuthMode string

const (
	UserAuthModeLocal UserAuthMode = "local"
	UserAuthModeLDAP  UserAuthMode = "ldap"
)

// Log contains logging configuration.
// App / access logs always write to stdout (journald / container runtime).
// Dir only holds audit/security files that Portal tails over the admin API;
// rotation of those files is an OS concern (logrotate / journal), not in-process.
type Log struct {
	Dir         string     `toml:"dir" comment:"审计/安全日志目录（相对路径解析为 BaseDir 下路径；打包默认 /var/log/design-cloud）；应用与访问日志走 stdout"`
	AppLevel    slog.Level `toml:"app_level" comment:"应用日志级别：DEBUG/INFO/WARN/ERROR"`
	AccessLevel slog.Level `toml:"access_level" comment:"HTTP 访问日志级别：DEBUG/INFO/WARN/ERROR"`
}

// Portal contains portal HTTP(S) server configuration.
// The portal is exposed over HTTP(S); some endpoints may upgrade to WebSocket.
type Portal struct {
	Host string `toml:"host" comment:"监听地址，空字符串表示监听所有网卡"`
	Port string `toml:"port" comment:"监听端口"`
	TLS  bool   `toml:"tls" comment:"是否启用 HTTPS"`
	Cert string `toml:"cert" comment:"TLS 证书文件路径"`
	Key  string `toml:"key" comment:"TLS 私钥文件路径（cert/key 均未配置时自动补为默认路径）"`

	// BehindReverseProxy 表示 Portal 前面有反向代理（Nginx、HAProxy、云 LB 等）。
	// 启用后，服务器将从 X-Forwarded-For 请求头中提取客户端真实 IP，
	// 而非直接使用 TCP 连接的远端地址。
	//
	// ⚠️  必须同步在反向代理侧配置，否则c.RealIP()将无法正确获取客户端IP。
	//     Nginx:   proxy_set_header X-Forwarded-For $remote_addr;
	//     HAProxy: option forwardfor
	//     AWS ALB: 自动注入，无需额外配置
	//
	BehindReverseProxy bool `toml:"behind_reverse_proxy" comment:"是否位于反向代理之后；启用则从 X-Forwarded-For 提取客户端真实 IP（需反向代理同步配置）"`

	// TrustedProxyCIDR 是可信反向代理出口 IP 的网段（CIDR 记法，如 10.0.0.0/8、192.168.1.10/32）。
	// 仅当 BehindReverseProxy=true 时生效且必填：X-Forwarded-For 链条中属于该网段的
	// IP 才被视为代理跳，客户端真实 IP 取链条中最近一个不可信 IP。
	// ⚠️  网段切勿配宽——网段内任何主机都能伪造 X-Forwarded-For 前置 IP。
	TrustedProxyCIDR string `toml:"trusted_proxy_cidr" comment:"可信反向代理网段（CIDR）；behind_reverse_proxy=true 时必填，仅网段内 IP 被视为代理跳"`

	// ReadHeaderTimeout 默认 8s，范围 5-15s。HTTP 请求头通常毫秒级到达，
	// 此超时用于防护慢速头部攻击（Slow Header Attack / Slowloris）。
	// Go http.Server 默认值 0 表示不限制，此处强制设置最小值以提供基本保护。
	ReadHeaderTimeout int `toml:"read_header_timeout" comment:"请求头读取超时（秒），范围 5-15，防慢速头部攻击"`

	// RateLimit 公共 API（无需登录）IP 级请求速率限制，单位 req/s。
	RateLimit float64 `toml:"rate_limit" comment:"公共 API（无需登录）IP 级请求速率限制（req/s）"`
	// RateBurst 公共 API IP 级最大瞬时突发请求数。
	RateBurst int `toml:"rate_burst" comment:"公共 API IP 级最大瞬时突发请求数"`

	// TransferRateLimit 文件传输控制请求速率限制，单位 req/s。
	TransferRateLimit float64 `toml:"transfer_rate_limit" comment:"文件传输控制请求速率限制（req/s）"`
	// TransferRateBurst 文件传输控制最大突发请求数。
	TransferRateBurst int `toml:"transfer_rate_burst" comment:"文件传输控制最大突发请求数"`

	// WSRateLimit WebSocket 握手请求速率限制，单位 req/s。
	WSRateLimit float64 `toml:"ws_rate_limit" comment:"WebSocket 握手请求速率限制（req/s）"`
	// WSRateBurst WebSocket 握手最大突发请求数。
	WSRateBurst int `toml:"ws_rate_burst" comment:"WebSocket 握手最大突发请求数"`

	// ReadTimeout 默认 60s，一般 API 请求在此时限内应完成请求体读取。
	// 文件上传/下载路由通过 disableReadTimeout 中间件豁免（读取流式传输时禁用截止时间），
	// 见 middleware/route_policy.go 中的 PolicyFileTransfer。
	// 设为 0 可禁用此超时（不推荐，会失去传输层慢速攻击防护）。
	ReadTimeout int `toml:"read_timeout" comment:"请求读取超时（秒），0=禁用（不推荐）"`

	IdleTimeout int `toml:"idle_timeout" comment:"空闲连接超时（秒）"`

	// WebSocketHost 是 WebSocket 升级时用于校验 Host 头（防 DNS rebinding）的域名。
	// 当项目直接对浏览器暴露 WebSocket 时，应设置此值为 Portal 的公网域名。
	// 当前适配的原生 app 客户端不走 Origin 校验，此值可为空。
	WebSocketHost string `toml:"websocket_host" comment:"WebSocket Host 校验域名（防 DNS rebinding），为空不校验"`
}

// Cluster describes the cluster node topology and internal gRPC communication
// parameters between cluster servers and agents.
type Cluster struct {
	Port                   int      `toml:"port" comment:"集群 Server 的 gRPC 端口"`
	HeartbeatInterval      int      `toml:"heartbeat_interval" comment:"心跳发送间隔（秒）"`
	ConnectTimeout         int      `toml:"connect_timeout" comment:"连接超时（秒）"`
	NodeHeartbeatTimeout   int      `toml:"node_heartbeat_timeout" comment:"节点心跳超时（秒），超过则标记离线"`
	NodeCheckInterval      int      `toml:"node_check_interval" comment:"扫描失联节点周期（秒）"`
	TLSServerName          string   `toml:"tls_server_name" comment:"gRPC TLS SNI，为空则不校验服务端名称"`
	Servers                []string `toml:"servers" comment:"集群 Server 列表"`
	LeaseTTL               int      `toml:"lease_ttl" comment:"控制面 Leader 租约 TTL（秒），0 使用默认 30；调小可加快故障接管，代价是 DB 写频率上升"`
	RenewInterval          int      `toml:"renew_interval" comment:"租约续约间隔（秒），0 使用默认 15；必须小于 lease_ttl"`
	ExecutionRetentionDays int      `toml:"execution_retention_days" comment:"终态执行记录保留天数，0 使用默认 7；超期记录由低频清理任务删除"`
}

// DefaultClusterPort is the single source of truth for the cluster gRPC port.
// It is referenced by the config defaults and by the configless bootstrap path.
const DefaultClusterPort = 52255

// Database contains database connection settings
type Database struct {
	Type            DatabaseType `toml:"type" comment:"数据库类型：sqlite / postgres"`
	Path            string       `toml:"path" comment:"SQLite 数据库文件路径（仅 sqlite）"`
	Host            string       `toml:"host" comment:"数据库主机"`
	Port            string       `toml:"port" comment:"数据库端口"`
	User            string       `toml:"user" comment:"数据库用户"`
	Password        string       `toml:"password" comment:"数据库密码（优先从 password_file 读取，此处可留空）"`
	PasswordFile    string       `toml:"password_file" comment:"数据库密码文件路径"`
	Name            string       `toml:"name" comment:"数据库名"`
	SSLMode         string       `toml:"ssl_mode" comment:"PostgreSQL SSL 模式：disable/allow/prefer/require/verify-ca/verify-full"`
	SSLCAFile       string       `toml:"ssl_ca_file" comment:"自定义 CA 证书路径（verify-ca/verify-full 使用），为空则用系统信任链"`
	MaxIdleConns    int          `toml:"max_idle_conns" comment:"连接池最大空闲连接数"`
	MaxOpenConns    int          `toml:"max_open_conns" comment:"连接池最大打开连接数"`
	ConnMaxLifetime int          `toml:"conn_max_lifetime" comment:"连接最大复用时间（秒），0=不限制"`
}

// LdapConfig contains all LDAP connection and search configuration.
type LdapConfig struct {
	// the LDAP server URL start with ldap:// or ldaps://
	// Examples: ldap://ldap.example.com:389, ldaps://ldap.example.com:636
	Server string `toml:"server" comment:"LDAP 服务器地址，ldap:// 或 ldaps:// 开头，如 ldap://ldap.example.com:389"`

	// --- Search & authentication
	UserBaseDN       string `toml:"user_base_dn" comment:"用户搜索基准 DN，如 cn=users,cn=accounts,dc=example,dc=com"`
	BindDN           string `toml:"bind_dn" comment:"服务账号绑定 DN"`
	BindPassword     string `toml:"bind_password" comment:"绑定密码（优先从 bind_password_file 读取）"`
	BindPasswordFile string `toml:"bind_password_file" comment:"绑定密码文件路径"`

	// --- User filter attributes
	IdAttribute string `toml:"id_attribute" comment:"用户唯一标识属性，默认 uid"`
	UserFilter  string `toml:"user_filter" comment:"用户搜索过滤器，默认 (objectClass=posixAccount)"`

	// Group attributes
	GroupBaseDN    string `toml:"group_base_dn" comment:"组搜索基准 DN，默认同 user_base_dn"`
	GroupFilter    string `toml:"group_filter" comment:"组搜索过滤器，默认 (objectClass=posixGroup)"`
	PasswordModify bool   `toml:"password_modify" comment:"是否启用 RFC 3062 LDAP 密码修改扩展操作"`

	// TLS settings (protocol determined by Server URL scheme: ldap:// vs ldaps://).
	// TLSInsecureSkipVerify is intentionally NOT exposed in config — production must
	// always verify the server certificate. Use TLSCAPath for custom CA chains.
	TLSCAPath string `toml:"tls_ca_path" comment:"自定义 CA 证书路径，为空则使用系统信任链"`
}

// User contains user authentication configuration
type User struct {
	AuthMode UserAuthMode `toml:"auth_mode" comment:"认证模式：local / ldap"`
	Ldap     LdapConfig   `toml:"ldap" comment:"LDAP 配置"`
}

type Admin struct {
	EnableThreeRoles bool `toml:"enable_three_roles" comment:"三员管理模式：true=审计/系统/安全管理员，false=单一超级管理员（默认）"`
}
