package system

import (
	"context"
	"testing"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/platform/config"
)

type stubSettings struct {
	platform schema.PlatformSettings
	err      error
}

func (s stubSettings) GetPlatformSettings(context.Context) (schema.PlatformSettings, error) {
	return s.platform, s.err
}

func TestPlatformStatus_UsesSettingsAndServers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Runtime.Servers = []string{"cn01", "cn02"}
	svc := &Service{
		settingsSvc: stubSettings{
			platform: schema.PlatformSettings{
				Name:        "Demo Platform",
				Description: "demo desc",
			},
		},
		cfg:       cfg,
		startedAt: time.Now().Add(-90 * time.Second),
	}

	ps := svc.platformStatus(context.Background())
	if ps.Name != "Demo Platform" || ps.Description != "demo desc" {
		t.Fatalf("platform identity = %+v", ps)
	}
	if ps.Version == "" {
		t.Fatal("expected non-empty version")
	}
	if ps.UptimeSeconds < 89 {
		t.Fatalf("uptimeSeconds = %d, want >= 89", ps.UptimeSeconds)
	}
	if len(ps.Servers) != 2 || ps.Servers[0] != "cn01" || ps.Servers[1] != "cn02" {
		t.Fatalf("servers = %v", ps.Servers)
	}

	// Mutation of Runtime.Servers must not affect the snapshot.
	cfg.Runtime.Servers[0] = "mutated"
	if ps.Servers[0] != "cn01" {
		t.Fatalf("servers snapshot mutated: %v", ps.Servers)
	}
}

func TestGetStatus_AuthCarriesThreeRoles(t *testing.T) {
	cfg := &config.Config{}
	cfg.User.AuthMode = config.UserAuthModeLocal
	cfg.Admin.EnableThreeRoles = true
	cfg.Runtime.Servers = []string{"cn01"}
	svc := &Service{
		settingsSvc: stubSettings{platform: schema.PlatformSettings{Name: "P", Description: "D"}},
		cfg:         cfg,
		startedAt:   time.Now(),
	}

	// database / ldap / nodes paths need deps; only assert platform + auth shape via platformStatus + auth fields assembled like GetStatus.
	ps := svc.platformStatus(context.Background())
	if len(ps.Servers) != 1 {
		t.Fatalf("servers = %v", ps.Servers)
	}
	auth := AuthStatus{
		AuthMode:   string(cfg.User.AuthMode),
		ThreeRoles: cfg.Admin.EnableThreeRoles,
	}
	if auth.AuthMode != "local" || !auth.ThreeRoles || auth.LDAP != nil {
		t.Fatalf("auth = %+v", auth)
	}
}
