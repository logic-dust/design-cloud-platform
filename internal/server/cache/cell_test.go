package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Cell Tests（统一单值缓存原语：TTL 过期 + 采样指纹 + 双锁）
// =============================================================================

func TestCell_InitialLoad(t *testing.T) {
	t.Parallel()

	var cell Cell[string]
	var callCount int32

	refresh := func(ctx context.Context) {
		atomic.AddInt32(&callCount, 1)
		cell.Set("initial_data", time.Now())
	}

	val := cell.GetOrRefresh(context.Background(), 100*time.Millisecond, refresh)

	if val != "initial_data" {
		t.Errorf("got %q, want 'initial_data'", val)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}
}

func TestCell_HitFreshCache(t *testing.T) {
	t.Parallel()

	var cell Cell[string]
	cell.Set("fresh_data", time.Now())

	var callCount int32
	refresh := func(ctx context.Context) {
		atomic.AddInt32(&callCount, 1)
	}

	val := cell.GetOrRefresh(context.Background(), 1*time.Hour, refresh)

	if val != "fresh_data" {
		t.Errorf("got %q, want 'fresh_data'", val)
	}
	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("callCount = %d, want 0 for fresh cache", callCount)
	}
}

func TestCell_StaleTriggersBackgroundRefresh(t *testing.T) {
	t.Parallel()

	var cell Cell[string]
	cell.Set("stale_data", time.Now().Add(-10*time.Minute)) // stale

	refreshed := make(chan struct{})
	refresh := func(ctx context.Context) {
		cell.Set("new_data", time.Now())
		close(refreshed)
	}

	// First call should return stale data immediately and start background refresh
	val := cell.GetOrRefresh(context.Background(), 1*time.Minute, refresh)
	if val != "stale_data" {
		t.Errorf("got %q, want 'stale_data' on stale read", val)
	}

	// Wait for background refresh to finish
	select {
	case <-refreshed:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for background refresh")
	}

	// Cell data should now be updated
	if got := cell.GetOrRefresh(context.Background(), 1*time.Minute, refresh); got != "new_data" {
		t.Errorf("got %q after refresh, want 'new_data'", got)
	}
}

func TestCell_ConcurrentReadDoesNotSingleFlightStampede(t *testing.T) {
	t.Parallel()

	var cell Cell[int]
	cell.Set(42, time.Now().Add(-10*time.Minute)) // stale

	var callCount atomic.Int32
	blockChan := make(chan struct{})
	refresh := func(ctx context.Context) {
		callCount.Add(1)
		<-blockChan
		cell.Set(100, time.Now())
	}

	var wg sync.WaitGroup
	// Launch 10 concurrent calls on stale cache
	for range 10 {
		wg.Go(func() {
			_ = cell.GetOrRefresh(context.Background(), 1*time.Minute, refresh)
		})
	}

	// Give goroutines a moment to hit TryLock
	time.Sleep(50 * time.Millisecond)

	// Unblock refresh
	close(blockChan)
	wg.Wait()

	// Only 1 background refresh should have run due to TryLock
	if count := callCount.Load(); count != 1 {
		t.Errorf("callCount = %d, want 1 (TryLock should prevent stampede)", count)
	}
}

// sampledAt 的所有读取都必须在锁内：Set 在写锁内更新它，而 GetOrRefresh 会在持锁
// 判断过期后、进分支前再读一次"是否从未采样"。
//
// 该测试用 `-race` 运行才有检测力（普通运行只验证不 panic/不误判）：让"读 stale
// 判断 + 读 IsZero"与"refresh 内的 Set 回填"持续并发。修复前 GetOrRefresh 会在锁外
// 读 sampledAt，-race 报 DATA RACE；修复后干净通过。
//
// 注意不能靠 TestCell_ConcurrentReadDoesNotSingleFlightStampede 覆盖：它的 refresh
// 被 blockChan 卡住、在读全部结束之后才 Set，读写不重叠，-race 不会报。
func TestCell_ConcurrentReadAndRefreshSetRaceFree(t *testing.T) {
	t.Parallel()

	var cell Cell[int]
	cell.Set(1, time.Now())

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 多个读者持续进入 stale 分支（TTL 微不足道 → 每次调用都判为过期）
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = cell.GetOrRefresh(context.Background(), time.Nanosecond,
						func(context.Context) { cell.Set(2, time.Now()) })
				}
			}
		})
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	if got := cell.SampledAt(); got.IsZero() {
		t.Error("refresh 回填后 sampledAt 不应为零值")
	}
}

func TestCell_SetAndSampledAt(t *testing.T) {
	t.Parallel()
	var cell Cell[string]
	if !cell.SampledAt().IsZero() {
		t.Error("SampledAt should be zero before first Set")
	}
	if cell.IsFresh(time.Second) {
		t.Error("empty cell should not be fresh")
	}

	now := time.Now()
	cell.Set("v", now)

	if cell.SampledAt() != now {
		t.Errorf("SampledAt = %v, want %v", cell.SampledAt(), now)
	}
	if !cell.IsFresh(time.Second) {
		t.Error("cell should be fresh right after Set")
	}
	if cell.IsFresh(-time.Second) {
		t.Error("cell with negative TTL should be stale")
	}
}
