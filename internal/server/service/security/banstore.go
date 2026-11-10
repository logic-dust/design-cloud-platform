package security

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/concurrency"
	"design-cloud-engine/internal/server/cache"

	"gorm.io/gorm"
)

const (
	flushInterval                = 30 * time.Second
	pruneInterval                = 15 * time.Minute
	authFailureUnbannedRetention = 24 * time.Hour
	authFailurePruneBatchSize    = 1000
	// flushDirtyThreshold triggers an immediate flush after this many new dirty
	// writes, without waiting for the next timer tick. The same key counts each time.
	flushDirtyThreshold = 10
	// maxBanRecords 限制内存中的失败计数记录总数（key 为 IP 或 IP:username，攻击者可构造大量新 key）。
	// 约 300–500 B/条，上限约 50 MB。达到上限后只拒绝新增“未封禁的失败计数”记录；
	// 已有记录照常更新，封禁记录（ApplyBan）不受限制且永不因容量被淘汰。
	maxBanRecords = 100_000
	// banCapacityWarnInterval 是容量告警日志的最小间隔。
	banCapacityWarnInterval = time.Minute
)

type entry struct {
	record  model.Ban
	dirty   bool
	version uint64 // incremented on every write; used to detect concurrent updates during flush
}

type BanCache struct {
	mu           sync.RWMutex
	records      map[string]*entry
	dirtyCount   int // number of entries marked dirty since last flush; guarded by mu
	db           *db.BanRepo
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	stopChan     chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup // tracks background goroutines so Stop can wait for them
	flushNow     chan struct{}  // buffered(1): signals flushLoop to flush immediately
	flushTimerMu sync.Mutex
	flushTimer   *time.Timer // delayed flush when dirty; nil when idle or after Stop
	invalidator  *cache.Invalidator
	maxRecords   int       // 0 = maxBanRecords；测试可调小
	lastCapWarn  time.Time // guarded by mu
	// flushDelay: when > 0, override flushInterval (tests).
	flushDelay time.Duration
}

func NewBanCache(banRepo *db.BanRepo, logger *slog.Logger, invalidator *cache.Invalidator) (*BanCache, error) {
	if banRepo == nil {
		return nil, fmt.Errorf("auth failure store: ban repo is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("auth failure store: logger is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &BanCache{
		records:     make(map[string]*entry),
		db:          banRepo,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
		stopChan:    make(chan struct{}),
		flushNow:    make(chan struct{}, 1),
		invalidator: invalidator,
	}
	s.loadFromDB()
	s.wg.Add(2)
	go s.flushLoop()
	go s.pruneLoop()
	if invalidator != nil {
		invalidator.Register(db.CacheVersionTopicBans, func() {
			s.logger.Info("invalidating all active ban caches from broadcast")
			s.reloadFromDB()
		})
	}
	return s, nil
}

func (s *BanCache) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.cancelDelayedFlush()
		close(s.stopChan)
	})
	s.flush()
	// Wait for flushLoop to exit so no goroutine leaks and the final flush is
	// guaranteed complete before Stop returns.
	s.wg.Wait()
}

func (s *BanCache) reloadFromDB() {
	s.flush() // flush any dirty entries to DB before discarding local state
	s.mu.Lock()
	s.records = make(map[string]*entry)
	s.dirtyCount = 0
	s.mu.Unlock()
	s.loadFromDB()
}

func (s *BanCache) loadFromDB() {
	records, err := s.db.List(context.Background())
	if err != nil {
		s.logger.Warn("auth failure store: failed to load records from DB", "error", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range records {
		s.records[r.Key] = &entry{record: r}
	}
	s.logger.Info("auth failure store: loaded records from DB", "count", len(s.records))
}

func (s *BanCache) flushLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.flushNow:
			func() {
				defer concurrency.SafeRecover(s.logger, "ban-cache-flush-now")()
				s.runFlushCycle()
			}()
		case <-s.stopChan:
			return
		}
	}
}

func (s *BanCache) pruneLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			func() {
				defer concurrency.SafeRecover(s.logger, "ban-cache-prune-tick")()
				if deleted := s.pruneExpired(s.ctx, time.Now()); deleted > 0 {
					s.logger.Debug("auth failure store: pruned expired records", "count", deleted)
				}
			}()
		case <-s.stopChan:
			return
		}
	}
}

func (s *BanCache) flushDelayDuration() time.Duration {
	if s.flushDelay > 0 {
		return s.flushDelay
	}
	return flushInterval
}

func (s *BanCache) hasDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.records {
		if e.dirty {
			return true
		}
	}
	return false
}

// scheduleDelayedFlush arms the delayed flush timer unless one is already pending.
func (s *BanCache) scheduleDelayedFlush() {
	s.flushTimerMu.Lock()
	defer s.flushTimerMu.Unlock()
	s.scheduleDelayedFlushLocked()
}

// scheduleDelayedFlushLocked must be called with flushTimerMu held.
func (s *BanCache) scheduleDelayedFlushLocked() {
	if s.flushTimer != nil {
		return
	}
	s.flushTimer = time.AfterFunc(s.flushDelayDuration(), func() {
		s.flushTimerMu.Lock()
		s.flushTimer = nil
		s.flushTimerMu.Unlock()
		select {
		case s.flushNow <- struct{}{}:
		default:
		}
	})
}

func (s *BanCache) cancelDelayedFlush() {
	s.flushTimerMu.Lock()
	defer s.flushTimerMu.Unlock()
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
}

// settleDelayedFlush reconciles the delayed flush timer with the dirty state after
// a flush cycle. The dirty check and the timer mutation must share flushTimerMu:
// otherwise AddBanRecord could mark an entry dirty and arm a timer between the
// check and the cancellation, leaving the entry dirty with no flush scheduled.
// Lock order: flushTimerMu -> mu (AddBanRecord drops mu before arming).
func (s *BanCache) settleDelayedFlush() {
	s.flushTimerMu.Lock()
	defer s.flushTimerMu.Unlock()
	if s.hasDirty() {
		s.scheduleDelayedFlushLocked()
		return
	}
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
}

func (s *BanCache) runFlushCycle() {
	s.flush()
	if deleted := s.pruneExpired(s.ctx, time.Now()); deleted > 0 {
		s.logger.Debug("auth failure store: pruned expired records on flush", "count", deleted)
	}
	s.settleDelayedFlush()
}

// shouldPruneBanRecord returns true if the record is safe to remove from memory and DB.
func shouldPruneBanRecord(record model.Ban, now time.Time) bool {
	if record.BannedUntil != nil {
		return now.After(*record.BannedUntil)
	}
	if record.LastFailedAt.IsZero() {
		return true
	}
	return now.Sub(record.LastFailedAt) > authFailureUnbannedRetention
}

// pruneExpired removes entries that are safe to discard (expired bans or old unbanned failures).
// It deletes from DB first; on success it removes from the in-memory map.
// Dirty entries are skipped: an expired ban that just received another failure is
// still pending its write-back, and deleting it would silently drop that count.
// At most authFailurePruneBatchSize entries are pruned per call to bound latency.
func (s *BanCache) pruneExpired(ctx context.Context, now time.Time) int {
	keys := make([]string, 0)
	s.mu.RLock()
	for key, e := range s.records {
		if e.dirty {
			continue
		}
		if shouldPruneBanRecord(e.record, now) {
			keys = append(keys, key)
			if len(keys) >= authFailurePruneBatchSize {
				break
			}
		}
	}
	s.mu.RUnlock()

	if len(keys) == 0 {
		return 0
	}

	if err := s.db.DeleteByKeys(ctx, keys); err != nil {
		s.logger.Warn("auth failure store: prune DB delete failed", "count", len(keys), "error", err)
		return 0
	}

	deleted := 0
	s.mu.Lock()
	for _, key := range keys {
		if e, ok := s.records[key]; ok && !e.dirty && shouldPruneBanRecord(e.record, now) {
			delete(s.records, key)
			deleted++
		}
	}
	s.mu.Unlock()
	return deleted
}

func (s *BanCache) flush() {
	type snapshot struct {
		record  model.Ban
		version uint64
	}

	s.mu.Lock()
	toFlush := make([]snapshot, 0, len(s.records))
	for _, e := range s.records {
		if e.dirty {
			toFlush = append(toFlush, snapshot{record: e.record, version: e.version})
		}
	}
	s.dirtyCount = 0
	s.mu.Unlock()

	if len(toFlush) == 0 {
		return
	}

	// Use a bounded timeout so that a slow or unavailable DB does not block
	// the flush goroutine indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, snap := range toFlush {
		if err := s.db.Upsert(ctx, snap.record); err != nil {
			s.logger.Warn("auth failure store: flush failed", "key", snap.record.Key, "error", err)
			// Keep dirty flag so the next flush cycle retries this entry.
			continue
		}
		// Clear dirty only if the record has not been updated since the snapshot
		// was taken, to avoid silently discarding a concurrent write.
		s.mu.Lock()
		if e, ok := s.records[snap.record.Key]; ok && e.version == snap.version {
			e.dirty = false
		}
		s.mu.Unlock()
	}
}

func (s *BanCache) AddBanRecord(record model.Ban) (model.Ban, error) {
	s.mu.Lock()
	e, ok := s.records[record.Key]
	if !ok && len(s.records) >= s.recordLimit() {
		now := time.Now()
		warn := now.Sub(s.lastCapWarn) >= banCapacityWarnInterval
		if warn {
			s.lastCapWarn = now
		}
		s.mu.Unlock()
		if warn {
			s.logger.Warn("auth failure store: record capacity reached, dropping new failure record",
				"limit", s.recordLimit(), "key", record.Key)
		}
		record.Count = 1
		return record, nil
	}
	if !ok {
		e = &entry{record: model.Ban{
			Key:           record.Key,
			IP:            record.IP,
			Username:      record.Username,
			Subject:       record.Subject,
			Source:        record.Source,
			FirstFailedAt: time.Now(), // 首次失败/封禁时间
		}}
		s.records[record.Key] = e
	} else if e.record.BannedUntil != nil && !e.record.BannedUntil.After(time.Now()) {
		// 上一个封禁窗口已经结束：从本窗口重新计数。否则旧 Count 会让解封后的
		// 第一次失败立即再次触发封禁（15m 的 prune 间隔会放大这个窗口）。
		e.record.Count = 0
		e.record.BannedUntil = nil
		e.record.Reason = ""
		e.record.FirstFailedAt = time.Now()
	}
	e.record.Count++
	e.record.LastFailedAt = time.Now()
	e.record.IP = record.IP
	e.record.Username = record.Username
	e.record.Subject = record.Subject
	e.record.Source = record.Source
	e.dirty = true
	e.version++
	s.dirtyCount++
	shouldFlush := s.dirtyCount >= flushDirtyThreshold
	result := e.record
	s.mu.Unlock()

	if shouldFlush {
		// Non-blocking send: if flushNow already has a pending signal, skip.
		select {
		case s.flushNow <- struct{}{}:
		default:
		}
	} else {
		s.scheduleDelayedFlush()
	}
	return result, nil
}

func (s *BanCache) recordLimit() int {
	if s.maxRecords > 0 {
		return s.maxRecords
	}
	return maxBanRecords
}

func (s *BanCache) GetBanRecord(key string) (model.Ban, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.records[key]
	if !ok {
		return model.Ban{}, false
	}
	return e.record, true
}

func (s *BanCache) List() []model.Ban {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Ban, 0, len(s.records))
	for _, e := range s.records {
		out = append(out, e.record)
	}
	return out
}

func (s *BanCache) DeleteByKey(ctx context.Context, key string) error {
	s.mu.Lock()
	delete(s.records, key)
	s.mu.Unlock()
	return s.db.DeleteByKey(ctx, key, func(tx *gorm.DB) error {
		if s.invalidator != nil {
			return s.invalidator.BumpTx(ctx, tx, db.CacheVersionTopicBans)
		}
		return nil
	})
}

func (s *BanCache) DeleteAll(ctx context.Context) error {
	s.mu.Lock()
	s.records = make(map[string]*entry)
	s.dirtyCount = 0
	s.mu.Unlock()
	return s.db.DeleteAll(ctx, func(tx *gorm.DB) error {
		if s.invalidator != nil {
			return s.invalidator.BumpTx(ctx, tx, db.CacheVersionTopicBans)
		}
		return nil
	})
}

// ApplyBan upserts a complete ban record (expiry + reason + metadata) without
// touching Count. Used by both auto-triggered and manual bans. DB first,
// memory after, so memory never leads the DB.
func (s *BanCache) ApplyBan(ctx context.Context, record model.Ban) error {
	record.LastFailedAt = time.Now()
	if record.FirstFailedAt.IsZero() {
		record.FirstFailedAt = record.LastFailedAt
	}
	if err := s.db.UpsertFull(ctx, record, func(tx *gorm.DB) error {
		if s.invalidator != nil {
			return s.invalidator.BumpTx(ctx, tx, db.CacheVersionTopicBans)
		}
		return nil
	}); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.records[record.Key]; ok {
		e.record.Subject = record.Subject
		e.record.Source = record.Source
		e.record.IP = record.IP
		e.record.Username = record.Username
		e.record.BannedUntil = record.BannedUntil
		e.record.Reason = record.Reason
		e.record.LastFailedAt = record.LastFailedAt
		e.version++
	} else {
		s.records[record.Key] = &entry{record: record}
	}
	return nil
}
