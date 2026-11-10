package settings

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"log/slog"

	"design-cloud-engine/internal/contract/schema"
)

// loadTyped reads a settings domain and validates it with the same constraints
// used by the write path; invalid values fall back to the schema default.
//
// 校验放在这一层（唯一收口），而不是各消费方各自兜底：读到的值一定满足
// 与写入相同的约束（含跨字段不变量），消费方无需再防御。
func loadTyped[T any](ctx context.Context, s *Service, domain schema.Domain) (T, error) {
	val, err := s.cache.Get(ctx, domain, func(ctx context.Context, k schema.Domain) (any, error) {
		var settings T
		var defaultValue T
		if defaultVal, ok := schema.DefaultSettings(k); ok {
			if typedDefault, ok := defaultVal.(T); ok {
				settings, defaultValue = typedDefault, typedDefault
			}
		}
		if err := s.settingsRepo.Load(ctx, k.String(), &settings); err != nil {
			return nil, err
		}
		if err := validateLoaded(k, settings); err != nil {
			slog.Warn("stored settings failed validation, falling back to defaults",
				"domain", k.String(), "error", err)
			return defaultValue, nil
		}
		return settings, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return val.(T), nil
}

// validateLoaded 复用写入路径的校验（含跨字段规则），保证读写同一套约束。
func validateLoaded(domain schema.Domain, settings any) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal %s settings for validation: %w", domain.String(), err)
	}
	if _, err := schema.DecodeForStore(domain, raw); err != nil {
		return fmt.Errorf("%s settings failed validation: %w", domain.String(), err)
	}
	return nil
}

// GetPlatformSettings returns typed platform settings.
func (s *Service) GetPlatformSettings(ctx context.Context) (schema.PlatformSettings, error) {
	return loadTyped[schema.PlatformSettings](ctx, s, schema.DomainPlatform)
}

// IsIndexEnabled returns whether the homepage (/) is enabled.
func (s *Service) IsIndexEnabled(ctx context.Context) bool {
	p, err := s.GetPlatformSettings(ctx)
	if err != nil {
		return true
	}
	return p.Website.IndexEnabled
}

// IsOverviewEnabled returns whether the overview page (/overview) and guest access are enabled.
func (s *Service) IsOverviewEnabled(ctx context.Context) bool {
	p, err := s.GetPlatformSettings(ctx)
	if err != nil {
		return true
	}
	return p.Website.OverviewEnabled
}

// IsAllowUserClusterOverview returns whether ordinary users may access cluster overview APIs.
// Defaults to true when settings cannot be loaded.
func (s *Service) IsAllowUserClusterOverview(ctx context.Context) bool {
	o, err := s.GetOverviewSettings(ctx)
	if err != nil {
		return true
	}
	return o.AllowUserCluster
}

// GetAuthSettings returns typed auth settings.
func (s *Service) GetAuthSettings(ctx context.Context) (schema.AuthSettings, error) {
	return loadTyped[schema.AuthSettings](ctx, s, schema.DomainAuth)
}

// GetConnectionSettings returns typed connection settings.
func (s *Service) GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error) {
	return loadTyped[schema.ConnectionSettings](ctx, s, schema.DomainConnection)
}

// MaxPerUserCap returns the configured upper bound for a resource's max_per_user.
func (s *Service) MaxPerUserCap(ctx context.Context) (int, error) {
	settings, err := s.GetConnectionSettings(ctx)
	if err != nil {
		return 0, err
	}
	return settings.MaxPerUserCap, nil
}

// GetFileSettings returns typed file settings.
func (s *Service) GetFileSettings(ctx context.Context) (schema.FileSettings, error) {
	return loadTyped[schema.FileSettings](ctx, s, schema.DomainFile)
}

// GetOverviewSettings returns typed overview settings.
func (s *Service) GetOverviewSettings(ctx context.Context) (schema.OverviewSettings, error) {
	return loadTyped[schema.OverviewSettings](ctx, s, schema.DomainOverview)
}

// GetStorageSettings returns typed storage settings.
func (s *Service) GetStorageSettings(ctx context.Context) (schema.StorageSettings, error) {
	return loadTyped[schema.StorageSettings](ctx, s, schema.DomainStorage)
}

// GetSchedulerSettings returns typed scheduler settings.
func (s *Service) GetSchedulerSettings(ctx context.Context) (schema.SchedulerSettings, error) {
	return loadTyped[schema.SchedulerSettings](ctx, s, schema.DomainScheduler)
}

// GetLicenseSettings returns typed license settings.
func (s *Service) GetLicenseSettings(ctx context.Context) (schema.LicenseSettings, error) {
	return loadTyped[schema.LicenseSettings](ctx, s, schema.DomainLicense)
}

// GetAgentSettings returns typed agent settings.
func (s *Service) GetAgentSettings(ctx context.Context) (schema.AgentSettings, error) {
	return loadTyped[schema.AgentSettings](ctx, s, schema.DomainAgent)
}

// GetAlertSettings returns typed alert settings.
func (s *Service) GetAlertSettings(ctx context.Context) (schema.AlertSettings, error) {
	return loadTyped[schema.AlertSettings](ctx, s, schema.DomainAlert)
}

// GetDesktopSettings returns typed desktop settings.
func (s *Service) GetDesktopSettings(ctx context.Context) (schema.DesktopSettings, error) {
	return loadTyped[schema.DesktopSettings](ctx, s, schema.DomainDesktop)
}

// GetDesktopSettingsStrict loads desktop settings without falling back to
// defaults when stored values fail validation.
func (s *Service) GetDesktopSettingsStrict(ctx context.Context) (schema.DesktopSettings, error) {
	var settings schema.DesktopSettings
	if defaultVal, ok := schema.DefaultSettings(schema.DomainDesktop); ok {
		if typed, ok := defaultVal.(schema.DesktopSettings); ok {
			settings = typed
		}
	}
	if err := s.settingsRepo.Load(ctx, schema.DomainDesktop.String(), &settings); err != nil {
		return schema.DesktopSettings{}, err
	}
	if err := validateLoaded(schema.DomainDesktop, settings); err != nil {
		return schema.DesktopSettings{}, err
	}
	return settings, nil
}

// GetCertSettings returns typed certificate lifecycle settings.
func (s *Service) GetCertSettings(ctx context.Context) (schema.CertSettings, error) {
	return loadTyped[schema.CertSettings](ctx, s, schema.DomainCert)
}
