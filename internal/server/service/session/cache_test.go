package session

import (
	"testing"
	"time"
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
