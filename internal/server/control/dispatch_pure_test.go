package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNodeMatchesSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		labels   map[string]string
		selector map[string]string
		want     bool
	}{
		{
			name:     "empty selector matches any",
			labels:   map[string]string{"role": "worker"},
			selector: map[string]string{},
			want:     true,
		},
		{
			name:     "nil selector matches any",
			labels:   map[string]string{"role": "worker"},
			selector: nil,
			want:     true,
		},
		{
			name:     "exact match single key",
			labels:   map[string]string{"role": "worker"},
			selector: map[string]string{"role": "worker"},
			want:     true,
		},
		{
			name:     "value mismatch",
			labels:   map[string]string{"role": "worker"},
			selector: map[string]string{"role": "manager"},
			want:     false,
		},
		{
			name:     "key not present",
			labels:   map[string]string{"role": "worker"},
			selector: map[string]string{"zone": "us-east"},
			want:     false,
		},
		{
			name:     "multiple keys all match",
			labels:   map[string]string{"role": "worker", "zone": "us-east"},
			selector: map[string]string{"role": "worker", "zone": "us-east"},
			want:     true,
		},
		{
			name:     "multiple keys one mismatch",
			labels:   map[string]string{"role": "worker", "zone": "us-east"},
			selector: map[string]string{"role": "worker", "zone": "us-west"},
			want:     false,
		},
		{
			name:     "selector subset of labels",
			labels:   map[string]string{"role": "worker", "zone": "us-east", "rack": "a1"},
			selector: map[string]string{"role": "worker"},
			want:     true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeMatchesSelector(tc.labels, tc.selector)
			if got != tc.want {
				t.Errorf("nodeMatchesSelector(%v, %v) = %v, want %v", tc.labels, tc.selector, got, tc.want)
			}
		})
	}
}

func TestDefaultWaitResult_Result(t *testing.T) {
	t.Parallel()

	resultCh := make(chan any, 1)
	resultCh <- "ok"

	ctx := context.Background()
	got, err := defaultWaitResult(ctx, resultCh, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("got %q, want ok", got)
	}
}

func TestDefaultWaitResult_ErrorInChannel(t *testing.T) {
	t.Parallel()

	resultCh := make(chan any, 1)
	resultCh <- errors.New("something failed")

	ctx := context.Background()
	_, err := defaultWaitResult(ctx, resultCh, 5*time.Second)
	if err == nil || err.Error() != "something failed" {
		t.Fatalf("got %v, want 'something failed'", err)
	}
}

func TestDefaultWaitResult_NilResult(t *testing.T) {
	t.Parallel()

	resultCh := make(chan any, 1)
	resultCh <- nil

	ctx := context.Background()
	_, err := defaultWaitResult(ctx, resultCh, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for nil result")
	}
}

func TestDefaultWaitResult_Timeout(t *testing.T) {
	t.Parallel()

	resultCh := make(chan any) // unbuffered, nothing will be sent

	ctx := context.Background()
	_, err := defaultWaitResult(ctx, resultCh, 50*time.Millisecond)
	if !errors.Is(err, errTimeout) {
		t.Fatalf("got %v, want errTimeout", err)
	}
}

func TestDefaultWaitResult_ContextCanceled(t *testing.T) {
	t.Parallel()

	resultCh := make(chan any) // unbuffered

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := defaultWaitResult(ctx, resultCh, 5*time.Second)
	if err == nil {
		t.Fatal("expected context canceled error")
	}
}
