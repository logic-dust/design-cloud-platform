package proxy

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

func dialTCPWithRefusedRetry(ctx context.Context, target string, dialTimeout time.Duration) (net.Conn, error) {
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	deadline := time.Now().Add(dialTimeout)
	var d net.Dialer
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		d.Timeout = remaining
		conn, err := d.DialContext(ctx, "tcp", target)
		if err == nil {
			return conn, nil
		}
		if !isConnectionRefused(err) || ctx.Err() != nil {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return false
	}
	return false
}
