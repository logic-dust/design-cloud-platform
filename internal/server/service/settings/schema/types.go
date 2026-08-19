package schema

import (
	"path/filepath"
	"slices"
	"strings"

	"design-cloud-engine/internal/contract"
)

// === platform

// PlatformSettings holds platform-level display and brand settings.
type PlatformSettings struct {
	Name            string          `json:"name" validate:"min=1"`
	Description     string          `json:"description"`
	Website         WebsiteSettings `json:"website"`
	DisabledModules []string        `json:"disabledModules,omitempty"`
	SupportUrl      string          `json:"supportUrl,omitempty"`
}

// WebsiteSettings holds embedded web pages (homepage and dashboard) visibility switches.
type WebsiteSettings struct {
	IndexEnabled     bool `json:"indexEnabled"`     // 门户介绍页 (/) 开关，默认 true
	DashboardEnabled bool `json:"dashboardEnabled"` // 态势大屏页 (/dashboard) 开关，默认 true
}

// === connection

const (
	ConnectionListCacheTTLDefault = 60 // 1 分钟（内网轮询节流）
)

// ConnectionSettings holds application and desktop connection control policies.
type ConnectionSettings struct {
	MaxConnectionsPerUser int `json:"maxConnectionsPerUser" validate:"min=0,max=100,allowzero"`

	// ConnectionListCacheTTL 是用户连接列表缓存 TTL（秒）：前端轮询
	// GET /api/connections 时防重复 DB 查询，默认 60，可调 15-1800。
	ConnectionListCacheTTL int `json:"connectionListCacheTTL,omitempty" validate:"min=15,max=1800,allowzero"`
}

// === file

const (
	UploadMaxSizeBytesDefault = 8 * 1024 * 1024 * 1024   // 8 GB
	UploadMaxSizeBytesLimit   = 100 * 1024 * 1024 * 1024 // 100 GB
)

// FileSettings holds file access and upload settings.
type FileSettings struct {
	Enabled             bool                   `json:"enabled"`
	DownloadEnabled     bool                   `json:"downloadEnabled"`
	UploadEnabled       bool                   `json:"uploadEnabled"`
	RequiresApproval    bool                   `json:"requiresApproval"`
	DownloadApprovalTTL int64                  `json:"downloadApprovalTTL" validate:"min=60,max=2592000"`
	UploadMaxSizeBytes  int64                  `json:"uploadMaxSizeBytes" validate:"max=107374182400"`
	Locations           []FileLocationSettings `json:"locations"`
}

// FindLocation finds an extra file location by its configured name.
func (f FileSettings) FindLocation(name string) (FileLocationSettings, bool) {
	for _, loc := range f.Locations {
		if loc.Name == name {
			return loc, true
		}
	}
	return FileLocationSettings{}, false
}

// FileLocationSettings describes an additional file location root.
type FileLocationSettings struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	Root    string `json:"root"`
}

// === tunnel

// TunnelSettings holds remote access tunnel configuration (DomainTunnel).
type TunnelSettings struct {
	Enabled           bool `json:"enabled"`
	VncEnabled        bool `json:"vncEnabled"`
	RdpEnabled        bool `json:"rdpEnabled"`
	SshEnabled        bool `json:"sshEnabled"`
	MaxTunnels        int  `json:"maxTunnels" validate:"min=0,allowzero"`
	MaxTunnelsPerUser int  `json:"maxTunnelsPerUser" validate:"min=0,max=100,allowzero"`
	OtpTTL            int  `json:"otpTTL" validate:"min=30,max=86400"`
}

// === auth

const (
	IDMappingFoundCacheTTLDefault    = 86400 // 24h
	IDMappingNotFoundCacheTTLDefault = 600   // 10min
)

// AuthSettings holds login, session, ban, password policy, and user sync settings.
type AuthSettings struct {
	SessionTTL                int    `json:"sessionTTL" validate:"min=300,max=2592000"`
	SessionCleanupInterval    int    `json:"sessionCleanupInterval" validate:"min=60,max=86400,allowzero"`
	MaxLoginSessionsPerUser   int    `json:"maxLoginSessionsPerUser" validate:"min=0,max=10,allowzero"`
	MaxUserLoginAttempts      int    `json:"maxUserLoginAttempts" validate:"min=1,max=100"`
	UserBanDuration           int    `json:"userBanDuration" validate:"min=60,max=604800"`
	MaxIPLoginAttempts        int    `json:"maxIPLoginAttempts" validate:"min=1,max=1000"`
	IPBanDuration             int    `json:"ipBanDuration" validate:"min=60,max=2592000"`
	UserSyncInterval          int    `json:"userSyncInterval" validate:"min=300,max=604800,allowzero"`
	IDMappingFoundCacheTTL    int    `json:"idMappingFoundCacheTTL,omitempty" validate:"omitempty,min=600,max=604800"`
	IDMappingNotFoundCacheTTL int    `json:"idMappingNotFoundCacheTTL,omitempty" validate:"omitempty,min=300,max=604800"`
	LdapWebAdminUrl           string `json:"ldapWebAdminUrl,omitempty"`
}

// === agent

// AgentSettings holds global default tool paths and subsystem configuration for cluster agents (DomainAgent).
type AgentSettings struct {
	LauncherPath string        `json:"launcherPath"`
	VNC          VNCConfig     `json:"vnc"`
	Monitor      MonitorConfig `json:"monitor"`
}

// VNCConfig holds VNC server and probe configuration.
type VNCConfig struct {
	BinDir       string   `json:"binDir"`
	ServerArgs   []string `json:"serverArgs,omitempty"`                               // 默认 VNC 启动选项（如 ["-wm", "2d"]）
	ScanEnabled  bool     `json:"scanEnabled"`                                        // 是否启用 VNC 周期扫描，默认 true
	ScanInterval int      `json:"scanInterval" validate:"min=60,max=86400,allowzero"` // VNC 进程扫描周期，单位秒，默认 1800；0=不启用周期扫描
}

// ResolvedServerPath returns the absolute path to vncserver based on BinDir.
func (v VNCConfig) ResolvedServerPath() string {
	if v.BinDir != "" {
		return filepath.Join(v.BinDir, "vncserver")
	}
	return ""
}

// ResolvedPasswdPath returns the absolute path to vncpasswd based on BinDir.
func (v VNCConfig) ResolvedPasswdPath() string {
	if v.BinDir != "" {
		return filepath.Join(v.BinDir, "vncpasswd")
	}
	return ""
}

// DiskMountConfig defines an extra mount point for agent monitoring.
type DiskMountConfig struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	OS      string `json:"os,omitempty" validate:"omitempty,enum=linux|windows"`
}

// MonitorConfig holds hardware, mounts, and metric reporting configuration.
type MonitorConfig struct {
	ResourceReportEnabled  bool              `json:"resourceReportEnabled"`
	ResourceReportInterval int               `json:"resourceReportInterval,omitempty" validate:"min=30,max=3600,allowzero"` // 资源上报间隔，单位秒，默认 60 (30-3600)
	DiskMounts             []DiskMountConfig `json:"diskMounts"`
	NvidiaSmiPath          string            `json:"nvidiaSmiPath,omitempty"`
}

// === scheduler

const (
	SchedulerJobCacheTTLDefault = 60 // 1 分钟
)

// SchedulerSettings 控制调度器服务端行为（DomainScheduler）。
type SchedulerSettings struct {
	JobCacheTTL int `json:"jobCacheTTL,omitempty" validate:"min=30,max=3600,allowzero"` // 作业列表缓存 TTL（秒），默认 60
}

// === dashboard

const (
	DashboardNodeStatusRefreshIntervalDefault = 300    // 5 分钟
	DashboardResourceRefreshIntervalDefault   = 60     // 1 分钟
	DashboardSchedulerRefreshIntervalDefault  = 600    // 10 分钟
	DashboardStorageRefreshIntervalDefault    = 1800   // 30 分钟
	DashboardStorageQueryWindowDefault        = 604800 // 7 天
)

// DashboardSettings 控制 dashboard 聚合层的各数据块缓存刷新间隔（秒）。
type DashboardSettings struct {
	NodeStatusRefreshInterval int `json:"nodeStatusRefreshInterval,omitempty" validate:"min=60,max=3600,allowzero"` // 节点状态统计缓存，默认 300
	ResourceRefreshInterval   int `json:"resourceRefreshInterval,omitempty" validate:"min=30,max=3600,allowzero"`   // 资源聚合缓存，默认 60
	SchedulerRefreshInterval  int `json:"schedulerRefreshInterval,omitempty" validate:"min=90,max=3600,allowzero"`  // 调度器缓存，默认 600
	StorageRefreshInterval    int `json:"storageRefreshInterval,omitempty" validate:"min=60,max=3600,allowzero"`    // 存储缓存，默认 1800
	StorageQueryWindow        int `json:"storageQueryWindow,omitempty" validate:"min=3600,max=2592000,allowzero"`   // 存储历史查询窗口，默认 604800 (7天)
}

// === storage

const (
	StorageProbeIntervalDefault = 3600 // 1 小时
)

// StorageSettings holds shared storage monitoring configuration.
type StorageSettings struct {
	Storages      []StorageConfig `json:"storages"`
	ProbeInterval int             `json:"probeInterval,omitempty" validate:"min=180,max=43200,allowzero"` // 共享存储探测间隔（秒）
}

// EffectiveProbeInterval returns the validated probe interval, falling back to StorageProbeIntervalDefault.
func (s StorageSettings) EffectiveProbeInterval() int {
	if s.ProbeInterval >= 180 && s.ProbeInterval <= 43200 {
		return s.ProbeInterval
	}
	return StorageProbeIntervalDefault
}

// HasEnabled reports whether at least one storage is enabled.
func (s StorageSettings) HasEnabled() bool {
	for _, storage := range s.Storages {
		if storage.Enabled {
			return true
		}
	}
	return false
}

// EnabledStorages returns only the enabled storage configurations.
func (s StorageSettings) EnabledStorages() []StorageConfig {
	var enabled []StorageConfig
	for _, storage := range s.Storages {
		if storage.Enabled {
			enabled = append(enabled, storage)
		}
	}
	return enabled
}

// StorageConfig defines a shared storage volume to be monitored by the cluster.
type StorageConfig struct {
	Enabled     bool     `json:"enabled"`                                         // Whether to actively monitor this storage
	ID          string   `json:"id"`                                              // Business unique ID, e.g., "project-nfs"
	Name        string   `json:"name"`                                            // Display name
	Description string   `json:"description"`                                     // Optional description
	Type        string   `json:"type" validate:"enum=nfs|lustre|ceph|gpfs|other"` // nfs / lustre / ceph / gpfs / other
	MountPoint  string   `json:"mountPoint"`                                      // Mount path to run statfs on
	ProbeNodes  []string `json:"probeNodes"`                                      // Candidate node IDs to run the probe
}

// ShouldProbeOnNode reports whether this storage is enabled and assigned to be probed by nodeID.
func (c StorageConfig) ShouldProbeOnNode(nodeID string) bool {
	return c.Enabled && slices.Contains(c.ProbeNodes, nodeID)
}

// === desktop

// DesktopSettings holds desktop client release and auto-update policies (DomainDesktop).
type DesktopSettings struct {
	ReleasesEnabled bool `json:"releasesEnabled"`
	WindowsEnabled  bool `json:"windowsEnabled"`
	LinuxEnabled    bool `json:"linuxEnabled"`
	MacosEnabled    bool `json:"macosEnabled"`
	MaxAssets       int  `json:"maxAssets" validate:"min=1,max=100"`
}

// IsPlatformAllowed reports whether client package releases are enabled for the specified platform.
// Supported platform aliases match canonical channels:
// - "windows", "win", ""     -> WindowsEnabled
// - "linux"                  -> LinuxEnabled
// - "macos", "osx", "darwin" -> MacosEnabled
func (d DesktopSettings) IsPlatformAllowed(platform string) bool {
	if !d.ReleasesEnabled {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "", "win", "windows":
		return d.WindowsEnabled
	case "linux":
		return d.LinuxEnabled
	case "macos", "osx", "darwin":
		return d.MacosEnabled
	default:
		return false
	}
}

// === alert
//
// 统一单位与约定规范：
// - 容量/空间下限（MinFreeSpace）：单位均为 KB（1024 Bytes），0 表示不启用该维度的绝对容量判定。
// - 时间间隔（CooldownInterval）：单位均为秒（seconds）。
// - 资源使用率上限（MaxUsedPercent）：百分比数值（0~100），0 表示不启用该维度的百分比判定。
// - 负载百分比上限（MaxLoadPercent）：单核平均负载百分比（load1 / cores * 100），0 表示不启用负载告警。
// - 前端与展示层自行负责人类可读格式化（如 B/KB/MB/GB/TB 自适应转换、时间转分/时），配置层与存储层保持纯数值。

// AlertSettings holds resource/capacity alert thresholds (DomainAlert).
type AlertSettings struct {
	Enabled       bool               `json:"enabled"` // 总开关
	CPU           CPUAlertConfig     `json:"cpu"`
	Load          LoadAlertConfig    `json:"load"`
	Memory        MemoryAlertConfig  `json:"memory"`
	Disk          DiskAlertConfig    `json:"disk"`
	SharedStorage StorageAlertConfig `json:"sharedStorage"`
}

// CPUAlertConfig CPU 使用率告警。
type CPUAlertConfig struct {
	Enabled          bool    `json:"enabled"`                                             // 默认 false
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100,allowzero"` // >=阈值告警, 0=不告警
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400"`        // 持续告警重推间隔（秒）
}

// LoadAlertConfig 负载告警（相对核数百分比）。
type LoadAlertConfig struct {
	Enabled          bool    `json:"enabled"`                                              // 默认 false
	MaxLoadPercent   float64 `json:"maxLoadPercent"   validate:"min=0,max=9999,allowzero"` // 相对核数百分比 (load1/cores * 100), 0=不告警
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400"`
}

// MemoryAlertConfig 节点内存使用率告警。
type MemoryAlertConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100,allowzero"`
	MinFreeSpace     int64   `json:"minFreeSpace"     validate:"min=0,allowzero"` // <阈值触发，0=不启用
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400"`
}

// DiskAlertConfig 节点磁盘（挂载点）告警。
type DiskAlertConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100,allowzero"` // >=阈值触发
	MinFreeSpace     int64   `json:"minFreeSpace"     validate:"min=0,allowzero"`         // <阈值触发，0=不启用
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400"`
}

// StorageAlertConfig 共享存储容量告警。
type StorageAlertConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100,allowzero"` // >=阈值触发
	MinFreeSpace     int64   `json:"minFreeSpace"     validate:"min=0,allowzero"`         // <阈值触发，0=不启用
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400"`
}

// === cert

const (
	CertAutoRenewApply   = "apply"
	CertAutoRenewObserve = "observe"
	CertAutoRenewOff     = "off"
)

// DefaultCertBackoffHours is the default backoff sequence (1h / 6h / 24h).
var DefaultCertBackoffHours = []int{1, 6, 24}

// CertSettings holds the cluster certificate lifecycle automation policy (DomainCert).
type CertSettings struct {
	AutoRenewMode        string `json:"autoRenewMode" validate:"enum=apply|observe|off"`
	RenewBeforeDays      int    `json:"renewBeforeDays" validate:"min=1,max=365"`
	MaxAttempts          int    `json:"maxAttempts" validate:"min=1,max=100"`
	BackoffScheduleHours []int  `json:"backoffScheduleHours"`
}

// === tunnel protocol helpers

// IsProtocolAllowed reports whether the given protocol is allowed by TunnelSettings.
func (v TunnelSettings) IsProtocolAllowed(protocol contract.Protocol) bool {
	switch protocol {
	case contract.VNCProtocol:
		return v.VncEnabled
	case contract.RDPProtocol:
		return v.RdpEnabled
	case contract.SSHProtocol:
		return v.SshEnabled
	default:
		return false
	}
}

// === license

const (
	LicenseStatusCacheTTLDefault   = 60
	LicenseFeaturesCacheTTLDefault = 60
)

// LicenseSettings 控制许可证服务监控与缓存配置（DomainLicense）。
type LicenseSettings struct {
	StatusCacheTTL   int    `json:"statusCacheTTL,omitempty" validate:"min=30,max=600,allowzero"`
	FeaturesCacheTTL int    `json:"featuresCacheTTL,omitempty" validate:"min=30,max=600,allowzero"`
	LmutilPath       string `json:"lmutilPath,omitempty"`
}
