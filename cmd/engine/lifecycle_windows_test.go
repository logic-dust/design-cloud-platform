package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// drainStatuses reads SCM status updates until the channel is idle for a short window
// or Stopped is observed. Used by Execute unit tests that drive stub channels.
func drainStatuses(t *testing.T, changes <-chan svc.Status, wantStopped bool) (stopped bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	idle := time.NewTimer(50 * time.Millisecond)
	defer idle.Stop()
	for {
		select {
		case st, ok := <-changes:
			if !ok {
				return stopped
			}
			if st.State == svc.Stopped {
				stopped = true
				if !wantStopped {
					return stopped
				}
			}
			if !idle.Stop() {
				<-idle.C
			}
			idle.Reset(50 * time.Millisecond)
		case <-idle.C:
			return stopped
		case <-deadline:
			t.Fatal("timed out waiting for SCM status updates")
			return
		}
	}
}

func TestEngineServiceExecute_RunFnSucceedsImmediately(t *testing.T) {
	r := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 8)
	s := &engineService{
		runFn: func(context.Context) error { return nil },
	}

	done := make(chan struct {
		ssec  bool
		errno uint32
	}, 1)
	go func() {
		ssec, errno := s.Execute(nil, r, changes)
		done <- struct {
			ssec  bool
			errno uint32
		}{ssec, errno}
	}()

	select {
	case res := <-done:
		if res.errno != 0 {
			t.Fatalf("errno = %d, want 0 on successful runFn", res.errno)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute blocked after runFn returned nil (errCh double-read regression)")
	}

	stopped := drainStatuses(t, changes, true)
	if !stopped {
		t.Fatal("expected Stopped status after successful runFn exit")
	}
}

func TestEngineServiceExecute_RunFnFailsImmediately(t *testing.T) {
	r := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 8)
	s := &engineService{
		runFn: func(context.Context) error { return errors.New("boot failed") },
	}

	done := make(chan uint32, 1)
	go func() {
		_, errno := s.Execute(nil, r, changes)
		done <- errno
	}()

	select {
	case errno := <-done:
		if errno != 1 {
			t.Fatalf("errno = %d, want 1 when runFn fails", errno)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute blocked after runFn returned an error")
	}

	stopped := drainStatuses(t, changes, true)
	if !stopped {
		t.Fatal("expected Stopped status after runFn failure")
	}
}

func TestEngineServiceExecute_StopAfterRunFnAlreadyExited(t *testing.T) {
	// Race: runFn exits with nil before SCM Stop arrives. The previous
	// implementation drained errCh in the select then blocked forever on <-errCh.
	started := make(chan struct{})
	r := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 8)
	s := &engineService{
		runFn: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		},
	}

	done := make(chan struct {
		ssec  bool
		errno uint32
	}, 1)
	go func() {
		ssec, errno := s.Execute(nil, r, changes)
		done <- struct {
			ssec  bool
			errno uint32
		}{ssec, errno}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runFn did not start")
	}

	// Wait until Running has been reported, then stop.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case st := <-changes:
			if st.State == svc.Running {
				r <- svc.ChangeRequest{Cmd: svc.Stop}
				goto waited
			}
		case <-deadline:
			t.Fatal("timed out waiting for Running status")
		}
	}
waited:

	select {
	case res := <-done:
		if res.errno != 0 {
			t.Fatalf("errno = %d, want 0 after graceful Stop", res.errno)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute blocked on Stop after runFn exit race")
	}
}
