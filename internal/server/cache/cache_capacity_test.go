package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
