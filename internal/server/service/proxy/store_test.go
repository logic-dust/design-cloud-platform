package proxy

import (
	"errors"
	"testing"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/contract"
)

func newProxyStoreTunnel(connectionID uint, otp string, status Status, expiresAt time.Time) *Tunnel {
	return &Tunnel{
		ConnectionID: connectionID,
		OTP:          otp,
		Status:       status,
		ExpiresAt:    expiresAt,
		Protocol:     contract.VNCProtocol,
		Host:         "node-a",
		Port:         5901,
		Username:     "alice",
	}
}

func TestTunnelStoreGetByOTPRemovesExpiredPending(t *testing.T) {
	t.Parallel()

	store := newTunnelStore()
	tunnel := newProxyStoreTunnel(1, "otp-expired", StatusPending, time.Now().Add(-time.Minute))
	store.Create(tunnel)

	_, err := store.GetByOTP("otp-expired")
	if !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("GetByOTP error = %v, want not found", err)
	}
	if _, ok := store.byConnection[1]; ok {
		t.Fatalf("expired pending tunnel remained in byConnection")
	}
	if _, ok := store.byOTP["otp-expired"]; ok {
		t.Fatalf("expired pending tunnel remained in byOTP")
	}
}

func TestTunnelStoreActivateRemovesExpiredPending(t *testing.T) {
	t.Parallel()

	now := time.Now()
	store := newTunnelStore()
	tunnel := newProxyStoreTunnel(1, "otp-expired", StatusPending, now.Add(-time.Minute))
	store.Create(tunnel)

	_, err := store.Activate("otp-expired", now)
	if !errors.Is(err, ErrProxyTunnelActivateFailed) {
		t.Fatalf("Activate error = %v, want activation failure", err)
	}
	if _, ok := store.byConnection[1]; ok {
		t.Fatalf("expired pending tunnel remained in byConnection")
	}
	if _, ok := store.byOTP["otp-expired"]; ok {
		t.Fatalf("expired pending tunnel remained in byOTP")
	}
}

func TestTunnelStoreGetByConnectionIDRemovesExpiredPending(t *testing.T) {
	t.Parallel()

	store := newTunnelStore()
	tunnel := newProxyStoreTunnel(1, "otp-expired", StatusPending, time.Now().Add(-time.Minute))
	store.Create(tunnel)

	_, err := store.GetByConnectionID(1)
	if !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("GetByConnectionID error = %v, want not found", err)
	}
	if _, ok := store.byConnection[1]; ok {
		t.Fatalf("expired pending tunnel remained in byConnection")
	}
	if _, ok := store.byOTP["otp-expired"]; ok {
		t.Fatalf("expired pending tunnel remained in byOTP")
	}
}

func TestTunnelStoreListAndCountSkipExpiredPendingWithoutImmediateFullPrune(t *testing.T) {
	t.Parallel()

	store := newTunnelStore()
	expired := newProxyStoreTunnel(1, "otp-expired", StatusPending, time.Now().Add(-time.Minute))
	active := newProxyStoreTunnel(2, "otp-active", StatusActive, time.Now().Add(time.Hour))
	store.Create(expired)
	store.Create(active)

	store.mu.Lock()
	store.nextPruneAt = time.Now().Add(time.Hour)
	store.mu.Unlock()

	if got := store.CountActive(); got != 1 {
		t.Fatalf("CountActive = %d, want 1", got)
	}
	tunnels := store.List()
	if len(tunnels) != 1 || tunnels[0].ConnectionID != 2 {
		t.Fatalf("List = %+v, want only active tunnel", tunnels)
	}
	if _, ok := store.byConnection[1]; !ok {
		t.Fatalf("expired pending tunnel was fully pruned before nextPruneAt")
	}

	store.mu.Lock()
	store.nextPruneAt = time.Time{}
	store.mu.Unlock()
	if got := store.CountActive(); got != 1 {
		t.Fatalf("CountActive after due prune = %d, want 1", got)
	}
	if _, ok := store.byConnection[1]; ok {
		t.Fatalf("expired pending tunnel remained after due prune")
	}
}
