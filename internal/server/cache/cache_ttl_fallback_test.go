package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// GetWithTTL 的 ttl<=0 回退缓存级 TTL 是多处调用方依赖的契约：领域层从 settings
// 读 TTL，读失败/未配置就返回 0，由缓存回退到构造时设定的代码默认值。若这条回退
// 失效，配置读取失败会让条目带着"某个意外 TTL"落库（甚至不过期）。
func TestGetWithTTL_ZeroTTLFallsBackToCacheLevelTTL(t *testing.T) {
	t.Parallel()
	const cacheTTL = 30 * time.Millisecond
	c := NewTTLCache[string, int](cacheTTL)

	_, err := c.GetWithTTL(t.Context(), "k", 0, func(ctx context.Context, _ string) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	_, ok := c.GetIfPresent("k")
	require.True(t, ok)

	// 未过期前命中
	time.Sleep(cacheTTL / 3)
	_, ok = c.GetIfPresent("k")
	require.True(t, ok, "缓存级 TTL 未到时仍应命中")

	// 超过缓存级 TTL 后失效（证明用的是 c.ttl 而不是不过期）
	time.Sleep(cacheTTL + 10*time.Millisecond)
	_, ok = c.GetIfPresent("k")
	require.False(t, ok, "超过缓存级 TTL 应过期")
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
