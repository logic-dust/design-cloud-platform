package proxy

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestDialTCPWithRefusedRetryConnectsToListener(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()

	ctx := context.Background()
	conn, dialErr := dialTCPWithRefusedRetry(ctx, ln.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	_ = conn.Close()
}

func TestDialTCPWithRefusedRetryHonorsCancel(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, dialErr := dialTCPWithRefusedRetry(ctx, ln.Addr().String(), time.Second)
	if dialErr == nil {
		t.Fatal("expected error when context canceled")
	}
}
