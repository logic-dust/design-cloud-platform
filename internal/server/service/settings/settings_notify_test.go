package settings

import (
	"context"
	"slices"
	"testing"
	"time"

	"design-cloud-engine/internal/contract/schema"

	"github.com/stretchr/testify/require"
)

func TestSubscribeDomains_OnlyReportsChangedDomains(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	got := make(chan []schema.Domain, 4)
	svc.SubscribeDomains(func(_ context.Context, domains []schema.Domain) error {
		got <- append([]schema.Domain(nil), domains...)
		return nil
	})

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"sessionTTL":7200}`)))
	domains := recvDomains(t, got, 2*time.Second)
	if slices.Contains(domains, schema.DomainDesktop) {
		t.Fatalf("auth update notified desktop domain: %v", domains)
	}
	if !slices.Contains(domains, schema.DomainAuth) {
		t.Fatalf("auth update missing auth domain: %v", domains)
	}

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"maxAssets":4}`)))
	domains = recvDomains(t, got, 2*time.Second)
	if !slices.Contains(domains, schema.DomainDesktop) {
		t.Fatalf("desktop update missing desktop domain: %v", domains)
	}
	if slices.Contains(domains, schema.DomainAuth) {
		t.Fatalf("desktop update unexpectedly included auth: %v", domains)
	}
}

func TestGetDesktopSettingsStrict_RejectsInvalidStoredValue(t *testing.T) {
	svc, repo, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, repo.Save(ctx, schema.DomainDesktop.String(), map[string]any{
		"maxAssets":           5,
		"minSupportedVersion": "v1.0.0",
	}))

	_, err := svc.GetDesktopSettingsStrict(ctx)
	if err == nil {
		t.Fatal("expected invalid stored minSupportedVersion to fail strict load")
	}

	defaults, err := svc.GetDesktopSettings(ctx)
	require.NoError(t, err)
	if defaults.MinSupportedVersion != "" {
		t.Fatalf("fallback getter minSupportedVersion = %q, want empty", defaults.MinSupportedVersion)
	}
}

func recvDomains(t *testing.T, ch <-chan []schema.Domain, timeout time.Duration) []schema.Domain {
	t.Helper()
	select {
	case domains := <-ch:
		return domains
	case <-time.After(timeout):
		t.Fatal("timed out waiting for domain subscriber")
		return nil
	}
}
