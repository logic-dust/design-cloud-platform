package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache_SingleflightCoalescesConcurrentFetches(t *testing.T) {
	c := NewTTLCache[string, int](time.Minute)
	var fetchCount atomic.Int32

	fetch := func(_ context.Context, _ string) (int, error) {
		fetchCount.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate slow fetch
		return 100, nil
	}

	var wg sync.WaitGroup
	results := make([]int, 50)
	for i := range 50 {
		wg.Go(func() {
			val, err := c.Get(context.Background(), "same_key", fetch)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[i] = val
		})
	}

	wg.Wait()

	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 fetch call due to singleflight, got %d", got)
	}
	for i, r := range results {
		if r != 100 {
			t.Errorf("result[%d] = %d, want 100", i, r)
		}
	}
}

func TestTTLCache_SlowKeyDoesNotBlockUnrelatedKey(t *testing.T) {
	c := NewTTLCache[string, string](time.Minute)

	// Pre-populate keyB
	c.mu.Lock()
	c.data["keyB"] = entry[string]{value: "valB", expiresAt: time.Now().Add(time.Hour)}
	c.mu.Unlock()

	slowStarted := make(chan struct{})
	slowDone := make(chan struct{})

	slowFetch := func(_ context.Context, _ string) (string, error) {
		close(slowStarted)
		<-slowDone
		return "valA", nil
	}

	// Start slow fetch on keyA in background
	go func() {
		_, _ = c.Get(context.Background(), "keyA", slowFetch)
	}()

	<-slowStarted

	// In the meantime, reading keyB must complete immediately without waiting for keyA
	readBStarted := time.Now()
	valB, err := c.Get(context.Background(), "keyB", func(_ context.Context, _ string) (string, error) {
		return "should_not_run", nil
	})
	elapsedB := time.Since(readBStarted)

	if err != nil {
		t.Fatalf("unexpected error reading keyB: %v", err)
	}
	if valB != "valB" {
		t.Errorf("got %q, want 'valB'", valB)
	}
	if elapsedB > 100*time.Millisecond {
		t.Errorf("reading keyB took %v, was blocked by slow keyA!", elapsedB)
	}

	close(slowDone)
}

func TestTTLCache_StaleOnErrorFallback(t *testing.T) {
	c := NewTTLCache[string, string](10 * time.Millisecond)

	// Insert stale entry
	c.mu.Lock()
	c.data["stale_key"] = entry[string]{value: "old_val", expiresAt: time.Now().Add(-time.Hour)}
	c.mu.Unlock()

	errFail := errors.New("backend failed")
	failingFetch := func(_ context.Context, _ string) (string, error) {
		return "", errFail
	}

	val, err := c.Get(context.Background(), "stale_key", failingFetch)
	if err != nil {
		t.Fatalf("expected stale-on-error fallback, but got error: %v", err)
	}
	if val != "old_val" {
		t.Fatalf("got %q, want fallback 'old_val'", val)
	}
}
