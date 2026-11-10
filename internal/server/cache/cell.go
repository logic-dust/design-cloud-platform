package cache

import (
	"context"
	"sync"
	"time"
)

// Cell 是单值缓存原语：一个数据块 + 采样时间戳 + 双锁。
// 适用于"聚合读模型"场景（如 overview 各数据块）：数据块以 TTL 过期，
// 首次访问同步刷新，后续过期触发后台刷新（返回旧值）。区别于 TTLCache
// （多 key 缓存），Cell 管理单一数据块并暴露 sampledAt 供变更检测。
//
// 统一处理链路：所有"单块 + TTL + 采样指纹"的缓存都应使用 Cell，
// 而非各自实现锁逻辑。GetOrRefresh 承担过期判断/并发刷新去重，
// 刷新函数只负责取数写回（Set 内部完成）。
// backgroundRefreshTimeout 限制后台刷新的最长执行时间：后台刷新脱离请求生命周期，
// 若下游卡死，超时可保证 goroutine 退出并释放 update 锁，后续请求得以重新触发刷新。
const backgroundRefreshTimeout = 30 * time.Second

type Cell[T any] struct {
	mu        sync.RWMutex
	update    sync.Mutex
	data      T
	sampledAt time.Time
}

// GetOrRefresh returns cached data if fresh, otherwise triggers a refresh.
// First call blocks until refresh completes; subsequent stale calls trigger a
// background refresh while returning the existing (slightly stale) value.
func (c *Cell[T]) GetOrRefresh(ctx context.Context, ttl time.Duration, refresh func(context.Context)) T {
	// 一次加锁取快照：sampledAt 由 Set 在写锁内更新，锁外再读它会与 refresh 的写回
	// 构成数据竞争（-race 可复现）。判过期与判"从未采样"基于同一份快照。
	c.mu.RLock()
	sampledAt := c.sampledAt
	c.mu.RUnlock()
	isStale := time.Since(sampledAt) > ttl

	if isStale {
		if sampledAt.IsZero() {
			c.update.Lock()
			// Double check under lock to avoid duplicate initial refreshes
			c.mu.RLock()
			fresh := !c.sampledAt.IsZero() && time.Since(c.sampledAt) <= ttl
			c.mu.RUnlock()
			if !fresh {
				refresh(ctx)
			}
			c.update.Unlock()
		} else if c.update.TryLock() {
			// 脱离请求取消（请求结束不应中断后台刷新），但保留 ctx 携带的值并加超时上限。
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundRefreshTimeout)
			go func() {
				defer c.update.Unlock()
				defer cancel()
				c.mu.RLock()
				fresh := time.Since(c.sampledAt) <= ttl
				c.mu.RUnlock()
				if !fresh {
					refresh(bgCtx)
				}
			}()
		}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}

// Set 写入数据并更新采样时间。refresh 函数内部通过 Set 回填结果。
func (c *Cell[T]) Set(data T, sampledAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = data
	c.sampledAt = sampledAt
}

// SampledAt 返回当前采样时间（零值=从未刷新）。SSE 指纹/变更检测用。
func (c *Cell[T]) SampledAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sampledAt
}

// IsFresh 判断缓存是否在 TTL 内（未过期）。
func (c *Cell[T]) IsFresh(ttl time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.sampledAt) <= ttl
}
