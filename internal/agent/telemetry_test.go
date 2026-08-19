package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestOneShotTask(t *testing.T) {
	mgr := NewPeriodicRunner()
	var ran atomic.Bool

	mgr.Add(task{
		Name: "test-one-shot",
		Run: func(ctx context.Context) error {
			ran.Store(true)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	cancel()
	mgr.Stop()

	if !ran.Load() {
		t.Fatal("one-shot task did not execute")
	}
}

func TestPeriodicTask(t *testing.T) {
	mgr := NewPeriodicRunner()
	var count atomic.Int32

	mgr.Add(task{
		Name:     "test-periodic",
		Interval: 10 * time.Millisecond,
		Run: func(ctx context.Context) error {
			count.Add(1)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	mgr.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	mgr.Stop()

	n := count.Load()
	if n < 3 {
		t.Fatalf("periodic task ran %d times, expected at least 3", n)
	}
}

func TestMultipleTasks(t *testing.T) {
	mgr := NewPeriodicRunner()
	var a, b atomic.Int32

	mgr.Add(task{Name: "a", Interval: 10 * time.Millisecond,
		Run: func(ctx context.Context) error { a.Add(1); return nil }})
	mgr.Add(task{Name: "b", Interval: 15 * time.Millisecond,
		Run: func(ctx context.Context) error { b.Add(1); return nil }})

	ctx, cancel := context.WithCancel(t.Context())
	mgr.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()
	mgr.Stop()

	if a.Load() < 2 {
		t.Fatalf("task a ran %d times, expected at least 2", a.Load())
	}
	if b.Load() < 1 {
		t.Fatalf("task b ran %d times, expected at least 1", b.Load())
	}
}

func TestRunErrorDoesNotStopTask(t *testing.T) {
	mgr := NewPeriodicRunner()
	var count atomic.Int32

	mgr.Add(task{
		Name:     "test-error",
		Interval: 10 * time.Millisecond,
		Run: func(ctx context.Context) error {
			count.Add(1)
			return fmt.Errorf("intentional error")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	time.Sleep(25 * time.Millisecond)
	cancel()
	mgr.Stop()

	if count.Load() < 2 {
		t.Fatalf("task ran %d times, want at least 2 (errors should not stop it)", count.Load())
	}
}

func TestManager_StopOnError(t *testing.T) {
	mgr := NewPeriodicRunner()
	var count atomic.Int32
	_ = errors.New("dummy error")

	mgr.Add(task{
		Name:        "stop-on-error",
		Interval:    10 * time.Millisecond,
		StopOnError: true,
		Run: func(ctx context.Context) error {
			n := count.Add(1)
			if n >= 2 {
				return fmt.Errorf("fatal")
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	mgr.Stop()

	// The task should run at most a few times: first success, then error stops it.
	if count.Load() > 3 {
		t.Fatalf("task ran %d times; StopOnError should have stopped it early", count.Load())
	}
}

func TestStopIsIdempotent(t *testing.T) {
	mgr := NewPeriodicRunner()
	mgr.Add(task{
		Name: "test-idempotent",
		Run:  func(ctx context.Context) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)

	// Calling Stop multiple times must not panic or hang.
	mgr.Stop()
	mgr.Stop()
	cancel() // cancel after Stop is also safe
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	mgr := NewPeriodicRunner()
	mgr.Stop() // no-op, must not panic
	mgr.Stop()
}

func TestManager_AddAfterStart_IsDiscarded(t *testing.T) {
	mgr := NewPeriodicRunner()
	mgr.Add(task{
		Name: "initial",
		Run:  func(ctx context.Context) error { return nil },
	})

	mgr.Start(context.Background())
	defer mgr.Stop()

	// Add after Start must not panic — it should silently discard with a warning.
	// Verify the task is not registered by checking the state map.
	mgr.Add(task{
		Name: "late-add",
		Run:  func(ctx context.Context) error { return nil },
	})

	if len(mgr.tasks) != 1 || mgr.tasks[0].Name != "initial" {
		t.Errorf("late-add task should have been discarded, tasks: %v", mgr.tasks)
	}
}

func TestPeriodicRunner_RecoversPanic(t *testing.T) {
	mgr := NewPeriodicRunner()
	var runCount atomic.Int32

	mgr.Add(task{
		Name:     "panicking-task",
		Interval: 10 * time.Millisecond,
		Run: func(ctx context.Context) error {
			runCount.Add(1)
			panic("simulated probe panic")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	mgr.Stop()

	// Task should not crash the runner and should run multiple times across panics
	if runCount.Load() < 2 {
		t.Fatalf("expected panicking task to run at least 2 times, ran: %d", runCount.Load())
	}
}
