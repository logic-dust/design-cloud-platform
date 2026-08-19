package cache

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"design-cloud-engine/internal/db"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// ============================================================================
// 1. TTLCache - 泛型内存 TTL 缓存
// ============================================================================

// TTLCache is a generic in-memory cache with TTL-based expiry, singleflight
// request coalescing to prevent concurrent fetches of the same key without
// blocking unrelated keys, and stale-on-error fallback (returns expired cache
// entry instead of an error).
//
// 可选容量上限（SetMaxEntries）：写入路径自生效，采用采样淘汰（思路同 Redis
// volatile-ttl），调用方无需预留。
type TTLCache[K comparable, V any] struct {
	mu         sync.RWMutex
	data       map[K]entry[V]
	ttl        time.Duration
	maxEntries int // 0 = 不限制
	sf         singleflight.Group

	// gen 在 Invalidate/Clear 时递增（与数据变更在同一把锁内），用于丢弃"失效之前
	// 发起、失效之后才落地"的在途回填。失效只删"当时已存在"的条目，拦不住在途 fetch：
	// 没有这道门槛，旧值会在失效之后被写回并长期驻留（TTL 可能长达 24h），失效形同虚设。
	// 两种使用方式：GetWithTTL 内部自动校验；批量回填用 Generation + SetIfGeneration。
	//
	// 代次是**整份缓存**一份，不是 per-key：Invalidate(A) 也会放弃 B 的在途回填。
	// 这是过度失效（多一次回源），不是错填；为单键维护纪元表不划算。
	gen atomic.Uint64
}

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// NewTTLCache creates a cache with the given TTL. ttl must be > 0.
func NewTTLCache[K comparable, V any](ttl time.Duration) *TTLCache[K, V] {
	return &TTLCache[K, V]{
		data: make(map[K]entry[V]),
		ttl:  ttl,
	}
}

// JanitorInterval 是 per-user 等易积累缓存的默认后台清理周期。
// 惰性检查只清理被再次访问的 key，无人访问的过期 key 靠 janitor 兜底回收。
// 10 分钟一次：过期 entry 残留最坏 10 分钟，数百字节级，无感知。
const JanitorInterval = 10 * time.Minute

// ---- 全局 janitor 注册表 ----
// 需要后台过期清理的 cache 通过 EnableJanitor 注册，Server.Start 时统一
// StartJanitors 启动一个全局清理协程。调用处只需决定"是否启用清理"，
// 机制（周期、遍历、删除）全部由本包实现。
//
// 判断依据：**键数会随外部输入增长**（per-user / per-query）的缓存需要注册；
// 键数是固定小集合（如各类"全局单份"配置缓存）不必注册——过期项残留几个
// entry 无感知。漏注册的代价是内存随键增长而单调上涨，因此新加 per-key 缓存时
// 必须一并注册（已有 `cache_cleanup_test.go` 验证清理生效）。

type janitorEntry interface {
	Cleanup()
}

var (
	janitorMu   sync.Mutex
	janitorList []janitorEntry
)

// EnableJanitor 注册本 cache 到全局后台清理。返回自身便于链式调用。
func (c *TTLCache[K, V]) EnableJanitor() *TTLCache[K, V] {
	janitorMu.Lock()
	defer janitorMu.Unlock()
	janitorList = append(janitorList, c)
	return c
}

// snapshotJanitors 返回当前注册表的副本（每轮清理前调用，以包含后来注册的 cache）。
func snapshotJanitors() []janitorEntry {
	janitorMu.Lock()
	defer janitorMu.Unlock()
	return slices.Clone(janitorList)
}

// StartJanitors 启动全局过期清理协程，周期性清理所有已注册的 cache。
// 生产路径在服务启动时调用一次，返回的 stop 可忽略（进程退出即回收）。
// 测试必须调用 stop，否则短周期扫描会泄漏到同包后续用例。
//
// 重复调用只会多一个扫描协程（每次扫描是幂等的删过期项），没有做"只启一次"
// 守卫——那会把 interval 钉死在首次调用上，反而让需要不同周期的测试相互干扰。
func StartJanitors(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	stop = func() { once.Do(func() { close(done) }) }

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// 每轮取实时快照：StartJanitors 之后才 EnableJanitor 的 cache 也被覆盖
				// （旧实现只在启动时快照一次，晚注册者永远不被清理——静默内存泄漏）。
				for _, c := range snapshotJanitors() {
					c.Cleanup()
				}
			}
		}
	}()
	return stop
}

// GetIfPresent returns the cached value when present and not expired.
// It does not trigger a fetch.
func (c *TTLCache[K, V]) GetIfPresent(key K) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.data[key]; ok && time.Now().Before(e.expiresAt) {
		return e.value, true
	}
	return zero, false
}

// GetStale returns a cached value even if expired. Used for stale-on-error fallback
// when batch fetches fail outside GetWithTTL.
func (c *TTLCache[K, V]) GetStale(key K) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.data[key]; ok {
		return e.value, true
	}
	return zero, false
}

// SetWithTTL stores value under key with the given TTL. ttl <= 0 falls back to c.ttl.
func (c *TTLCache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	if c == nil {
		return
	}
	if ttl <= 0 {
		ttl = c.ttl
	}
	c.mu.Lock()
	c.setLocked(key, value, ttl)
	c.mu.Unlock()
}

// setLocked 写入一条并（必要时）执行容量裁剪；调用方必须持写锁。
//
// 只有**新增 key** 才需要腾槽位：覆盖已有 key 不增加条目数，若也走淘汰，续期/改值
// 会把无关的存活条目一起裁掉——那是静默丢工作集。
func (c *TTLCache[K, V]) setLocked(key K, value V, ttl time.Duration) {
	if _, exists := c.data[key]; !exists && c.maxEntries > 0 && len(c.data) >= c.maxEntries {
		c.evictLocked()
	}
	c.data[key] = entry[V]{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
}

// evictSampleSize 是每次淘汰检查的候选数（语义同 Redis 的 maxmemory-samples）。
// Go 的 map 迭代起点随机，取前 N 个即随机样本，无需额外随机源。
const evictSampleSize = 5

// evictLocked 在已达上限时腾出一个槽位；调用方必须持写锁。
//
// 采样淘汰（思路同 Redis volatile-ttl）：在随机样本内挑最早过期者删除，复杂度 O(k)、
// 零分配。取代了早先的"全量扫描 + 排序 + 批量删除"——后者在 10 万条上限时单次要
// 17ms 且全程独占写锁、分配数 MB（对比见 cache_evict_bench_test.go）。
//
// 代价：淘汰由"全局最早过期"变为近似（样本内的最早者）。样本里若有过期项会被优先
// 回收，故仍保有"先回收过期"的倾向。
func (c *TTLCache[K, V]) evictLocked() {
	now := time.Now()
	var (
		victim    K
		victimExp time.Time
		found     bool
		sampled   int
	)
	for k, e := range c.data {
		if now.After(e.expiresAt) {
			delete(c.data, k) // 过期项：无条件回收，优于淘汰存活项
			return
		}
		if !found || e.expiresAt.Before(victimExp) {
			victim, victimExp, found = k, e.expiresAt, true
		}
		sampled++
		if sampled >= evictSampleSize {
			break
		}
	}
	if found {
		delete(c.data, victim)
	}
}

// flightKey 把 cache key 与代次编进同一条 singleflight 键，使 Clear/Invalidate
// 之后的新 Get 不会加入清空前的 in-flight fetch。
func flightKey[K comparable](key K, gen uint64) string {
	return fmt.Sprintf("%v\x00%d", key, gen)
}

// Get returns the cached value for key if it exists and has not expired.
// Otherwise it calls fetch (coalesced via singleflight), caches the result,
// and returns it.
func (c *TTLCache[K, V]) Get(ctx context.Context, key K, fetch func(context.Context, K) (V, error)) (V, error) {
	return c.GetWithTTL(ctx, key, c.ttl, fetch)
}

// GetWithTTL is like Get but uses a per-call TTL instead of the cache-level
// default. 每次调用可指定 TTL：适用于 TTL 由外部配置动态决定（如 scheduler
// jobCache 跟随 settings.JobCacheTTL 热更新）的场景。缓存级别 ttl 仍作为
// 默认值；传入 ttl <= 0 时回退 c.ttl。
func (c *TTLCache[K, V]) GetWithTTL(ctx context.Context, key K, ttl time.Duration, fetch func(context.Context, K) (V, error)) (V, error) {
	if ttl <= 0 {
		ttl = c.ttl
	}
	// Fast path: read lock check.
	c.mu.RLock()
	if e, ok := c.data[key]; ok && time.Now().Before(e.expiresAt) {
		c.mu.RUnlock()
		return e.value, nil
	}
	c.mu.RUnlock()

	// Slow path: SingleFlight coalesces concurrent fetches for the same key
	// without holding the global cache read/write mutex during fetch execution.
	//
	// flight key 带上代次：Clear/Invalidate 之后的新调用不会加入清空前的 in-flight
	// fetch（否则本次响应会拿到旧值——gen 守卫只拦回填，拦不住把结果返回给后到的调用者）。
	genAtStart := c.gen.Load()
	valAny, err, _ := c.sf.Do(flightKey(key, genAtStart), func() (any, error) {
		// Double-check under read lock in case another singleflight runner just populated it
		c.mu.RLock()
		if e, ok := c.data[key]; ok && time.Now().Before(e.expiresAt) {
			c.mu.RUnlock()
			return e.value, nil
		}
		c.mu.RUnlock()

		val, fetchErr := fetch(ctx, key)
		if fetchErr != nil {
			// Stale-on-error fallback: return stale cached value if present
			c.mu.RLock()
			if e, ok := c.data[key]; ok {
				c.mu.RUnlock()
				return e.value, nil
			}
			c.mu.RUnlock()
			return nil, fetchErr
		}

		// 发起到现在发生过失效 → 取到的可能是失效前的旧值，丢弃回填（仍把结果返回给
		// 本次调用者，它本就在与写入赛跑）。
		c.setLockedIfGeneration(key, val, ttl, genAtStart)
		return val, nil
	})

	if err != nil {
		var zero V
		return zero, err
	}
	return valAny.(V), nil
}

// Invalidate removes a specific key from the cache immediately.
// 同时递增整份缓存的代次：发起于本次失效之前的在途回填（含其它 key）都不会被写回。
func (c *TTLCache[K, V]) Invalidate(key K) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.gen.Add(1)
	delete(c.data, key)
	c.mu.Unlock()
	// 新 Get 的 flight key 带新代次，不会加入旧 in-flight；无需 Forget。
}

// Generation 返回当前代次（语义见 gen 字段）。
//
// 批量回填的用法：在**查询开始前**记录代次，每笔写入用 SetIfGeneration 校验。
// 这样"失效前读到源数据、失效后才写入"的条目会被丢弃，不会让失效形同虚设。
func (c *TTLCache[K, V]) Generation() uint64 {
	if c == nil {
		return 0
	}
	return c.gen.Load()
}

// SetIfGeneration 仅在代次仍等于 gen 时写入，返回是否写入。
// 代次校验与写入在同一把锁内，因此不存在"校验通过后立即被失效"的漏检窗口。
func (c *TTLCache[K, V]) SetIfGeneration(gen uint64, key K, value V, ttl time.Duration) bool {
	if c == nil {
		return false
	}
	if ttl <= 0 {
		ttl = c.ttl
	}
	return c.setLockedIfGeneration(key, value, ttl, gen)
}

// setLockedIfGeneration 是代次守卫下的写入原语（GetWithTTL 回填与 SetIfGeneration
// 共用）：代次不一致则放弃写入。
func (c *TTLCache[K, V]) setLockedIfGeneration(key K, value V, ttl time.Duration, gen uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen.Load() != gen {
		return false
	}
	c.setLocked(key, value, ttl)
	return true
}

// Clear removes all entries from the cache.
// 同时递增代次：在途回填不会被写回；GetWithTTL 的 flight key 含代次，清空后的
// 新调用也不会加入清空前的 in-flight fetch。
func (c *TTLCache[K, V]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen.Add(1)
	c.data = make(map[K]entry[V])
}

// Cleanup removes all expired entries. 惰性检查（Get 时单 key 过期即删）无法回收
// 不再被访问的过期 key，因此需要后台周期清理，防止 per-user 缓存无限积累。
func (c *TTLCache[K, V]) Cleanup() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.data {
		if now.After(e.expiresAt) {
			delete(c.data, k)
		}
	}
}

// SetMaxEntries bounds the cache size. 0 (default) means unlimited.
// 自生效：写入路径（SetWithTTL / GetWithTTL 回填）在新增 key 且达上限时自动采样淘汰，
// 调用方无需任何配合。
func (c *TTLCache[K, V]) SetMaxEntries(n int) *TTLCache[K, V] {
	if c == nil {
		return c
	}
	c.mu.Lock()
	c.maxEntries = n
	c.mu.Unlock()
	return c
}

// Len returns the current number of entries, including expired-but-not-yet-reclaimed ones.
func (c *TTLCache[K, V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// ============================================================================
// 2. Invalidator - 多节点缓存失效广播协调器
// ============================================================================

// InvalidatorPollInterval 是 cache_versions 的统一轮询节拍：全表一次查询即可覆盖
// 所有主题，因此不再需要"每主题一个轮询 goroutine + 每主题一次查询"。
const InvalidatorPollInterval = 15 * time.Second

type registration struct {
	// callbacks 允许同一主题多个回调共存（如 settings 域的自身缓存与 Security 的派生缓存）。
	callbacks []func()
}

// Invalidator 负责多节点间的缓存失效广播协调。
//
// 单 goroutine 按 InvalidatorPollInterval 拉取整张 cache_versions 表，发现某主题
// 版本递增即触发其回调（在独立 goroutine 中执行，慢回调不拖慢节拍）。
// 加新主题零边际成本。
type Invalidator struct {
	store  *db.CacheVersionRepo
	logger *slog.Logger

	mu         sync.RWMutex
	versions   map[string]int64
	registered map[string]*registration
	cancel     context.CancelFunc
}

// NewInvalidator 创建一个新的缓存失效协调器
func NewInvalidator(store *db.CacheVersionRepo, logger *slog.Logger) *Invalidator {
	return &Invalidator{
		store:      store,
		logger:     logger.With("component", "cache_invalidator"),
		versions:   make(map[string]int64),
		registered: make(map[string]*registration),
	}
}

// Register 注册指定缓存主题的回调；同一主题可多次注册，回调按注册顺序累加执行。
func (i *Invalidator) Register(name string, cb func()) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if reg, ok := i.registered[name]; ok {
		reg.callbacks = append(reg.callbacks, cb)
		return
	}
	i.registered[name] = &registration{callbacks: []func(){cb}}
}

// BumpTx 在指定的数据库事务内原子自增缓存主题版本号。
func (i *Invalidator) BumpTx(ctx context.Context, tx *gorm.DB, name string) error {
	return i.store.BumpTx(ctx, tx, name)
}

// Start 启动共享轮询。isSingleNode 为 true 时跳过轮询。
func (i *Invalidator) Start(ctx context.Context, isSingleNode bool) {
	if isSingleNode {
		i.logger.Info("running in single-node mode, disabling cache invalidator polling")
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	i.cancel = cancel

	i.mu.RLock()
	topics := len(i.registered)
	i.mu.RUnlock()
	if topics == 0 {
		i.logger.Info("no cache topics registered, invalidator polling not started")
		return
	}

	go i.pollLoop(ctx)
	i.logger.Info("started shared cache invalidator polling",
		"topics", topics, "interval", InvalidatorPollInterval)
}

// Stop 停止轮询
func (i *Invalidator) Stop() {
	if i.cancel != nil {
		i.cancel()
	}
}

func (i *Invalidator) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(InvalidatorPollInterval)
	defer ticker.Stop()

	i.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			i.pollOnce(ctx)
		}
	}
}

// pollOnce 拉取一次全表版本号并分发失效。
// 首次见到某主题只建立基线（不触发回调），之后版本严格递增才触发。
func (i *Invalidator) pollOnce(ctx context.Context) {
	all, err := i.store.GetAll(ctx)
	if err != nil {
		i.logger.Warn("failed to fetch cache versions", "err", err)
		return
	}

	type dispatch struct {
		name      string
		oldVer    int64
		newVer    int64
		callbacks []func()
	}

	i.mu.Lock()
	var pending []dispatch
	for name, ver := range all {
		reg, ok := i.registered[name]
		if !ok {
			continue
		}
		lastVer, seen := i.versions[name]
		i.versions[name] = ver
		if !seen || ver <= lastVer {
			continue
		}
		// 拷贝回调切片：回调在独立 goroutine 中执行，避免与后续 Register 竞争
		pending = append(pending, dispatch{
			name: name, oldVer: lastVer, newVer: ver, callbacks: slices.Clone(reg.callbacks),
		})
	}
	i.mu.Unlock()

	for _, d := range pending {
		i.logger.Info("cache invalidation triggered by version bump",
			"name", d.name, "old_version", d.oldVer, "new_version", d.newVer)
		callbacks := d.callbacks
		go func() {
			for _, cb := range callbacks {
				if cb != nil {
					cb()
				}
			}
		}()
	}
}
