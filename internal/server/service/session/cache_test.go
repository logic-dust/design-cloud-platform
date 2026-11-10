package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
)

func TestSessionInfoCacheEvictExpiredHandlesNilEntry(t *testing.T) {
	t.Parallel()

	cache := newSessionInfoCache()
	cache.byToken["orphan-token"] = nil

	if got := cache.invalidateExpired(time.Now()); got != 1 {
		t.Fatalf("invalidated entries = %d, want 1", got)
	}
	if _, ok := cache.byToken["orphan-token"]; ok {
		t.Fatalf("nil token entry remained after invalidation")
	}
}

func TestSessionInfoCacheInvalidateExpired_BatchAndConcurrent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cache := newSessionInfoCache()

	// Seed > batchSize (256) expired entries plus active entries so multi-batch
	// deletion and retention of valid entries are both exercised.
	const expiredCount = 300
	const activeCount = 50
	for i := range expiredCount {
		cache.put(&model.Session{
			ID:        uint(i + 1),
			Token:     fmt.Sprintf("exp-%04d", i),
			Username:  fmt.Sprintf("user-%02d", i%10),
			ExpiresAt: now.Add(-time.Minute),
		})
	}
	for i := range activeCount {
		cache.put(&model.Session{
			ID:        uint(expiredCount + i + 1),
			Token:     fmt.Sprintf("act-%04d", i),
			Username:  fmt.Sprintf("user-%02d", i%10),
			ExpiresAt: now.Add(time.Hour),
		})
	}

	var wg sync.WaitGroup
	// Concurrent readers and renewals while invalidateExpired runs.
	for g := range 4 {
		wg.Go(func() {
			for i := range activeCount {
				tok := fmt.Sprintf("act-%04d", i)
				_, _ = cache.get(tok)
				if g == 0 {
					cache.put(&model.Session{
						ID:        uint(expiredCount + i + 1),
						Token:     tok,
						Username:  fmt.Sprintf("user-%02d", i%10),
						ExpiresAt: now.Add(2 * time.Hour),
					})
				}
			}
		})
	}

	deleted := cache.invalidateExpired(now)
	wg.Wait()

	if deleted != expiredCount {
		t.Fatalf("deleted = %d, want %d", deleted, expiredCount)
	}
	for i := range activeCount {
		if _, ok := cache.get(fmt.Sprintf("act-%04d", i)); !ok {
			t.Fatalf("active token act-%04d was unexpectedly evicted", i)
		}
	}
}

func BenchmarkSessionInfoCacheInvalidateExpired(b *testing.B) {
	now := time.Now()
	for _, size := range []int{500, 10_000, 100_000} {
		for _, expiredPct := range []int{0, 10} {
			name := fmt.Sprintf("size=%d/expired=%d%%", size, expiredPct)
			b.Run(name, func(b *testing.B) {
				cache := newSessionInfoCache()
				expiredCount := size * expiredPct / 100
				expiredEntries := make([]*model.Session, 0, expiredCount)
				for i := range size {
					exp := now.Add(time.Hour)
					if i < expiredCount {
						exp = now.Add(-time.Minute)
					}
					entry := &model.Session{
						ID:        uint(i + 1),
						Token:     fmt.Sprintf("tok-%06d", i),
						Username:  fmt.Sprintf("user-%05d", i%2000),
						ExpiresAt: exp,
					}
					cache.put(entry)
					if i < expiredCount {
						expiredEntries = append(expiredEntries, entry)
					}
				}

				b.ResetTimer()
				for b.Loop() {
					_ = cache.invalidateExpired(now)
					if expiredCount > 0 {
						b.StopTimer()
						for _, entry := range expiredEntries {
							cache.put(entry)
						}
						b.StartTimer()
					}
				}
			})
		}
	}
}
