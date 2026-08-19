package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
