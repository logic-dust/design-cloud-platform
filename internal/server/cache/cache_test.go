package cache

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 容量自生效：写入路径内建裁剪，调用方无需任何配合（旧的协作式 EnsureCapacity 已删除）。

func TestSetMaxEntries_EvictsEarliestExpiring(t *testing.T) {
	t.Parallel()
	// cap=3 ≤ evictSampleSize=5：样本盖住全集，淘汰等于全局最早过期（采样的精确特例）。
	c := NewTTLCache[string, int](time.Hour).SetMaxEntries(3)
	c.SetWithTTL("short", 1, time.Minute) // 最早过期
	c.SetWithTTL("mid", 2, time.Hour)
	c.SetWithTTL("long", 3, 2*time.Hour)

	// 第 4 条写入触发裁剪
	c.SetWithTTL("newest", 4, 3*time.Hour)

	require.LessOrEqual(t, c.Len(), 3, "cache must stay within max entries")
	_, ok := c.GetIfPresent("newest")
	require.True(t, ok, "the entry just written must be present")
	_, ok = c.GetIfPresent("short")
	require.False(t, ok, "earliest-expiring entry must be evicted first")
}

func TestSetMaxEntries_ReclaimsExpiredBeforeEvictingLive(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour).SetMaxEntries(3)
	c.SetWithTTL("expired-a", 1, time.Millisecond)
	c.SetWithTTL("expired-b", 2, time.Millisecond)
	c.SetWithTTL("live", 3, time.Hour)
	time.Sleep(5 * time.Millisecond)

	c.SetWithTTL("newest", 4, time.Hour)

	// 样本里过期项优先于存活项；一次写入只腾一个槽位，不要求一次收尽全部过期项。
	_, ok := c.GetIfPresent("live")
	require.True(t, ok, "live entry must survive when an expired sample member frees a slot")
	require.LessOrEqual(t, c.Len(), 3)
}

func TestSetMaxEntries_UnlimitedByDefault(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	for i := range 50 {
		c.SetWithTTL(string(rune('a'+i%26))+string(rune('0'+i/26)), i, time.Hour)
	}
	require.Equal(t, 50, c.Len(), "unbounded cache must keep every entry")
}

// GetWithTTL 的写入路径（回填）同样受容量约束。
func TestSetMaxEntries_AppliesToGetWithTTLFills(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour).SetMaxEntries(4)
	for i := range 20 {
		key := string(rune('a' + i))
		_, err := c.GetWithTTL(context.Background(), key, time.Hour,
			func(_ context.Context, _ string) (int, error) { return i, nil })
		require.NoError(t, err)
	}
	require.LessOrEqual(t, c.Len(), 4, "GetWithTTL fills must respect the cap too")
}

// 裁剪是增量式的：满员后每新增一条只腾出一个槽位，不会成批丢掉工作集。
func TestSetMaxEntries_EvictsOneEntryPerInsert(t *testing.T) {
	t.Parallel()
	const capEntries = 64
	c := NewTTLCache[int, int](time.Hour).SetMaxEntries(capEntries)
	for i := range capEntries {
		c.SetWithTTL(i, i, time.Duration(i+1)*time.Minute)
	}
	require.Equal(t, capEntries, c.Len())

	c.SetWithTTL(999, 999, 10*time.Hour)

	// 容量不变（腾一个槽位给新条目），而不是被裁到只剩一小部分
	require.Equal(t, capEntries, c.Len(), "满员后新增一条只应腾出一个槽位")
	_, ok := c.GetIfPresent(999)
	require.True(t, ok, "刚写入的条目必须在")
}

// 满员时覆盖已有 key 不得淘汰其它存活项：条目数没有净增，不需要腾槽位。
// 回归"续期/改值顺手裁掉一批无关条目"的缺陷。
func TestSetMaxEntries_OverwriteLiveKeyDoesNotEvictOthers(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour).SetMaxEntries(3)
	c.SetWithTTL("a", 1, time.Minute) // 最早过期，最容易被误裁
	c.SetWithTTL("b", 2, time.Hour)
	c.SetWithTTL("c", 3, 2*time.Hour)
	require.Equal(t, 3, c.Len())

	c.SetWithTTL("c", 33, 3*time.Hour) // 覆盖仍存活的 key

	require.Equal(t, 3, c.Len(), "overwrite must not shrink the cache")
	_, okA := c.GetIfPresent("a")
	require.True(t, okA, "unrelated live entry must survive an overwrite")
	_, okB := c.GetIfPresent("b")
	require.True(t, okB, "unrelated live entry must survive an overwrite")
	got, okC := c.GetIfPresent("c")
	require.True(t, okC)
	require.Equal(t, 33, got, "overwrite must take effect")
}

// TestCleanup verifies that expired entries are removed and fresh ones kept.
func TestCleanup(t *testing.T) {
	c := NewTTLCache[string, int](50 * time.Millisecond)
	c.mu.Lock()
	c.data["expired"] = entry[int]{value: 1, expiresAt: time.Now().Add(-time.Second)}
	c.data["fresh"] = entry[int]{value: 2, expiresAt: time.Now().Add(time.Hour)}
	c.mu.Unlock()

	c.Cleanup()

	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.data["expired"]; ok {
		t.Error("expired entry was not cleaned up")
	}
	if _, ok := c.data["fresh"]; ok {
		// fresh entry must survive
	} else {
		t.Error("fresh entry was wrongly removed")
	}
}

// TestStartJanitor verifies the global janitor removes expired entries after ticking.
func TestStartJanitor(t *testing.T) {
	c := NewTTLCache[string, int](10 * time.Millisecond).EnableJanitor()
	stop := StartJanitors(20 * time.Millisecond)
	t.Cleanup(stop)

	c.mu.Lock()
	c.data["stale"] = entry[int]{value: 1, expiresAt: time.Now().Add(-time.Second)}
	c.mu.Unlock()

	// Wait for at least one janitor tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		_, ok := c.data["stale"]
		c.mu.RUnlock()
		if !ok {
			return // cleaned up
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("janitor did not clean up expired entry within deadline")
}

// TestConcurrentCleanupAndGet ensures Cleanup does not race with Get.
func TestConcurrentCleanupAndGet(t *testing.T) {
	c := NewTTLCache[string, int](50 * time.Millisecond)
	fetch := func(_ context.Context, _ string) (int, error) { return 42, nil }

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				_, _ = c.Get(context.TODO(), "k", fetch)
			}
		})
		wg.Go(func() {
			for range 100 {
				c.Cleanup()
			}
		})
	}
	wg.Wait()
}

// 失效只删"当时已存在"的条目，拦不住在途 fetch。若回填不校验代次，旧值会在失效
// 之后被写回并长期驻留（settings 域 TTL 长达 24h），使失效形同虚设——表现为
// "管理员写成功、后续读取仍是旧值"。
func TestGetWithTTL_FillStartedBeforeInvalidateIsDiscarded(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)

	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	callerDone := make(chan struct{})

	go func() {
		defer close(callerDone)
		_, _ = c.GetWithTTL(context.Background(), "k", time.Hour,
			func(context.Context, string) (int, error) {
				close(fetchStarted)
				<-releaseFetch
				return 1, nil // 失效前的旧值
			})
	}()

	<-fetchStarted
	c.Invalidate("k") // 回填尚未落地时发生失效
	close(releaseFetch)
	<-callerDone

	_, ok := c.GetIfPresent("k")
	require.False(t, ok, "失效发生在回填之前，该回填必须被丢弃，不得把旧值留在缓存里")
}

// Clear 同理：清空发生在回填之前时，该回填不得把旧值写回。
func TestGetWithTTL_FillStartedBeforeClearIsDiscarded(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	c.SetWithTTL("k", 0, time.Nanosecond) // 过期占位，让 fetch 成为唯一路径

	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	callerDone := make(chan struct{})

	go func() {
		defer close(callerDone)
		_, _ = c.GetWithTTL(context.Background(), "other", time.Hour,
			func(context.Context, string) (int, error) {
				close(fetchStarted)
				<-releaseFetch
				return 7, nil
			})
	}()

	<-fetchStarted
	c.Clear() // 回填尚未落地时清空
	close(releaseFetch)
	<-callerDone

	_, ok := c.GetIfPresent("other")
	require.False(t, ok, "清空发生在回填之前，该回填必须被丢弃")
}

// 无失效发生时，回填必须正常落地——守卫不能把正常路径也拦掉。
func TestGetWithTTL_FillStillLandsWithoutInvalidation(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	ctx := context.Background()

	fetches := 0
	first, err := c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
		fetches++
		return 42, nil
	})
	require.NoError(t, err)
	require.Equal(t, 42, first)

	second, err := c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
		fetches++
		return 99, nil
	})
	require.NoError(t, err)
	require.Equal(t, 42, second, "第二次必须命中缓存，返回首次回填的值")
	require.Equal(t, 1, fetches, "缓存命中时不应再执行 fetch")
	require.Equal(t, 1, c.Len(), "无失效时回填应正常写入")
}

// Clear 之后的新 Get 不得加入清空前的 in-flight fetch：gen 守卫只阻止回填，
// 若不把代次编进 flight key，后到的调用者会拿到旧 fetch 的返回值。
func TestGetWithTTL_PostClearDoesNotJoinInFlightFetch(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	ctx := context.Background()

	fetchAStarted := make(chan struct{})
	releaseA := make(chan struct{})
	aResult := make(chan int, 1)

	aErr := make(chan error, 1)
	go func() {
		v, err := c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
			close(fetchAStarted)
			<-releaseA
			return 1, nil
		})
		aErr <- err
		aResult <- v
	}()

	<-fetchAStarted
	c.Clear()

	bFetchStarted := make(chan struct{})
	bDone := make(chan struct{})
	var (
		v   int
		err error
	)
	go func() {
		defer close(bDone)
		v, err = c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
			close(bFetchStarted)
			return 2, nil
		})
	}()
	select {
	case <-bDone:
	case <-time.After(2 * time.Second):
		t.Fatal("清空后的 Get 仍在等待清空前的 in-flight fetch")
	}
	require.NoError(t, err)
	require.Equal(t, 2, v, "清空后的调用必须自己回源，不得拿到清空前 in-flight 的旧值")
	select {
	case <-bFetchStarted:
	default:
		t.Fatal("清空后的 Get 加入了清空前的 in-flight fetch")
	}

	got, ok := c.GetIfPresent("k")
	require.True(t, ok)
	require.Equal(t, 2, got, "后到的回填应按新代次写入")

	close(releaseA)
	select {
	case err := <-aErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("清空前的 Get 未在超时内返回")
	}
	require.Equal(t, 1, <-aResult)
	got, ok = c.GetIfPresent("k")
	require.True(t, ok)
	require.Equal(t, 2, got, "清空前的回填不得覆盖新代次已写入的值")
}

// Generation + SetIfGeneration 是批量回填的代次守卫：在查询开始前记录代次，写入时
// 校验；期间发生过失效（Invalidate/Clear）的条目一律放弃写入，避免把失效前算出的
// 旧值写回缓存。失效只删"当时已存在"的条目，拦不住在途回填，所以必须由写入侧校验。
func TestSetIfGeneration(t *testing.T) {
	t.Parallel()

	t.Run("代次未变时写入", func(t *testing.T) {
		t.Parallel()
		c := NewTTLCache[string, int](time.Hour)
		gen := c.Generation()
		require.True(t, c.SetIfGeneration(gen, "k", 1, time.Hour))
		got, ok := c.GetIfPresent("k")
		require.True(t, ok)
		require.Equal(t, 1, got)
	})

	t.Run("Invalidate 之后放弃写入", func(t *testing.T) {
		t.Parallel()
		c := NewTTLCache[string, int](time.Hour)
		gen := c.Generation()
		c.Invalidate("k")
		require.False(t, c.SetIfGeneration(gen, "k", 1, time.Hour),
			"失效之后的回填必须被丢弃")
		_, ok := c.GetIfPresent("k")
		require.False(t, ok)
	})

	t.Run("Clear 之后放弃写入", func(t *testing.T) {
		t.Parallel()
		c := NewTTLCache[string, int](time.Hour)
		gen := c.Generation()
		c.Clear()
		require.False(t, c.SetIfGeneration(gen, "k", 1, time.Hour))
		require.Equal(t, 0, c.Len())
	})

	t.Run("批中途发生失效：后续写入全部放弃", func(t *testing.T) {
		t.Parallel()
		c := NewTTLCache[int, int](time.Hour)
		gen := c.Generation()

		written := 0
		for i := range 5 {
			if i == 2 {
				c.Invalidate(-1) // 模拟失效落在回填过程中（删的是无关 key，不动已写入的）
			}
			if !c.SetIfGeneration(gen, i, i, time.Hour) {
				break
			}
			written++
		}
		require.Equal(t, 2, written, "失效前的 2 条写入，其后必须全部放弃")
		require.Equal(t, 2, c.Len(), "只有失效前写入的条目留下")
	})

	t.Run("新代次下可继续写入", func(t *testing.T) {
		t.Parallel()
		c := NewTTLCache[string, int](time.Hour)
		c.Clear()
		fresh := c.Generation() // 失效之后重新捕获
		require.True(t, c.SetIfGeneration(fresh, "k", 1, time.Hour))
		require.Equal(t, 1, c.Len())
	})

	t.Run("ttl<=0 回退缓存级 TTL", func(t *testing.T) {
		t.Parallel()
		c := NewTTLCache[string, int](time.Hour)
		gen := c.Generation()
		require.True(t, c.SetIfGeneration(gen, "k", 1, 0))
		_, ok := c.GetIfPresent("k")
		require.True(t, ok)
	})
}

// 覆盖已有 key 也必须走代次校验（它是"回填"而非"显式写入"）。
func TestSetIfGeneration_OverwriteAlsoGuarded(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	c.SetWithTTL("k", 1, time.Hour)

	gen := c.Generation()
	c.Invalidate("k")

	require.False(t, c.SetIfGeneration(gen, "k", 2, time.Hour))
	_, ok := c.GetIfPresent("k")
	require.False(t, ok, "失效后不得被回填覆盖回来")
}

// Generation 单调递增，只在失效路径上变化——正常的读写不应改动它。
func TestGeneration_OnlyMovesOnInvalidation(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	start := c.Generation()

	c.SetWithTTL("a", 1, time.Hour)
	_, err := c.GetWithTTL(context.Background(), "b", time.Hour,
		func(context.Context, string) (int, error) { return 2, nil })
	require.NoError(t, err)
	_, _ = c.GetIfPresent("a")
	require.Equal(t, start, c.Generation(), "普通读写不改动代次")

	c.Invalidate("a")
	require.Greater(t, c.Generation(), start)

	afterInvalidate := c.Generation()
	c.Clear()
	require.Greater(t, c.Generation(), afterInvalidate)
}

// TestInvalidator_RegisterAppendsCallbacks 锁定"同一主题多回调累加"的语义。
//
// 回归背景：Register 曾按主题名覆盖写入 map，导致同一主题的第二个注册者
// 悄悄顶掉第一个。典型受害者是 settings 主题——settings.Service 注册的
// "清空域缓存"被 service.go 注册的 "Security 派生缓存失效" 覆盖，
// 使跨节点 settings 变更实际不生效（只在多节点部署暴露）。
func TestInvalidator_RegisterAppendsCallbacks(t *testing.T) {
	t.Parallel()
	inv := NewInvalidator(nil, slog.Default())

	var first, second int
	inv.Register("settings", func() { first++ }) // topic label arbitrary in unit test
	inv.Register("settings", func() { second++ })

	reg, ok := inv.registered["settings"]
	require.True(t, ok, "topic must be registered")
	require.Len(t, reg.callbacks, 2, "same topic must accumulate callbacks")

	for _, cb := range reg.callbacks {
		cb()
	}
	require.Equal(t, 1, first)
	require.Equal(t, 1, second)
}

// TestInvalidator_RegisterSeparateTopics 不同主题各自独立。
func TestInvalidator_RegisterSeparateTopics(t *testing.T) {
	t.Parallel()
	inv := NewInvalidator(nil, slog.Default())

	inv.Register("sessions", func() {})
	inv.Register("bans", func() {})
	inv.Register("identity", func() {})

	require.Len(t, inv.registered, 3)
	require.Len(t, inv.registered["sessions"].callbacks, 1)
	require.Len(t, inv.registered["bans"].callbacks, 1)
	require.Len(t, inv.registered["identity"].callbacks, 1)
}

func TestInvalidator_StopWaitsForDispatchedCallbacks(t *testing.T) {
	inv := NewInvalidator(nil, slog.Default())
	started := make(chan struct{})
	finish := make(chan struct{})
	inv.dispatchCallbacks([]func(){func() {
		close(started)
		<-finish
	}})
	<-started

	stopped := make(chan struct{})
	go func() {
		inv.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned while an invalidation callback was still running")
	case <-time.After(20 * time.Millisecond):
	}

	close(finish)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the callback completed")
	}
}

func TestTTLCache_SingleflightCoalescesConcurrentFetches(t *testing.T) {
	c := NewTTLCache[string, int](time.Minute)
	var fetchCount atomic.Int32

	fetch := func(_ context.Context, _ string) (int, error) {
		fetchCount.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate slow fetch
		return 100, nil
	}

	var wg sync.WaitGroup
	results := make([]int, 50)
	for i := range 50 {
		wg.Go(func() {
			val, err := c.Get(context.Background(), "same_key", fetch)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[i] = val
		})
	}

	wg.Wait()

	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 fetch call due to singleflight, got %d", got)
	}
	for i, r := range results {
		if r != 100 {
			t.Errorf("result[%d] = %d, want 100", i, r)
		}
	}
}

func TestTTLCache_SlowKeyDoesNotBlockUnrelatedKey(t *testing.T) {
	c := NewTTLCache[string, string](time.Minute)

	// Pre-populate keyB
	c.mu.Lock()
	c.data["keyB"] = entry[string]{value: "valB", expiresAt: time.Now().Add(time.Hour)}
	c.mu.Unlock()

	slowStarted := make(chan struct{})
	slowDone := make(chan struct{})

	slowFetch := func(_ context.Context, _ string) (string, error) {
		close(slowStarted)
		<-slowDone
		return "valA", nil
	}

	// Start slow fetch on keyA in background
	go func() {
		_, _ = c.Get(context.Background(), "keyA", slowFetch)
	}()

	<-slowStarted

	// In the meantime, reading keyB must complete immediately without waiting for keyA
	readBStarted := time.Now()
	valB, err := c.Get(context.Background(), "keyB", func(_ context.Context, _ string) (string, error) {
		return "should_not_run", nil
	})
	elapsedB := time.Since(readBStarted)

	if err != nil {
		t.Fatalf("unexpected error reading keyB: %v", err)
	}
	if valB != "valB" {
		t.Errorf("got %q, want 'valB'", valB)
	}
	if elapsedB > 100*time.Millisecond {
		t.Errorf("reading keyB took %v, was blocked by slow keyA!", elapsedB)
	}

	close(slowDone)
}

func TestTTLCache_StaleOnErrorFallback(t *testing.T) {
	c := NewTTLCache[string, string](10 * time.Millisecond)

	// Insert stale entry
	c.mu.Lock()
	c.data["stale_key"] = entry[string]{value: "old_val", expiresAt: time.Now().Add(-time.Hour)}
	c.mu.Unlock()

	errFail := errors.New("backend failed")
	failingFetch := func(_ context.Context, _ string) (string, error) {
		return "", errFail
	}

	val, err := c.Get(context.Background(), "stale_key", failingFetch)
	if err != nil {
		t.Fatalf("expected stale-on-error fallback, but got error: %v", err)
	}
	if val != "old_val" {
		t.Fatalf("got %q, want fallback 'old_val'", val)
	}
}

// GetWithTTL 的 ttl<=0 回退缓存级 TTL 是多处调用方依赖的契约：领域层从 settings
// 读 TTL，读失败/未配置就返回 0，由缓存回退到构造时设定的代码默认值。若这条回退
// 失效，配置读取失败会让条目带着"某个意外 TTL"落库（甚至不过期）。
func TestGetWithTTL_ZeroTTLFallsBackToCacheLevelTTL(t *testing.T) {
	t.Parallel()
	const cacheTTL = 50 * time.Millisecond
	c := NewTTLCache[string, int](cacheTTL)

	_, err := c.GetWithTTL(t.Context(), "k", 0, func(ctx context.Context, _ string) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)

	// 写入后立即可见：ttl<=0 若未回退到缓存级 TTL 而按 0 写入，条目会立即过期。
	_, ok := c.GetIfPresent("k")
	require.True(t, ok, "ttl<=0 应回退到缓存级 TTL，写入后必须立即可见")

	// 超过缓存级 TTL 后失效（证明用的是 c.ttl 而不是不过期）。
	require.Eventually(t, func() bool {
		_, present := c.GetIfPresent("k")
		return !present
	}, time.Second, 5*time.Millisecond, "超过缓存级 TTL 应过期")
}

// 负 TTL 同样回退（settings 侧有 min 约束，但脏数据可能给负值）。
func TestGetWithTTL_NegativeTTLFallsBackToCacheLevelTTL(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)

	_, err := c.GetWithTTL(t.Context(), "k", -time.Second, func(ctx context.Context, _ string) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, c.Len(), "负 TTL 应回退缓存级 TTL 而非拒绝写入")
}

// janitor 每轮读取实时注册表：StartJanitors 之后才 EnableJanitor 的缓存也必须被清理。
// 旧实现对注册表只在启动时快照一次，晚注册者永远不被清理（静默内存泄漏）。
func TestJanitor_CoversCachesRegisteredAfterStart(t *testing.T) {
	stop := StartJanitors(10 * time.Millisecond)
	t.Cleanup(stop)

	c := NewTTLCache[string, int](time.Millisecond).EnableJanitor()
	defer c.Clear()
	c.SetWithTTL("k", 1, time.Millisecond)

	require.Eventually(t, func() bool { return c.Len() == 0 },
		time.Second, 5*time.Millisecond,
		"晚注册的缓存也必须被后台清理回收")
}

func TestDeleteFunc_RemovesMatchingKeysOnly(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	c.SetWithTTL("a:1", 1, 0)
	c.SetWithTTL("a:2", 2, 0)
	c.SetWithTTL("b:1", 3, 0)
	gen := c.Generation()
	n := c.DeleteFunc(func(k string) bool { return k[0] == 'a' })
	require.Equal(t, 2, n)
	_, ok := c.GetIfPresent("a:1")
	require.False(t, ok)
	v, ok := c.GetIfPresent("b:1")
	require.True(t, ok)
	require.Equal(t, 3, v)
	require.Greater(t, c.Generation(), gen)
}
