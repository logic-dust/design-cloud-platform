package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCleanup verifies that expired entries are removed and fresh ones kept.
func TestCleanup(t *testing.T) {
	c := NewTTLCache[string, int](50 * time.Millisecond)
	c.mu.Lock()
	c.data["expired"] = entry[int]{value: 1, expiresAt: time.Now().Add(-time.Second)}
	c.data["fresh"] = entry[int]{value: 2, expiresAt: time.Now().Add(time.Hour)}
	c.mu.Unlock()

	c.Cleanup()

	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.data["expired"]; ok {
		t.Error("expired entry was not cleaned up")
	}
	if _, ok := c.data["fresh"]; ok {
		// fresh entry must survive
	} else {
		t.Error("fresh entry was wrongly removed")
	}
}

// TestStartJanitor verifies the global janitor removes expired entries after ticking.
func TestStartJanitor(t *testing.T) {
	c := NewTTLCache[string, int](10 * time.Millisecond).EnableJanitor()
	stop := StartJanitors(20 * time.Millisecond)
	t.Cleanup(stop)

	c.mu.Lock()
	c.data["stale"] = entry[int]{value: 1, expiresAt: time.Now().Add(-time.Second)}
	c.mu.Unlock()

	// Wait for at least one janitor tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		_, ok := c.data["stale"]
		c.mu.RUnlock()
		if !ok {
			return // cleaned up
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("janitor did not clean up expired entry within deadline")
}

// TestConcurrentCleanupAndGet ensures Cleanup does not race with Get.
func TestConcurrentCleanupAndGet(t *testing.T) {
	c := NewTTLCache[string, int](50 * time.Millisecond)
	fetch := func(_ context.Context, _ string) (int, error) { return 42, nil }

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				_, _ = c.Get(context.TODO(), "k", fetch)
			}
		})
		wg.Go(func() {
			for range 100 {
				c.Cleanup()
			}
		})
	}
	wg.Wait()
}
