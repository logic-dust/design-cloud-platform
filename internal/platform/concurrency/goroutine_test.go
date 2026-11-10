package concurrency

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
	ch chan struct{}
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err = s.b.Write(p)
	select {
	case s.ch <- struct{}{}:
	default:
	}
	return n, err
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestSafeGo_NormalExecution(t *testing.T) {
	var executed bool
	var wg sync.WaitGroup
	wg.Add(1)

	SafeGo(slog.Default(), "test-normal", func() {
		defer wg.Done()
		executed = true
	})

	wg.Wait()
	if !executed {
		t.Fatal("expected function to be executed")
	}
}

func TestSafeGo_RecoverPanic(t *testing.T) {
	sbuf := &safeBuffer{ch: make(chan struct{}, 1)}
	logger := slog.New(slog.NewTextHandler(sbuf, nil))

	SafeGo(logger, "test-panicking-worker", func() {
		panic("simulated worker explosion")
	})

	select {
	case <-sbuf.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for panic log")
	}

	logOutput := sbuf.String()
	if !strings.Contains(logOutput, "goroutine panic recovered") {
		t.Fatalf("expected log to contain panic recovery message, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "test-panicking-worker") {
		t.Fatalf("expected log to contain worker name, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "simulated worker explosion") {
		t.Fatalf("expected log to contain panic value, got: %s", logOutput)
	}
}

func TestSafeRecover_DeferredDirectly(t *testing.T) {
	sbuf := &safeBuffer{ch: make(chan struct{}, 1)}
	logger := slog.New(slog.NewTextHandler(sbuf, nil))

	func() {
		defer SafeRecover(logger, "test-direct-recover")()
		panic("direct recover panic")
	}()

	logOutput := sbuf.String()
	if !strings.Contains(logOutput, "test-direct-recover") {
		t.Fatalf("expected log to contain worker name, got: %s", logOutput)
	}
}

func TestBoundedGo_LimitsConcurrency(t *testing.T) {
	sem := make(chan struct{}, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	BoundedGo(sem, slog.Default(), "first", func() {
		defer wg.Done()
		close(started)
		<-release
	})
	<-started

	secondEntered := make(chan struct{})
	go func() {
		BoundedGo(sem, slog.Default(), "second", func() {
			defer wg.Done()
			close(secondEntered)
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second BoundedGo ran before first released the slot")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second BoundedGo")
	}
	wg.Wait()
}

func TestTryBoundedGo_DropsWhenSaturatedWithoutBlocking(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	called := make(chan struct{}, 1)

	if TryBoundedGo(sem, slog.Default(), "saturated", func() { called <- struct{}{} }) {
		t.Fatal("TryBoundedGo() = true with a full semaphore, want false")
	}
	select {
	case <-called:
		t.Fatal("function ran despite a full semaphore")
	default:
	}

	<-sem
	if !TryBoundedGo(sem, slog.Default(), "available", func() { called <- struct{}{} }) {
		t.Fatal("TryBoundedGo() = false with an available slot, want true")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("function did not run after acquiring an available slot")
	}
}
