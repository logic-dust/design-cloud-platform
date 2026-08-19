package schema

import "design-cloud-engine/internal/contract"

// DefaultSettings returns the default persisted value for the given runtime settings domain.
func DefaultSettings(domain Domain) (any, bool) {
	switch domain {
	case DomainPlatform:
		return PlatformSettings{
			Name:        "Design Cloud Platform",
			Description: "Design and manage HPC clusters in the cloud",
			Website: WebsiteSettings{
				IndexEnabled:     true,
				DashboardEnabled: true,
			},
			DisabledModules: []string{},
		}, true
	case DomainAuth:
		return AuthSettings{
			SessionTTL:                24 * 60 * 60,
			SessionCleanupInterval:    600,
			MaxLoginSessionsPerUser:   1,
			MaxUserLoginAttempts:      5,
			UserBanDuration:           300,
			MaxIPLoginAttempts:        20,
			IPBanDuration:             86400,
			UserSyncInterval:          86400,
			IDMappingFoundCacheTTL:    IDMappingFoundCacheTTLDefault,
			IDMappingNotFoundCacheTTL: IDMappingNotFoundCacheTTLDefault,
			LdapWebAdminUrl:           "",
		}, true
	case DomainConnection:
		return ConnectionSettings{
			MaxConnectionsPerUser:  2,
			ConnectionListCacheTTL: 60,
		}, true
	case DomainTunnel:
		return TunnelSettings{
			Enabled:           true,
			VncEnabled:        true,
			RdpEnabled:        true,
			SshEnabled:        true,
			MaxTunnels:        0,
			MaxTunnelsPerUser: 3,
			OtpTTL:            1800,
		}, true
	case DomainFile:
		return FileSettings{
			Enabled:             true,
			DownloadEnabled:     true,
			UploadEnabled:       true,
			RequiresApproval:    false,
			DownloadApprovalTTL: 86400, // 24 hours
			UploadMaxSizeBytes:  UploadMaxSizeBytesDefault,
			Locations:           []FileLocationSettings{},
		}, true
	case DomainDashboard:
		return DashboardSettings{
			NodeStatusRefreshInterval: 300,    // 5 分钟
			ResourceRefreshInterval:   60,     // 1 分钟
			SchedulerRefreshInterval:  600,    // 10 分钟
			StorageRefreshInterval:    1800,   // 30 分钟
			StorageQueryWindow:        604800, // 7 天
		}, true
	case DomainStorage:
		return StorageSettings{
			Storages: []StorageConfig{
				{
					Enabled:     false,
					ID:          "share",
					Name:        "share",
					Description: "Auto-generated default shared storage",
					Type:        "nfs",
					MountPoint:  "/share",
					ProbeNodes:  []string{},
				},
			},
			ProbeInterval: StorageProbeIntervalDefault,
		}, true
	case DomainScheduler:
		return SchedulerSettings{
			JobCacheTTL: 60, // 1 分钟
		}, true
	case DomainLicense:
		return LicenseSettings{
			StatusCacheTTL:   LicenseStatusCacheTTLDefault,
			FeaturesCacheTTL: LicenseFeaturesCacheTTLDefault,
			LmutilPath:       "",
		}, true
	case DomainAgent:
		return AgentSettings{
			LauncherPath: contract.DefaultLauncherPath,
			VNC: VNCConfig{
				BinDir:       contract.DefaultVNCBinDir,
				ServerArgs:   []string{},
				ScanEnabled:  true,
				ScanInterval: contract.DefaultVNCScanInterval, // 30 分钟
			},
			Monitor: MonitorConfig{
				ResourceReportEnabled:  true,
				ResourceReportInterval: 60,
				DiskMounts:             []DiskMountConfig{},
				NvidiaSmiPath:          "",
			},
		}, true
	case DomainAlert:
		return AlertSettings{
			Enabled: true,
			CPU:     CPUAlertConfig{Enabled: false, MaxUsedPercent: 90, CooldownInterval: 300},
			Load:    LoadAlertConfig{Enabled: false, MaxLoadPercent: 100, CooldownInterval: 300},
			Memory:  MemoryAlertConfig{Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 300},
			Disk:    DiskAlertConfig{Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 300},
			SharedStorage: StorageAlertConfig{
				Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 1800,
			},
		}, true
	case DomainDesktop:
		return DesktopSettings{
			ReleasesEnabled: true,
			WindowsEnabled:  true,
			LinuxEnabled:    true,
			MacosEnabled:    true,
			MaxAssets:       5,
		}, true
	case DomainCert:
		return CertSettings{
			AutoRenewMode:        CertAutoRenewApply,
			RenewBeforeDays:      30,
			MaxAttempts:          5,
			BackoffScheduleHours: append([]int(nil), DefaultCertBackoffHours...),
		}, true
	default:
		return nil, false
	}
}
