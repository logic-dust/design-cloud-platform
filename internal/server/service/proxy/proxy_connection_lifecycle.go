package proxy

import (
	"context"
	"net"
	"sync"
)

// ProxyConnectionLifecycle coordinates cancelation and socket teardown for one data plane.
// Sockets may be attached after activation while dial / WebSocket upgrade are in flight.
type ProxyConnectionLifecycle struct {
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	tcp    net.Conn
	wsNet  net.Conn
}

// NewProxyConnectionLifecycle creates a cancellable lifecycle for one proxy data plane.
func NewProxyConnectionLifecycle(parent context.Context) *ProxyConnectionLifecycle {
	return newProxyConnectionLifecycle(parent)
}

func newProxyConnectionLifecycle(parent context.Context) *ProxyConnectionLifecycle {
	ctx, cancel := context.WithCancel(parent)
	return &ProxyConnectionLifecycle{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (l *ProxyConnectionLifecycle) Context() context.Context {
	if l == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *ProxyConnectionLifecycle) AttachTCP(conn net.Conn) {
	if l == nil || conn == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		_ = conn.Close()
		return
	}
	if l.tcp != nil {
		_ = l.tcp.Close()
	}
	l.tcp = conn
}

func (l *ProxyConnectionLifecycle) AttachWSNet(conn net.Conn) {
	if l == nil || conn == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		_ = conn.Close()
		return
	}
	if l.wsNet != nil {
		_ = l.wsNet.Close()
	}
	l.wsNet = conn
}

// Close marks the lifecycle closed, detaches sockets for closing outside the lock, and cancels context.
func (l *ProxyConnectionLifecycle) Close() (tcp, wsNet net.Conn) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, nil
	}
	l.closed = true
	tcp, wsNet = l.tcp, l.wsNet
	l.tcp, l.wsNet = nil, nil
	l.mu.Unlock()
	l.cancel()
	return tcp, wsNet
}

// CloseDetachedSockets closes sockets after they were detached from a lifecycle handle.
func CloseDetachedSockets(tcp, wsNet net.Conn) {
	if tcp != nil {
		_ = tcp.Close()
	}
	if wsNet != nil {
		_ = wsNet.Close()
	}
}
