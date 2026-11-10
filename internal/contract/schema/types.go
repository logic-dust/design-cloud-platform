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
	Name            string          `json:"name" validate:"minLen=1,maxLen=64"`
	Description     string          `json:"description" validate:"maxLen=256"`
	Website         WebsiteSettings `json:"website"`
	DisabledModules map[string]bool `json:"disabledModules,omitempty"`
	SupportUrl      string          `json:"supportUrl,omitempty" validate:"maxLen=512"`
}

var userPortalModuleIDs = []string{
	"ai",
	"app",
	"connection",
	"file",
	"license",
	"overview",
	"scheduler",
	"ticket",
}

func defaultDisabledModules() map[string]bool {
	modules := make(map[string]bool, len(userPortalModuleIDs))
	for _, module := range userPortalModuleIDs {
		modules[module] = false
	}
	return modules
}

// WebsiteSettings holds embedded web pages (homepage and overview) visibility switches.
type WebsiteSettings struct {
	IndexEnabled    bool `json:"indexEnabled"`    // 门户介绍页 (/) 开关，默认 true
	OverviewEnabled bool `json:"overviewEnabled"` // 态势大屏页 (/overview) 开关，默认 true
}

// === connection

const (
	MaxPerUserCapDefault = 16
)

// ConnectionSettings holds connection inventory policies and nested protocol proxy settings.
type ConnectionSettings struct {
	// MaxPerUserCap 是单条连接规格 max_per_user 的上限。规格保存时必须满足 1 ≤ max_per_user ≤ 该值。
	MaxPerUserCap int `json:"maxPerUserCap" validate:"min=1,max=16"`
	// DefaultMaxConnectionsPerNode 是跨资源的单节点最大连接数全局默认值（0=不限制）。
	DefaultMaxConnectionsPerNode uint          `json:"defaultMaxConnectionsPerNode" validate:"max=1000"`
	Proxy                        ProxySettings `json:"proxy"`
}

// === file

const (
	UploadMaxSizeBytesDefault    = 8 * 1024 * 1024 * 1024  // 8 GB
	TrashMaxItemSizeBytesDefault = 10 * 1024 * 1024 * 1024 // 10 GB
	TrashMaxItemCountDefault     = 5000                    // 默认最多扫描 5000 个条目
	// ListMaxEntriesDefault 是单次目录/回收站列表的设置窗口。
	// PrivilegedExec 在此基础上再读 100 个名字并一并返回。设置取最大 999 时，一次最多返回 1099 个。
	// 读满后目录仍有后续名字时，响应 truncated 为 true。
	ListMaxEntriesDefault = 999
)

// FileSettings holds file access and upload settings.
type FileSettings struct {
	Enabled               bool                   `json:"enabled"`
	DownloadEnabled       bool                   `json:"downloadEnabled"`
	UploadEnabled         bool                   `json:"uploadEnabled"`
	RequiresApproval      bool                   `json:"requiresApproval"`
	DownloadApprovalTTL   int64                  `json:"downloadApprovalTTL" validate:"min=3600,max=2592000" unit:"s"`
	UploadMaxSizeBytes    int64                  `json:"uploadMaxSizeBytes" validate:"max=107374182400" unit:"B"`
	TrashMaxItemSizeBytes int64                  `json:"trashMaxItemSizeBytes" validate:"max=53687091200" unit:"B"`
	TrashMaxItemCount     int                    `json:"trashMaxItemCount" validate:"min=0,max=100000"`
	ListMaxEntries        int                    `json:"listMaxEntries" validate:"min=50,max=999"`
	Locations             []FileLocationSettings `json:"locations"`
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
	Name    string `json:"name" validate:"minLen=1,maxLen=64"`
	Root    string `json:"root" validate:"minLen=1,maxLen=256"`
}

// === proxy (remote access ProxyConnection)

// ProxySettings holds protocol proxy / ProxyConnection configuration (nested under connection).
type ProxySettings struct {
	Enabled    bool `json:"enabled"`
	VncEnabled bool `json:"vncEnabled"`
	RdpEnabled bool `json:"rdpEnabled"`
	SshEnabled bool `json:"sshEnabled"`
	// MaxProxyConnections is the global concurrent active ProxyConnection cap on each Engine node.
	// 0 means unlimited; schema default for new installs is 200.
	MaxProxyConnections        int `json:"maxProxyConnections" validate:"min=0,max=10000"`
	MaxProxyConnectionsPerUser int `json:"maxProxyConnectionsPerUser" validate:"min=0,max=100"`
}

// === auth

const (
	IDMappingFoundCacheTTLDefault    = 86400 // 24h
	IDMappingNotFoundCacheTTLDefault = 600   // 10min
)

// AuthSettings holds login, session, ban, password policy, and user sync settings.
type AuthSettings struct {
	SessionTTL             int `json:"sessionTTL" validate:"min=300,max=2592000" unit:"s"`
	SessionCleanupInterval int `json:"sessionCleanupInterval" validate:"min=60,max=86400" unit:"s"`
	MaxSessionsPerUser     int `json:"maxSessionsPerUser" validate:"min=1,max=5"`
	MaxUserLoginAttempts   int `json:"maxUserLoginAttempts" validate:"min=5,max=10"`
	UserBanDuration        int `json:"userBanDuration" validate:"min=60,max=604800" unit:"s"`
	MaxIPLoginAttempts     int `json:"maxIPLoginAttempts" validate:"min=10,max=100"`
	IPBanDuration          int `json:"ipBanDuration" validate:"min=60,max=2592000" unit:"s"`
	// BanAllowlist 永不参与失败计数与封禁的 IP/CIDR 列表（支持单个 IP 或 CIDR 前缀），
	// 用于保护可信出口（堡垒机、监控探针、反向代理健康检查等）免受误封。
	BanAllowlist              []string `json:"banAllowlist,omitempty" validate:"minLen=1,maxLen=64"`
	UserSyncInterval          int      `json:"userSyncInterval" validate:"min=300,max=604800,allowzero" unit:"s"`
	IDMappingFoundCacheTTL    int      `json:"idMappingFoundCacheTTL" validate:"min=600,max=604800" unit:"s"`
	IDMappingNotFoundCacheTTL int      `json:"idMappingNotFoundCacheTTL" validate:"min=300,max=604800" unit:"s"`
	LdapWebAdminUrl           string   `json:"ldapWebAdminUrl,omitempty" validate:"maxLen=512"`
}

// === agent

// AgentSettings holds global default tool paths and subsystem configuration for cluster agents (DomainAgent).
type AgentSettings struct {
	ExecBinPath string        `json:"execBinPath" validate:"maxLen=256"`
	VNC         VNCConfig     `json:"vnc"`
	Monitor     MonitorConfig `json:"monitor"`
}

// VNCConfig holds VNC server and probe configuration.
type VNCConfig struct {
	BinDir       string   `json:"binDir" validate:"maxLen=256"`
	ServerArgs   []string `json:"serverArgs,omitempty" validate:"maxLen=128"`                  // 默认 VNC 启动选项（如 ["-wm", "2d"]）
	ScanEnabled  bool     `json:"scanEnabled"`                                                 // 是否启用 VNC 周期扫描，默认 true
	ScanInterval int      `json:"scanInterval" validate:"min=30,max=86400" unit:"s"`           // VNC 进程扫描周期，单位秒，默认 60
	UseCgroup    bool     `json:"useCgroup"`                                                   // Linux VNC 是否默认启用 systemd transient slice
	PasswdMode   string   `json:"passwdMode,omitempty" validate:"omitempty,enum=user|managed"` // VNC 密码文件模式：user=沿用 ~/.vnc/passwd；managed=私有路径且启动后删除
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
	Name    string `json:"name" validate:"minLen=1,maxLen=64"`
	Path    string `json:"path" validate:"minLen=1,maxLen=256"`
	OS      string `json:"os,omitempty" validate:"omitempty,enum=linux|windows"`
}

// MonitorConfig holds hardware, mounts, and metric reporting configuration.
type MonitorConfig struct {
	MetricsEnabled  bool              `json:"metricsEnabled"`
	MetricsInterval int               `json:"metricsInterval" validate:"min=30,max=3600" unit:"s"` // 资源指标上报间隔，单位秒，默认 60 (30-3600)
	DiskMounts      []DiskMountConfig `json:"diskMounts"`
	NvidiaSmiPath   string            `json:"nvidiaSmiPath,omitempty" validate:"maxLen=256"`
}

// === scheduler

const (
	SchedulerJobCacheTTLDefault = 60 // 1 分钟
)

// JobScheduler 是底层 HPC 调度器类型。
type JobScheduler string

const (
	JobSchedulerSlurm    JobScheduler = "slurm"
	JobSchedulerOpenLava JobScheduler = "openlava"
	JobSchedulerLSF      JobScheduler = "lsf"
)

// SchedulerSettings 控制调度器行为（DomainScheduler）。
// Type 与 JobCacheTTL 由 Server 消费；EnvScript 由 Agent 在本机执行调度器命令前 source，
// 支持节点稀疏覆盖（NodeSchedulerOverride）。
type SchedulerSettings struct {
	Type        JobScheduler `json:"type" validate:"enum=lsf|slurm|openlava"`         // 调度器类型，默认 lsf
	EnvScript   string       `json:"envScript,omitempty" validate:"maxLen=4096"`      // 调度器环境初始化脚本（绝对路径），为空则直接执行命令
	JobCacheTTL int          `json:"jobCacheTTL" validate:"min=30,max=3600" unit:"s"` // 作业列表缓存 TTL（秒），默认 60
}

// === overview

const (
	OverviewNodeStatusRefreshIntervalDefault = 300    // 5 分钟
	OverviewMetricsRefreshIntervalDefault    = 60     // 1 分钟
	OverviewSchedulerRefreshIntervalDefault  = 600    // 10 分钟
	OverviewStorageRefreshIntervalDefault    = 1800   // 30 分钟
	OverviewStorageQueryWindowDefault        = 604800 // 7 天
)

// OverviewSettings 控制 overview 聚合层的各数据块缓存刷新间隔，以及普通用户能否查看集群概览。
type OverviewSettings struct {
	NodeStatusRefreshInterval int  `json:"nodeStatusRefreshInterval" validate:"min=60,max=3600" unit:"s"` // 节点状态统计缓存，默认 300
	MetricsRefreshInterval    int  `json:"metricsRefreshInterval" validate:"min=30,max=3600" unit:"s"`    // 资源聚合缓存，默认 60
	SchedulerRefreshInterval  int  `json:"schedulerRefreshInterval" validate:"min=90,max=3600" unit:"s"`  // 调度器缓存，默认 600
	StorageRefreshInterval    int  `json:"storageRefreshInterval" validate:"min=60,max=3600" unit:"s"`    // 存储缓存，默认 1800
	StorageQueryWindow        int  `json:"storageQueryWindow" validate:"min=3600,max=2592000" unit:"s"`   // 存储历史查询窗口，默认 604800 (7天)
	AllowUserCluster          bool `json:"allowUserCluster"`                                              // 普通用户是否可访问 /api/overview/cluster*，默认 true
}

// === storage

const (
	StorageProbeIntervalDefault = contract.DefaultStorageProbeInterval // 1 小时
)

// StorageSettings holds shared storage monitoring configuration.
type StorageSettings struct {
	Storages      []StorageConfig `json:"storages"`
	ProbeInterval int             `json:"probeInterval" validate:"min=180,max=43200" unit:"s"` // 共享存储探测间隔（秒）
}

// EffectiveProbeInterval returns the probe interval, falling back to StorageProbeIntervalDefault when 0.
// Non-zero values are assumed already validated (DecodeForStore / loadTyped).
func (s StorageSettings) EffectiveProbeInterval() int {
	if s.ProbeInterval == 0 {
		return StorageProbeIntervalDefault
	}
	return s.ProbeInterval
}

// HasEnabled reports whether at least one storage is enabled.
func (s StorageSettings) HasEnabled() bool {
	return slices.ContainsFunc(s.Storages, func(storage StorageConfig) bool {
		return storage.Enabled
	})
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
	ID          string   `json:"id" validate:"minLen=1,maxLen=64"`                // Business unique ID, e.g., "project-nfs"
	Name        string   `json:"name" validate:"minLen=1,maxLen=64"`              // Display name
	Description string   `json:"description" validate:"maxLen=256"`               // Optional description
	Type        string   `json:"type" validate:"enum=nfs|lustre|ceph|gpfs|other"` // nfs / lustre / ceph / gpfs / other
	MountPoint  string   `json:"mountPoint" validate:"minLen=1,maxLen=256"`       // Mount path to run statfs on
	ProbeNodes  []string `json:"probeNodes" validate:"minLen=1,maxLen=64"`        // Candidate node IDs to run the probe
}

// ShouldProbeOnNode reports whether this storage is enabled and assigned to be probed by nodeID.
func (c StorageConfig) ShouldProbeOnNode(nodeID string) bool {
	return c.Enabled && slices.Contains(c.ProbeNodes, nodeID)
}

// === desktop

// DefaultInstallerFileName is the first-run Windows Setup artifact name used when
// desktop.installerFileName is not configured. The installer is a single artifact
// (no version in the name); publish new releases by replacing this file in place.
const DefaultInstallerFileName = "DesignCloudDesktop-win-Setup.exe"

// DesktopSettings holds desktop client release and auto-update policies (DomainDesktop).
type DesktopSettings struct {
	ReleasesEnabled bool `json:"releasesEnabled"`
	PluginsEnabled  bool `json:"pluginsEnabled"`
	WindowsEnabled  bool `json:"windowsEnabled"`
	LinuxEnabled    bool `json:"linuxEnabled"`
	MacosEnabled    bool `json:"macosEnabled"`
	MaxAssets       int  `json:"maxAssets" validate:"min=1,max=100"`
	// InstallerFileName is the first-run Setup.exe name (plain base name). Empty means
	// DefaultInstallerFileName, which matches Velopack's default output
	// "<PackId>-<channel>-Setup.exe" (e.g. DesignCloudDesktop-win-Setup.exe).
	// Override only if the release pipeline renames/copies the artifact.
	InstallerFileName string `json:"installerFileName,omitempty" validate:"omitempty,maxLen=128"`
	// MinSupportedVersion is the lowest desktop product version allowed
	// to use this engine. Empty means no minimum is configured.
	MinSupportedVersion string `json:"minSupportedVersion" validate:"omitempty,maxLen=32"`
}

// EffectiveInstallerFileName returns the configured installer file name or the default.
func (d DesktopSettings) EffectiveInstallerFileName() string {
	if name := strings.TrimSpace(d.InstallerFileName); name != "" {
		return name
	}
	return DefaultInstallerFileName
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
	return d.isPlatformChannelEnabled(platform)
}

// IsPluginPlatformAllowed reports whether sidecar plugin releases are enabled for the platform.
// Uses pluginsEnabled and per-OS flags; does not require releasesEnabled (Velopack app packages).
func (d DesktopSettings) IsPluginPlatformAllowed(platform string) bool {
	if !d.PluginsEnabled {
		return false
	}
	return d.isPlatformChannelEnabled(platform)
}

func (d DesktopSettings) isPlatformChannelEnabled(platform string) bool {
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
// - 容量/空间下限（MinFreeSpace）：单位均为 KiB（1024 Bytes），0 表示不启用该维度的绝对容量判定。
// - 时间间隔（CooldownInterval）：单位均为秒（s）。
// - 资源使用率上限（MaxUsedPercent）：百分比数值（0~100），0 表示不启用该维度的百分比判定。
// - 负载百分比上限（MaxLoadPercent）：单核平均负载百分比（load1 / cores * 100），50–200；由 load.enabled 开关控制是否评估。
// - 前端与展示层自行负责人类可读格式化（如 B/KiB/MiB/GiB/TiB 自适应转换、时间转分/时），配置层与存储层保持纯数值。

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
	Enabled          bool    `json:"enabled"`                                               // 默认 false
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=1,max=100"`             // >=阈值告警
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400" unit:"s"` // 持续告警重推间隔（秒）
}

const (
	LoadAlertMaxLoadPercentDefault = 110
)

// LoadAlertConfig 负载告警（相对核数百分比）。
type LoadAlertConfig struct {
	Enabled          bool    `json:"enabled"`                                  // 默认 false
	MaxLoadPercent   float64 `json:"maxLoadPercent" validate:"min=50,max=200"` // load1/CPU核数×100；超过阈值告警
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400" unit:"s"`
}

// MemoryAlertConfig 节点内存使用率告警。
type MemoryAlertConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100"`
	MinFreeSpace     int64   `json:"minFreeSpace"     validate:"min=0" unit:"KiB"` // <阈值触发，0=不启用
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400" unit:"s"`
}

// DiskAlertConfig 节点磁盘（挂载点）告警。
type DiskAlertConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100"`    // >=阈值触发
	MinFreeSpace     int64   `json:"minFreeSpace"     validate:"min=0" unit:"KiB"` // <阈值触发，0=不启用
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400" unit:"s"`
}

// StorageAlertConfig 共享存储容量告警。
type StorageAlertConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsedPercent   float64 `json:"maxUsedPercent"   validate:"min=0,max=100"`    // >=阈值触发
	MinFreeSpace     int64   `json:"minFreeSpace"     validate:"min=0" unit:"KiB"` // <阈值触发，0=不启用
	CooldownInterval int     `json:"cooldownInterval" validate:"min=60,max=86400" unit:"s"`
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
	RenewBeforeDays      int    `json:"renewBeforeDays" validate:"min=1,max=365" unit:"d"`
	MaxAttempts          int    `json:"maxAttempts" validate:"min=1,max=100"`
	BackoffScheduleHours []int  `json:"backoffScheduleHours" validate:"min=1,max=168" unit:"h"`
}

// === proxy protocol helpers

// IsProtocolAllowed reports whether the given protocol is allowed by ProxySettings.
func (v ProxySettings) IsProtocolAllowed(protocol contract.Protocol) bool {
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
	LicenseStatusCacheTTLDefault   = 300
	LicenseFeaturesCacheTTLDefault = 300
)

// LicenseSettings 控制许可证服务监控与缓存配置（DomainLicense）。
type LicenseSettings struct {
	StatusCacheTTL   int    `json:"statusCacheTTL" validate:"min=60,max=900" unit:"s"`
	FeaturesCacheTTL int    `json:"featuresCacheTTL" validate:"min=60,max=900" unit:"s"`
	LmutilPath       string `json:"lmutilPath,omitempty" validate:"maxLen=256"`
}
