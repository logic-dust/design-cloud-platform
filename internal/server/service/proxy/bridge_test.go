package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestIsBridgeCloseWriteControl(t *testing.T) {
	t.Parallel()
	if !isBridgeCloseWriteControl([]byte(`{"op":"close-write"}`)) {
		t.Fatal("expected close-write control")
	}
	if isBridgeCloseWriteControl([]byte(`{"op":"ping"}`)) {
		t.Fatal("unexpected match")
	}
}

func TestCopyBufferWithWriteDeadlineClosesOnSlowWriter(t *testing.T) {
	t.Parallel()

	slow := &slowWriter{delay: 50 * time.Millisecond}
	_, err := copyBufferWithWriteDeadline(slow, &stringsReader{data: "x"}, make([]byte, 8), 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected write deadline error")
	}
}

func TestCopyBufferWithWriteDeadlineReturnsOnSlowWriter(t *testing.T) {
	t.Parallel()
	slow := &slowWriter{delay: 30 * time.Millisecond}
	done := make(chan struct{})
	go func() {
		_, _ = copyBufferWithWriteDeadline(slow, &stringsReader{data: "x"}, make([]byte, 32), 5*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("copy did not return on write deadline")
	}
}

func TestTCPCloseWritePreservesReadFromBackend(t *testing.T) {
	t.Parallel()

	payload := []byte("payload-after-close-write")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		srv, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer srv.Close()
		buf := make([]byte, 1024)
		for {
			if _, readErr := srv.Read(buf); readErr != nil {
				break
			}
		}
		_, _ = srv.Write(payload)
	}()

	engineToBackend, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial backend: %v", err)
	}
	defer engineToBackend.Close()

	tcpCloseWrite(engineToBackend)

	got := make([]byte, len(payload))
	if err := engineToBackend.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := io.ReadFull(engineToBackend, got); err != nil {
		t.Fatalf("read after close-write: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("read %q, want %q", got, payload)
	}
}

func TestCopyWebSocketInboundToTCPCloseWritePreservesTCPRead(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := []byte("backend-after-close-write")
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	defer backendLn.Close()

	backendReady := make(chan net.Conn, 1)
	go func() {
		srv, acceptErr := backendLn.Accept()
		if acceptErr != nil {
			return
		}
		backendReady <- srv
	}()

	engineToBackend, err := net.Dial("tcp", backendLn.Addr().String())
	if err != nil {
		t.Fatalf("dial backend: %v", err)
	}
	defer engineToBackend.Close()

	backendConn := <-backendReady

	wsReady := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		wsConn, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			t.Errorf("accept ws: %v", acceptErr)
			return
		}
		close(wsReady)
		go func() {
			_ = copyWebSocketInboundToTCP(ctx, wsConn, engineToBackend, make([]byte, 4096), 0)
		}()
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	wsClient, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer wsClient.Close(websocket.StatusNormalClosure, "")

	<-wsReady

	if err := wsClient.Write(ctx, websocket.MessageText, []byte(`{"op":"close-write"}`)); err != nil {
		t.Fatalf("write close-write control: %v", err)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = backendConn.Write(payload)
	}()

	got := make([]byte, len(payload))
	if err := engineToBackend.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := io.ReadFull(engineToBackend, got); err != nil {
		t.Fatalf("read tcp after close-write control: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("read %q, want %q", got, payload)
	}
}

type slowWriter struct {
	delay    time.Duration
	deadline time.Time
}

func (s *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	if !s.deadline.IsZero() && time.Now().After(s.deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return len(p), nil
}
func (s *slowWriter) Read([]byte) (int, error)        { return 0, io.EOF }
func (s *slowWriter) Close() error                    { return nil }
func (s *slowWriter) LocalAddr() net.Addr             { return nil }
func (s *slowWriter) RemoteAddr() net.Addr            { return nil }
func (s *slowWriter) SetDeadline(time.Time) error     { return nil }
func (s *slowWriter) SetReadDeadline(time.Time) error { return nil }
func (s *slowWriter) SetWriteDeadline(t time.Time) error {
	s.deadline = t
	return nil
}

type stringsReader struct {
	data string
	off  int
}

func (r *stringsReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
