package schema

import "design-cloud-engine/internal/contract"

func defaultProxySettings() ProxySettings {
	return ProxySettings{
		Enabled:                    true,
		VncEnabled:                 true,
		RdpEnabled:                 true,
		SshEnabled:                 true,
		MaxProxyConnections:        200, // 0 = unlimited；新装默认有限并发
		MaxProxyConnectionsPerUser: 3,
	}
}

// DefaultProxySettings returns the nested proxy defaults under connection settings.
func DefaultProxySettings() ProxySettings {
	return defaultProxySettings()
}

// DefaultSettings returns the default persisted value for the given runtime settings domain.
func DefaultSettings(domain Domain) (any, bool) {
	switch domain {
	case DomainPlatform:
		return PlatformSettings{
			Name:        "Design Cloud Platform",
			Description: "Design and manage HPC clusters in the cloud",
			Website: WebsiteSettings{
				IndexEnabled:    true,
				OverviewEnabled: true,
			},
			DisabledModules: defaultDisabledModules(),
		}, true
	case DomainAuth:
		return AuthSettings{
			SessionTTL:                24 * 60 * 60,
			SessionCleanupInterval:    600,
			MaxSessionsPerUser:        1,
			MaxUserLoginAttempts:      5,
			UserBanDuration:           300,
			MaxIPLoginAttempts:        30,
			IPBanDuration:             86400,
			BanAllowlist:              []string{},
			UserSyncInterval:          86400,
			IDMappingFoundCacheTTL:    IDMappingFoundCacheTTLDefault,
			IDMappingNotFoundCacheTTL: IDMappingNotFoundCacheTTLDefault,
			LdapWebAdminUrl:           "",
		}, true
	case DomainConnection:
		return ConnectionSettings{
			MaxPerUserCap:                MaxPerUserCapDefault,
			DefaultMaxConnectionsPerNode: 0,
			Proxy:                        defaultProxySettings(),
		}, true
	case DomainFile:
		return FileSettings{
			Enabled:               true,
			DownloadEnabled:       true,
			UploadEnabled:         true,
			RequiresApproval:      false,
			DownloadApprovalTTL:   86400, // 24 hours
			UploadMaxSizeBytes:    UploadMaxSizeBytesDefault,
			TrashMaxItemSizeBytes: TrashMaxItemSizeBytesDefault,
			TrashMaxItemCount:     TrashMaxItemCountDefault,
			ListMaxEntries:        ListMaxEntriesDefault,
			Locations:             []FileLocationSettings{},
		}, true
	case DomainOverview:
		return OverviewSettings{
			NodeStatusRefreshInterval: 300,    // 5 分钟
			MetricsRefreshInterval:    60,     // 1 分钟
			SchedulerRefreshInterval:  600,    // 10 分钟
			StorageRefreshInterval:    1800,   // 30 分钟
			StorageQueryWindow:        604800, // 7 天
			AllowUserCluster:          true,   // 普通用户默认可看集群概览
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
			Type:        JobSchedulerLSF,
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
			ExecBinPath: contract.DefaultExecBinPath,
			VNC: VNCConfig{
				BinDir:       contract.DefaultVNCBinDir,
				ServerArgs:   []string{},
				ScanEnabled:  true,
				ScanInterval: contract.DefaultVNCScanInterval, // 1 分钟 (60s)
				PasswdMode:   "user",
			},
			Monitor: MonitorConfig{
				MetricsEnabled:  true,
				MetricsInterval: contract.DefaultMetricsInterval,
				DiskMounts:      []DiskMountConfig{},
				NvidiaSmiPath:   "",
			},
		}, true
	case DomainAlert:
		return AlertSettings{
			Enabled: true,
			CPU:     CPUAlertConfig{Enabled: false, MaxUsedPercent: 90, CooldownInterval: 300},
			Load:    LoadAlertConfig{Enabled: false, MaxLoadPercent: LoadAlertMaxLoadPercentDefault, CooldownInterval: 300},
			Memory:  MemoryAlertConfig{Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 300},
			Disk:    DiskAlertConfig{Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 300},
			SharedStorage: StorageAlertConfig{
				Enabled: true, MaxUsedPercent: 90, MinFreeSpace: 0, CooldownInterval: 1800,
			},
		}, true
	case DomainDesktop:
		return DesktopSettings{
			ReleasesEnabled:     true,
			PluginsEnabled:      true,
			WindowsEnabled:      true,
			LinuxEnabled:        true,
			MacosEnabled:        true,
			MaxAssets:           5,
			InstallerFileName:   DefaultInstallerFileName,
			MinSupportedVersion: "",
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
