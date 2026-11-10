package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
)

func newProxyStoreSession(connectionID uint, requester string) *ProxyConnection {
	return &ProxyConnection{
		ConnectionID: connectionID,
		Requester:    requester,
		Status:       StatusActive,
		StartedAt:    time.Now(),
		Protocol:     contract.VNCProtocol,
		Host:         "10.0.0.5",
		Port:         5901,
		Username:     "owner",
	}
}

func storeRegister(t *testing.T, store *proxySessionRegistry, session *ProxyConnection) {
	t.Helper()
	_, _, err := store.RegisterActive(sessionQuota{
		MaxGlobal:    0,
		MaxPerUser:   0,
		ConnectionID: session.ConnectionID,
		Requester:    session.Requester,
	}, session, newProxyConnectionLifecycle(t.Context()))
	if err != nil {
		t.Fatalf("RegisterActive: %v", err)
	}
}

func TestRegisterActiveRejectsWhenGlobalCapReached(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	storeRegister(t, store, newProxyStoreSession(1, "alice"))

	_, _, err := store.RegisterActive(sessionQuota{
		MaxGlobal:    1,
		MaxPerUser:   0,
		ConnectionID: 2,
		Requester:    "bob",
	}, newProxyStoreSession(2, "bob"), newProxyConnectionLifecycle(t.Context()))
	if !errors.Is(err, ErrProxyMaxSessionsReached) {
		t.Fatalf("error = %v, want max sessions", err)
	}
}

func TestDifferentRequestersOnSameConnectionCoexist(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	storeRegister(t, store, newProxyStoreSession(1, "alice"))
	storeRegister(t, store, newProxyStoreSession(1, "bob"))

	if _, err := store.GetByConnectionIDAndRequester(1, "alice"); err != nil {
		t.Fatalf("alice session: %v", err)
	}
	if _, err := store.GetByConnectionIDAndRequester(1, "bob"); err != nil {
		t.Fatalf("bob session: %v", err)
	}
}

func TestExclusiveConnectionReplacesPriorSession(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	first := newProxyStoreSession(1, "alice")
	storeRegister(t, store, first)
	oldID := store.byAnchorRequester[sessionKeyFor(1, 0, "alice")]

	_, _, err := store.RegisterActive(sessionQuota{
		MaxGlobal: 0, MaxPerUser: 0,
		ConnectionID: 1, Requester: "alice",
	}, newProxyStoreSession(1, "alice"), newProxyConnectionLifecycle(t.Context()))
	if err != nil {
		t.Fatalf("RegisterActive: %v", err)
	}
	newID := store.byAnchorRequester[sessionKeyFor(1, 0, "alice")]
	if newID == oldID {
		t.Fatalf("expected new session id")
	}
}

func TestCheckQuotaAccountsForReplacement(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	storeRegister(t, store, newProxyStoreSession(1, "alice"))

	if err := store.CheckQuota(sessionQuota{MaxGlobal: 1, ConnectionID: 1, Requester: "alice"}); err != nil {
		t.Fatalf("replacement should not hit cap: %v", err)
	}
	if err := store.CheckQuota(sessionQuota{MaxGlobal: 1, ConnectionID: 2, Requester: "bob"}); !errors.Is(err, ErrProxyMaxSessionsReached) {
		t.Fatalf("new session should be capped: %v", err)
	}
}

func TestProxyConnectionLifecycleAttachAfterCloseClosesSocket(t *testing.T) {
	t.Parallel()

	life := newProxyConnectionLifecycle(t.Context())
	client, server := net.Pipe()
	tcp, ws := life.Close()
	CloseDetachedSockets(tcp, ws)
	life.AttachTCP(server)
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
}

func TestRegisterActiveConcurrentCap(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	var wg sync.WaitGroup
	var rejected atomic.Int32
	for i := range 8 {
		wg.Go(func() {
			requester := fmt.Sprintf("user-%d", i)
			connectionID := uint(i + 1)
			_, _, err := store.RegisterActive(sessionQuota{
				MaxGlobal:    2,
				MaxPerUser:   0,
				ConnectionID: connectionID,
				Requester:    requester,
			}, newProxyStoreSession(connectionID, requester), newProxyConnectionLifecycle(context.Background()))
			if errors.Is(err, ErrProxyMaxSessionsReached) {
				rejected.Add(1)
			}
		})
	}
	wg.Wait()
	if got := len(store.List()); got > 2 {
		t.Fatalf("store has %d sessions, want <= 2", got)
	}
	if rejected.Load() == 0 {
		t.Fatal("expected some quota rejections")
	}
}

func TestRemoveByIDDoesNotAffectReplacementSession(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	storeRegister(t, store, newProxyStoreSession(1, "alice"))
	oldID := store.byAnchorRequester[sessionKeyFor(1, 0, "alice")]

	stored, _, err := store.RegisterActive(sessionQuota{
		MaxGlobal: 0, MaxPerUser: 0,
		ConnectionID: 1, Requester: "alice",
	}, newProxyStoreSession(1, "alice"), newProxyConnectionLifecycle(t.Context()))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	newID := stored.session.ID

	if _, err := store.RemoveByID(oldID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("RemoveByID(old) = %v, want not found", err)
	}
	got, err := store.GetByConnectionIDAndRequester(1, "alice")
	if err != nil {
		t.Fatalf("current session missing: %v", err)
	}
	if got.ID != newID {
		t.Fatalf("got session id=%d, want id=%d", got.ID, newID)
	}
}

func TestRemoveAllByRequester(t *testing.T) {
	t.Parallel()

	store := newProxySessionRegistry()
	storeRegister(t, store, newProxyStoreSession(1, "alice"))
	storeRegister(t, store, newProxyStoreSession(2, "alice"))
	storeRegister(t, store, newProxyStoreSession(3, "bob"))

	_ = store.RemoveAllByRequester("alice")
	if _, err := store.GetByConnectionIDAndRequester(1, "alice"); !errors.Is(err, contract.ErrNotFound) {
		t.Fatal("alice session 1 should be gone")
	}
	if _, err := store.GetByConnectionIDAndRequester(2, "alice"); !errors.Is(err, contract.ErrNotFound) {
		t.Fatal("alice session 2 should be gone")
	}
	if _, err := store.GetByConnectionIDAndRequester(3, "bob"); err != nil {
		t.Fatalf("bob session should remain: %v", err)
	}
}
