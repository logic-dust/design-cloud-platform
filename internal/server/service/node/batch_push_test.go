package node

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingSettingsPusher struct {
	delay time.Duration

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	calls       atomic.Int32
}

func (p *countingSettingsPusher) PushNodeSettingsUpdate(ctx context.Context, nodeID string) error {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	p.mu.Unlock()

	p.calls.Add(1)
	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.delay):
		return nil
	}
}

func TestPushBatchNodeSettingsNeverExceedsSixteenConcurrentWorkers(t *testing.T) {
	pusher := &countingSettingsPusher{delay: 80 * time.Millisecond}
	svc := &Service{
		logger:         slog.Default(),
		settingsPusher: pusher,
	}

	const nodeCount = 40
	ids := make([]string, nodeCount)
	for i := range ids {
		ids[i] = fmt.Sprintf("batch-push-node-%02d", i)
	}

	svc.pushBatchNodeSettings(ids)

	if got := int(pusher.calls.Load()); got != nodeCount {
		t.Fatalf("push calls = %d, want %d", got, nodeCount)
	}
	if pusher.maxInFlight > defaultBatchSettingsPushWorkers {
		t.Fatalf("max concurrent pushes = %d, want <= %d", pusher.maxInFlight, defaultBatchSettingsPushWorkers)
	}
	if pusher.maxInFlight < 2 {
		t.Fatalf("max concurrent pushes = %d, expected parallel pool usage", pusher.maxInFlight)
	}
}
