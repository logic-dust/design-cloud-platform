package proxy

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// WSReadLimit is the max inbound WebSocket message size after Accept.
// coder/websocket defaults to 32KiB; VNC/RDP frames need a higher ceiling.
const WSReadLimit int64 = 4 << 20 // 4 MiB

const bridgeBufSize = 32768

var bridgeBufferPool = sync.Pool{
	New: func() any {
		return new([bridgeBufSize]byte)
	},
}

// BridgeStats counts bytes copied during a proxy data-plane session.
type BridgeStats struct {
	UpBytes   int64
	DownBytes int64
}

type bridgeControlMessage struct {
	Op string `json:"op"`
}

const bridgeControlCloseWrite = "close-write"

// Bridge copies bytes between an upgraded WebSocket and a backend TCP connection until one side closes.
func Bridge(ctx context.Context, life *ProxyConnectionLifecycle, wsConn *websocket.Conn, tcpConn net.Conn, writeDeadline time.Duration) BridgeStats {
	var upBytes, downBytes int64
	bridgeCtx := ctx
	if life != nil {
		bridgeCtx = life.Context()
	}
	wsNet := websocket.NetConn(bridgeCtx, wsConn, websocket.MessageBinary)
	wsConn.SetReadLimit(WSReadLimit)
	if life != nil {
		life.AttachTCP(tcpConn)
		life.AttachWSNet(wsNet)
	}

	var wg sync.WaitGroup
	var closeOnce sync.Once
	closeConnections := func() {
		closeOnce.Do(func() {
			if life != nil {
				tcp, ws := life.Close()
				CloseDetachedSockets(tcp, ws)
			} else {
				_ = wsNet.Close()
				_ = tcpConn.Close()
			}
		})
	}

	wg.Go(func() {
		defer closeConnections()
		bufPtr := bridgeBufferPool.Get().(*[bridgeBufSize]byte)
		defer bridgeBufferPool.Put(bufPtr)
		n := copyWebSocketInboundToTCP(bridgeCtx, wsConn, tcpConn, bufPtr[:], writeDeadline)
		atomic.AddInt64(&upBytes, n)
	})

	wg.Go(func() {
		defer closeConnections()
		bufPtr := bridgeBufferPool.Get().(*[bridgeBufSize]byte)
		defer bridgeBufferPool.Put(bufPtr)
		n, _ := copyBufferWithWriteDeadline(wsNet, tcpConn, bufPtr[:], writeDeadline)
		atomic.AddInt64(&downBytes, n)
	})

	wg.Wait()
	return BridgeStats{
		UpBytes:   atomic.LoadInt64(&upBytes),
		DownBytes: atomic.LoadInt64(&downBytes),
	}
}

func copyWebSocketInboundToTCP(ctx context.Context, wsConn *websocket.Conn, tcpConn net.Conn, buf []byte, writeDeadline time.Duration) int64 {
	var total int64
	for {
		msgType, reader, err := wsConn.Reader(ctx)
		if err != nil {
			return total
		}
		switch msgType {
		case websocket.MessageBinary:
			n, copyErr := copyBufferWithWriteDeadline(tcpConn, reader, buf, writeDeadline)
			total += n
			if copyErr != nil && !errors.Is(copyErr, io.EOF) {
				return total
			}
		case websocket.MessageText:
			body, readErr := io.ReadAll(reader)
			if readErr != nil {
				return total
			}
			if isBridgeCloseWriteControl(body) {
				tcpCloseWrite(tcpConn)
				continue
			}
		default:
			_, _ = io.Copy(io.Discard, reader)
		}
	}
}

func isBridgeCloseWriteControl(body []byte) bool {
	var msg bridgeControlMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return false
	}
	return msg.Op == bridgeControlCloseWrite
}

func tcpCloseWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
		return
	}
	_ = conn.Close()
}

func copyBufferWithWriteDeadline(dst net.Conn, src io.Reader, buf []byte, writeDeadline time.Duration) (int64, error) {
	if writeDeadline <= 0 {
		return io.CopyBuffer(dst, src, buf)
	}

	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			if err := dst.SetWriteDeadline(time.Now().Add(writeDeadline)); err != nil {
				return written, err
			}
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if errors.Is(er, os.ErrDeadlineExceeded) {
				return written, er
			}
			if errors.Is(er, io.EOF) {
				return written, nil
			}
			return written, er
		}
	}
}
