package security

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"

	"gorm.io/gorm"
)

const (
	flushInterval                = 30 * time.Second
	authFailureUnbannedRetention = 24 * time.Hour
	authFailurePruneBatchSize    = 1000
	// flushDirtyThreshold triggers an immediate flush when this many new dirty
	// entries have accumulated, without waiting for the next timer tick.
	flushDirtyThreshold = 10
)

type entry struct {
	record  model.BanRecord
	dirty   bool
	version uint64 // incremented on every write; used to detect concurrent updates during flush
}

type BanCache struct {
	mu          sync.RWMutex
	records     map[string]*entry
	dirtyCount  int // number of entries marked dirty since last flush; guarded by mu
	db          *db.BanRepo
	logger      *slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	stopChan    chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup // tracks the flushLoop goroutine so Stop can wait for it
	flushNow    chan struct{}  // buffered(1): signals flushLoop to flush immediately
	invalidator *cache.Invalidator
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
	s.wg.Add(1)
	go s.flushLoop()
	if invalidator != nil {
		invalidator.Register("ban", func() {
			s.logger.Info("invalidating all active ban caches from broadcast")
			s.reloadFromDB()
		})
	}
	return s, nil
}

func (s *BanCache) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
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
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			func() {
				defer common.SafeRecover(s.logger, "ban-cache-flush-tick")()
				s.flush()
				if deleted := s.pruneExpired(s.ctx, time.Now()); deleted > 0 {
					s.logger.Debug("auth failure store: pruned expired records", "count", deleted)
				}
			}()
		case <-s.flushNow:
			func() {
				defer common.SafeRecover(s.logger, "ban-cache-flush-now")()
				s.flush()
			}()
		case <-s.stopChan:
			return
		}
	}
}

// shouldPruneBanRecord returns true if the record is safe to remove from memory and DB.
func shouldPruneBanRecord(record model.BanRecord, now time.Time) bool {
	if record.BannedUntil != nil {
		return now.After(*record.BannedUntil)
	}
	if record.UpdatedAt.IsZero() {
		return true
	}
	return now.Sub(record.UpdatedAt) > authFailureUnbannedRetention
}

// pruneExpired removes entries that are safe to discard (expired bans or old unbanned failures).
// It deletes from DB first; on success it removes from the in-memory map.
// At most authFailurePruneBatchSize entries are pruned per call to bound latency.
func (s *BanCache) pruneExpired(ctx context.Context, now time.Time) int {
	keys := make([]string, 0)
	s.mu.RLock()
	for key, e := range s.records {
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
		if e, ok := s.records[key]; ok && shouldPruneBanRecord(e.record, now) {
			delete(s.records, key)
			deleted++
		}
	}
	s.mu.Unlock()
	return deleted
}

func (s *BanCache) flush() {
	type snapshot struct {
		record  model.BanRecord
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

func (s *BanCache) AddBanRecord(record model.BanRecord) (model.BanRecord, error) {
	s.mu.Lock()
	e, ok := s.records[record.Key]
	if !ok {
		e = &entry{record: model.BanRecord{
			Key:       record.Key,
			IP:        record.IP,
			Username:  record.Username,
			Type:      record.Type,
			CreatedAt: time.Now(), // 首次失败/封禁时间
		}}
		s.records[record.Key] = e
	}
	e.record.Count++
	e.record.UpdatedAt = time.Now()
	e.record.IP = record.IP
	e.record.Username = record.Username
	e.record.Type = record.Type
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
	}
	return result, nil
}

func (s *BanCache) GetBanRecord(key string) (model.BanRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.records[key]
	if !ok {
		return model.BanRecord{}, false
	}
	return e.record, true
}

func (s *BanCache) List() []model.BanRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.BanRecord, 0, len(s.records))
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
			return s.invalidator.BumpTx(ctx, tx, "ban")
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
			return s.invalidator.BumpTx(ctx, tx, "ban")
		}
		return nil
	})
}

func (s *BanCache) SetBannedUntil(ctx context.Context, key string, until *time.Time) error {
	if err := s.db.SetBannedUntil(ctx, key, until, func(tx *gorm.DB) error {
		if s.invalidator != nil {
			return s.invalidator.BumpTx(ctx, tx, "ban")
		}
		return nil
	}); err != nil {
		return err
	}

	// DB 成功后更新内存：保证内存最多滞后，绝不超前产生脏数据
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.records[key]; ok {
		e.record.BannedUntil = until
		e.record.UpdatedAt = time.Now()
	} else {
		s.records[key] = &entry{record: model.BanRecord{
			Key:         key,
			BannedUntil: until,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}}
	}
	return nil
}
