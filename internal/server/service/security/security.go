// Package security provides authentication failure tracking, IP/user banning,
// and authorization boundary enforcement.
package security

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/model"
)

// defaultAuthSettings is the single fallback source for ban thresholds when
// settings are unavailable or return zero values. It is DomainAuth's seed from
// schema.DefaultSettings — do not duplicate those literals here.
var defaultAuthSettings = mustDefaultAuthSettings()

func mustDefaultAuthSettings() schema.AuthSettings {
	v, ok := schema.DefaultSettings(schema.DomainAuth)
	if !ok {
		panic("schema: DomainAuth defaults missing")
	}
	return v.(schema.AuthSettings)
}

// BannedRecord is the client-facing representation of an active ban.
type BannedRecord struct {
	Subject  model.BanSubject `json:"subject"`
	Source   model.BanSource  `json:"source"`
	Username string           `json:"username"`
	IP       string           `json:"ip"`
	BanTime  time.Time        `json:"banTime"`
	Reason   string           `json:"reason"`
}

// Service handles IP and user ban logic, checking thresholds and
// coordinating with BanCache for persistence.
type Service struct {
	settingsSvc  SettingsService
	banCache     *BanCache
	logger       *slog.Logger
	onUserBanned func(username string)
}

// Option customizes a Service at construction time; the service is never
// mutated afterwards.
type Option func(*Service)

// WithUserBanHook registers a callback invoked when a user-scoped ban is
// applied (e.g. tear down that user's ProxyConnections). Injection happens
// at construction time only.
func WithUserBanHook(fn func(username string)) Option {
	return func(s *Service) {
		if fn != nil {
			s.onUserBanned = fn
		}
	}
}

// NewService creates a Service wired to the given settings service and
// ban cache.
func NewService(settingsSvc SettingsService, banCache *BanCache, logger *slog.Logger, opts ...Option) (*Service, error) {
	if settingsSvc == nil {
		return nil, fmt.Errorf("security: settings service is required")
	}
	if banCache == nil {
		return nil, fmt.Errorf("security: ban cache is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("security: logger is required")
	}
	s := &Service{
		settingsSvc: settingsSvc,
		banCache:    banCache,
		logger:      logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// parsedBanAllowlist is the pre-parsed form of AuthSettings.BanAllowlist.
// Entries are validated at settings-write time, so parse failures cannot occur here.
type parsedBanAllowlist struct {
	addrs    map[netip.Addr]struct{}
	prefixes []netip.Prefix
}

func parseBanAllowlist(entries []string) parsedBanAllowlist {
	wl := parsedBanAllowlist{addrs: make(map[netip.Addr]struct{}, len(entries))}
	for _, entry := range entries {
		if addr, err := netip.ParseAddr(entry); err == nil {
			wl.addrs[addr] = struct{}{}
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			wl.prefixes = append(wl.prefixes, prefix.Masked())
		}
	}
	return wl
}

func (w parsedBanAllowlist) contains(addr netip.Addr) bool {
	if _, ok := w.addrs[addr]; ok {
		return true
	}
	return slices.ContainsFunc(w.prefixes, func(p netip.Prefix) bool {
		return p.Contains(addr)
	})
}

// IsAllowlisted reports whether the IP is in the ban allowlist (AuthSettings.BanAllowlist)
// and thus exempt from failure counting and banning. Unparseable input never matches.
func (s *Service) IsAllowlisted(ctx context.Context, ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	return parseBanAllowlist(s.getAuthSettings(ctx).BanAllowlist).contains(addr)
}
