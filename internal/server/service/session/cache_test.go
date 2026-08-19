package session

import (
	"testing"
	"time"
)

func TestSessionInfoCacheEvictExpiredHandlesNilEntry(t *testing.T) {
	t.Parallel()

	cache := newSessionInfoCache()
	cache.byToken["orphan-token"] = nil

	if got := cache.evictExpired(time.Now()); got != 1 {
		t.Fatalf("evicted entries = %d, want 1", got)
	}
	if _, ok := cache.byToken["orphan-token"]; ok {
		t.Fatalf("nil token entry remained after eviction")
	}
}
