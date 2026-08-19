// Package security provides authentication failure tracking, IP/user banning,
// and authorization boundary enforcement.
package security

import (
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
)

// Defaults mirror the corresponding settings schema defaults so that callers
// have a zero-configuration fallback before settings are loaded.
var (
	defaultBanSecondsForUser = 300
	defaultBanSecondsForIP   = 86400

	defaultMaxUserFailedAttempts = 5
	defaultMaxIPFailedAttempts   = 20
)

// authSettingsKey is the singleton key used in the authSettingsCache.
// A 60-second TTL eliminates the ≈4 DB round-trips that occur on every
// failed login attempt while still respecting admin-configured thresholds.
const authSettingsKey = "auth"

// BannedRecord is the client-facing representation of an active ban.
type BannedRecord struct {
	Type     model.BanType `json:"type"`
	Username string        `json:"username"`
	IP       string        `json:"ip"`
	BanTime  time.Time     `json:"banTime"`
	Reason   string        `json:"reason"`
}

// Service handles IP and user ban logic, checking thresholds and
// coordinating with BanCache for persistence.
type Service struct {
	settingsSvc       *settings.Service
	banCache          *BanCache
	logger            *slog.Logger
	authSettingsCache *cache.TTLCache[string, schema.AuthSettings]
}

// NewService creates a Service wired to the given settings service and
// ban cache.
func NewService(settingsSvc *settings.Service, banCache *BanCache, logger *slog.Logger) (*Service, error) {
	if settingsSvc == nil {
		return nil, fmt.Errorf("security: settings service is required")
	}
	if banCache == nil {
		return nil, fmt.Errorf("security: ban cache is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("security: logger is required")
	}
	return &Service{
		settingsSvc:       settingsSvc,
		banCache:          banCache,
		logger:            logger,
		authSettingsCache: cache.NewTTLCache[string, schema.AuthSettings](60 * time.Second),
	}, nil
}

// InvalidateAuthSettingsCache clears the locally cached AuthSettings so that
// the next read goes back to the Settings Service.  The Invalidator triggers
// this callback when the "settings" domain version changes.
func (s *Service) InvalidateAuthSettingsCache() {
	s.authSettingsCache.Invalidate(authSettingsKey)
}
