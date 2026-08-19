package leader

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
)

// mockStore implements TaskLeaseStore for testing.
type mockStore struct {
	mu           sync.Mutex
	lease        *model.TaskLease
	dbNow        time.Time
	acquireErr   error
	getErr       error
	releaseErr   error
	acquireCalls int
	releaseCalls int
}

func (m *mockStore) TryAcquire(_ context.Context, taskName, ownerID string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acquireCalls++
	if m.acquireErr != nil {
		return false, m.acquireErr
	}
	if m.lease != nil && m.lease.OwnerID != ownerID && m.lease.ExpiresAt.After(m.dbNow) {
		return false, nil
	}
	m.lease = &model.TaskLease{
		TaskName:  taskName,
		OwnerID:   ownerID,
		ExpiresAt: m.dbNow.Add(ttl),
	}
	return true, nil
}

func (m *mockStore) GetDatabaseTime(_ context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dbNow, m.acquireErr
}

func (m *mockStore) Get(_ context.Context, taskName string) (model.TaskLease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return model.TaskLease{}, false, m.getErr
	}
	if m.lease == nil {
		return model.TaskLease{}, false, nil
	}
	return *m.lease, true, nil
}

func (m *mockStore) ReleaseIfOwner(_ context.Context, taskName, ownerID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseCalls++
	if m.releaseErr != nil {
		return false, m.releaseErr
	}
	if m.lease != nil && m.lease.OwnerID == ownerID {
		m.lease = nil
		return true, nil
	}
	return false, nil
}

func newTestElector(t *testing.T, store TaskLeaseStore) *Elector {
	t.Helper()
	return NewElector("node-1", slog.Default(), store)
}

// startAndStopRefreshes starts the elector (one immediate refresh) and cancels
// the ticker loop so the test controls subsequent refreshes via refresh(ctx).
func startAndStopRefreshes(t *testing.T, e *Elector) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	// Let the first refresh complete
	time.Sleep(100 * time.Millisecond)
	// Cancel the context to stop the ticker loop; test drives subsequent refreshes manually
	cancel()
	// Wait for ticker goroutine to exit
	if e.done != nil {
		<-e.done
	}
	return ctx
}

func TestElector_AcquiresLeaseOnStart(t *testing.T) {
	store := &mockStore{
		dbNow: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if !e.IsLeader() {
		t.Fatal("expected to be leader after acquiring lease")
	}
	if e.LeaderID() != "node-1" {
		t.Fatalf("leader ID = %q, want node-1", e.LeaderID())
	}
}

func TestElector_StandbyWhenAcquireFails(t *testing.T) {
	store := &mockStore{
		dbNow:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		acquireErr: context.DeadlineExceeded,
	}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if e.IsLeader() {
		t.Fatal("expected NOT to be leader when acquire fails")
	}
}

func TestElector_DemotesOnLeaseExpiry(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if !e.IsLeader() {
		t.Fatal("expected to be leader after first acquire")
	}

	// Advance DB time past lease TTL and make refresh fail
	store.mu.Lock()
	store.dbNow = baseTime.Add(LeaseTTL + time.Second)
	store.acquireErr = context.DeadlineExceeded
	store.mu.Unlock()

	e.refresh(context.Background())

	if e.IsLeader() {
		t.Fatal("expected to be demoted after lease expiry")
	}
}

func TestElector_RefreshRenewsLease(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if !e.IsLeader() {
		t.Fatal("expected to be leader")
	}

	// Advance time but still within TTL
	store.mu.Lock()
	store.dbNow = baseTime.Add(10 * time.Second)
	store.mu.Unlock()

	e.refresh(context.Background())

	if !e.IsLeader() {
		t.Fatal("expected to remain leader after refresh")
	}
	if e.LeaderID() != "node-1" {
		t.Fatalf("leader ID = %q, want node-1", e.LeaderID())
	}
	store.mu.Lock()
	if store.acquireCalls < 2 {
		t.Fatalf("expected at least 2 acquire calls, got %d", store.acquireCalls)
	}
	store.mu.Unlock()
}

func TestElector_StopReleasesLease(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	e.Stop()

	if e.IsLeader() {
		t.Fatal("expected not to be leader after Stop")
	}
	store.mu.Lock()
	if store.releaseCalls == 0 {
		t.Fatal("expected ReleaseIfOwner to be called on Stop")
	}
	store.mu.Unlock()
}

func TestElector_StandbyWhenOtherNodeOwnsLease(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{
		dbNow: baseTime,
		lease: &model.TaskLease{
			TaskName:  LeaseTaskName,
			OwnerID:   "node-2",
			ExpiresAt: baseTime.Add(LeaseTTL),
		},
	}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if e.IsLeader() {
		t.Fatal("expected to be standby when another node owns lease")
	}
	if e.LeaderID() != "node-2" {
		t.Fatalf("leader ID = %q, want node-2", e.LeaderID())
	}
}

func TestElector_TakesOverExpiredLease(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{
		dbNow: baseTime,
		lease: &model.TaskLease{
			TaskName:  LeaseTaskName,
			OwnerID:   "node-2",
			ExpiresAt: baseTime.Add(-time.Second), // already expired
		},
	}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if !e.IsLeader() {
		t.Fatal("expected to take over expired lease")
	}
	if e.LeaderID() != "node-1" {
		t.Fatalf("leader ID = %q, want node-1", e.LeaderID())
	}
}

func TestElector_GetFailsDuringRefresh(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{
		dbNow:  baseTime,
		getErr: context.DeadlineExceeded,
	}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if e.IsLeader() {
		t.Fatal("expected to be standby when Get fails during refresh")
	}
}

func TestElector_IsLeaderAndLeaderID_ConcurrentSafe(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			_ = e.IsLeader()
			_ = e.LeaderID()
		})
	}
	wg.Wait()
	e.Stop()
}
