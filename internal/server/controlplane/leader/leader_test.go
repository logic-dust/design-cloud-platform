package leader

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

// mockStore implements TaskLeaseRepo for testing.
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

func (m *mockStore) TryAcquire(_ context.Context, taskName, ownerID string, ttl time.Duration) (bool, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acquireCalls++
	if m.acquireErr != nil {
		return false, 0, m.acquireErr
	}
	if m.lease == nil {
		m.lease = &model.TaskLease{
			TaskName:  taskName,
			OwnerID:   ownerID,
			Epoch:     1,
			ExpiresAt: m.dbNow.Add(ttl),
		}
		return true, 1, nil
	}
	alive := m.lease.ExpiresAt.After(m.dbNow)
	switch {
	case m.lease.OwnerID == ownerID && alive:
		// 存活续约：epoch 不变
		m.lease.ExpiresAt = m.dbNow.Add(ttl)
		return true, m.lease.Epoch, nil
	case !alive:
		// 跨过期重获 / 抢占：epoch+1
		m.lease.OwnerID = ownerID
		m.lease.Epoch++
		m.lease.ExpiresAt = m.dbNow.Add(ttl)
		return true, m.lease.Epoch, nil
	default:
		return false, 0, nil
	}
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

func newTestElector(t *testing.T, store TaskLeaseRepo) *Elector {
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
			_ = e.LocalEpoch()
			_ = e.LeaderEpoch()
		})
	}
	wg.Wait()
	e.Stop()
}

// 存活续约 epoch 不变；同 Owner 跨过期重获 epoch+1。
func TestElector_EpochRenewVsReacquire(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if got := e.LocalEpoch(); got != 1 {
		t.Fatalf("LocalEpoch after acquire = %d, want 1", got)
	}
	if got := e.LeaderEpoch(); got != 1 {
		t.Fatalf("LeaderEpoch after acquire = %d, want 1", got)
	}

	// 存活续约：epoch 不变
	store.mu.Lock()
	store.dbNow = baseTime.Add(10 * time.Second)
	store.mu.Unlock()
	e.refresh(context.Background())
	if got := e.LocalEpoch(); got != 1 {
		t.Fatalf("LocalEpoch after alive renew = %d, want 1", got)
	}

	// 同 Owner 跨过期重获：epoch+1（上一步续约已把过期时间顺延到 +40s，需推进超过它）
	store.mu.Lock()
	store.dbNow = baseTime.Add(LeaseTTL + 11*time.Second)
	store.mu.Unlock()
	e.refresh(context.Background())
	if !e.IsLeader() {
		t.Fatal("expected to re-acquire lease after expiry")
	}
	if got := e.LocalEpoch(); got != 2 {
		t.Fatalf("LocalEpoch after re-acquire = %d, want 2", got)
	}
	e.Stop()
}

// Standby 观测对端 Leader epoch；抢占过期租约后 epoch+1。
func TestElector_EpochObservedAndTakeover(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{
		dbNow: baseTime,
		lease: &model.TaskLease{
			TaskName:  LeaseTaskName,
			OwnerID:   "node-2",
			Epoch:     7,
			ExpiresAt: baseTime.Add(LeaseTTL),
		},
	}
	e := newTestElector(t, store)
	_ = startAndStopRefreshes(t, e)

	if e.IsLeader() {
		t.Fatal("expected standby while node-2 holds lease")
	}
	if got := e.LocalEpoch(); got != 0 {
		t.Fatalf("LocalEpoch on standby = %d, want 0", got)
	}
	if got := e.LeaderEpoch(); got != 7 {
		t.Fatalf("LeaderEpoch on standby = %d, want 7", got)
	}

	// node-2 租约过期：抢占，epoch 7→8
	store.mu.Lock()
	store.dbNow = baseTime.Add(LeaseTTL + time.Second)
	store.mu.Unlock()
	e.refresh(context.Background())
	if !e.IsLeader() {
		t.Fatal("expected takeover of expired lease")
	}
	if got := e.LocalEpoch(); got != 8 {
		t.Fatalf("LocalEpoch after takeover = %d, want 8", got)
	}
	e.Stop()
}

// 降级触发钩子且 LocalEpoch 归零。
func TestElector_DemotionHookFires(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)

	var hookCalled int
	var hookLeaderID string
	e.SetDemotionHook(func(newLeaderID string) {
		hookCalled++
		hookLeaderID = newLeaderID
	})
	_ = startAndStopRefreshes(t, e)
	if !e.IsLeader() {
		t.Fatal("expected to be leader after first acquire")
	}

	// 续约失败且租约过期 → 降级
	store.mu.Lock()
	store.dbNow = baseTime.Add(LeaseTTL + time.Second)
	store.acquireErr = context.DeadlineExceeded
	store.mu.Unlock()
	e.refresh(context.Background())

	if e.IsLeader() {
		t.Fatal("expected demotion after lease expiry")
	}
	if hookCalled != 1 {
		t.Fatalf("demotion hook called %d times, want 1", hookCalled)
	}
	if got := e.LocalEpoch(); got != 0 {
		t.Fatalf("LocalEpoch after demotion = %d, want 0", got)
	}
	_ = hookLeaderID
}

// DB 不可用导致降级时，不得把自身广播为 Leader：Agent 会被重定向回同一个 Standby
// 并被再次拒绝，形成重连循环。
func TestElector_DemotionDoesNotAdvertiseSelf(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{dbNow: baseTime}
	e := newTestElector(t, store)

	var hookLeaderID string
	e.SetDemotionHook(func(newLeaderID string) { hookLeaderID = newLeaderID })
	_ = startAndStopRefreshes(t, e)
	if !e.IsLeader() {
		t.Fatal("expected to be leader after first acquire")
	}
	if e.LeaderID() == "" {
		t.Fatal("leader should report itself before demotion")
	}

	store.mu.Lock()
	store.dbNow = baseTime.Add(LeaseTTL + time.Second)
	store.acquireErr = context.DeadlineExceeded
	store.mu.Unlock()
	e.refresh(context.Background())

	if e.IsLeader() {
		t.Fatal("expected demotion after lease expiry")
	}
	if got := e.LeaderID(); got != "" {
		t.Fatalf("LeaderID after demotion = %q, want empty (must not advertise self)", got)
	}
	if hookLeaderID != "" {
		t.Fatalf("demotion hook leader = %q, want empty", hookLeaderID)
	}
}

// 故障演练（refactor v5 §4.6.3 的自动化等价物）：模拟 Leader 进程崩溃（取消 refresh
// 循环但不释放租约），Standby 必须在租约过期后接管，且接管时 epoch +1——旧 Leader
// 在失联窗口签发的命令随 epoch 失效（fencing 的拒绝路径由 control 包转发测试覆盖）。
func TestElector_TakeoverAfterLeaderCrash(t *testing.T) {
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "election.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(store)

	const (
		leaseTTL      = 2 * time.Second
		renewInterval = 400 * time.Millisecond
	)
	newElector := func(nodeID string) *Elector {
		return NewElector(nodeID, slog.Default(), repos.TaskLease,
			WithLeaseTTL(leaseTTL), WithRenewInterval(renewInterval))
	}

	// node-a 先启动并成为 Leader。
	electorA := newElector("node-a")
	ctxA, crashA := context.WithCancel(context.Background())
	electorA.Start(ctxA)
	if !electorA.IsLeader() {
		t.Fatal("node-a should become leader on first refresh")
	}
	if got := electorA.LocalEpoch(); got != 1 {
		t.Fatalf("node-a epoch = %d, want 1", got)
	}

	// node-b 启动进入 Standby，观测到 node-a 的租约与 epoch。
	electorB := newElector("node-b")
	electorB.Start(context.Background())
	t.Cleanup(electorB.Stop)
	if electorB.IsLeader() {
		t.Fatal("node-b must remain standby while node-a holds a live lease")
	}
	if got := electorB.LeaderID(); got != "node-a" {
		t.Fatalf("node-b observed leader = %q, want node-a", got)
	}
	if got := electorB.LeaderEpoch(); got != 1 {
		t.Fatalf("node-b observed leader epoch = %d, want 1", got)
	}

	// 模拟 node-a 崩溃：停止 refresh 但不释放租约（进程暴死无法走 Stop 的优雅释放）。
	crashTime := time.Now()
	crashA()
	<-electorA.done

	// node-b 不得在租约过期前抢占；必须在过期后的一个续约节拍内接管。
	deadline := time.Now().Add(15 * time.Second)
	for !electorB.IsLeader() {
		if time.Now().After(deadline) {
			t.Fatal("node-b did not take over within 15s of node-a crash")
		}
		time.Sleep(50 * time.Millisecond)
	}
	tookOverAfter := time.Since(crashTime)
	if tookOverAfter < leaseTTL {
		t.Fatalf("node-b took over after %s, before lease TTL %s expired", tookOverAfter, leaseTTL)
	}
	if got := electorB.LocalEpoch(); got != 2 {
		t.Fatalf("node-b epoch after takeover = %d, want 2 (expiry-based reacquire must increment)", got)
	}
	if got := electorB.LeaderID(); got != "node-b" {
		t.Fatalf("leader after takeover = %q, want node-b", got)
	}
	t.Logf("node-b took over %s after crash (TTL %s)", tookOverAfter.Round(time.Millisecond), leaseTTL)
}
