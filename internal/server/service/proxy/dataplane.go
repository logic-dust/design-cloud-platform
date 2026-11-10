package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"design-cloud-engine/internal/contract"

	"github.com/coder/websocket"
)

// BackendDialError is returned when the TCP dial to the desktop target fails.
// Portal maps it to HTTP 502.
type BackendDialError struct {
	Protocol contract.Protocol
	Target   string
	Err      error
}

func (e *BackendDialError) Error() string {
	if e == nil || e.Err == nil {
		return "proxy backend dial failed"
	}
	return e.Err.Error()
}

func (e *BackendDialError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ServeDataPlane opens the mediated proxy tunnel: prepare → dial → register →
// WebSocket accept → bridge. Portal only performs auth/origin checks and maps errors.
func (s *Service) ServeDataPlane(ctx context.Context, req *OpenDataPlaneRequest, w http.ResponseWriter, r *http.Request) error {
	prepared, err := s.PrepareDataPlaneOpen(ctx, req)
	if err != nil {
		return err
	}

	protocol := prepared.Session.Protocol
	if !protocol.Valid() {
		return contract.NewValidationError("proxy protocol not defined")
	}
	handler, err := s.GetProtocolHandler(protocol)
	if err != nil {
		return contract.NewValidationError(fmt.Sprintf("Unsupported protocol: %s", protocol.String()))
	}
	if prepared.Session.Host == "" || prepared.Session.Port == 0 || prepared.Session.Port > 65535 {
		return contract.NewValidationError("proxy target is invalid")
	}

	targetAddr := net.JoinHostPort(prepared.Session.Host, strconv.FormatUint(uint64(prepared.Session.Port), 10))
	life := NewProxyConnectionLifecycle(ctx)

	tcpConn, err := handler.Connect(life.Context(), targetAddr)
	if err != nil {
		tcp, ws := life.Close()
		CloseDetachedSockets(tcp, ws)
		s.logger.Error("Failed to connect to backend", "protocol", protocol, "target", targetAddr, "error", err)
		return &BackendDialError{Protocol: protocol, Target: targetAddr, Err: err}
	}

	session, err := s.RegisterDataPlaneSession(ctx, prepared, life)
	if err != nil {
		_ = tcpConn.Close()
		tcp, ws := life.Close()
		CloseDetachedSockets(tcp, ws)
		return err
	}
	sessionID := session.ID
	defer func() {
		if closeErr := s.CloseByID(sessionID); closeErr != nil && !errors.Is(closeErr, ErrProxyConnectionNotFound) {
			s.logger.Warn("failed to close proxy session", "session", sessionID, "error", closeErr)
		}
	}()

	// InsecureSkipVerify 关闭 coder/websocket 的 Origin 校验。这是安全的，前提是
	// 本路由只认 Authorization: Bearer 头（见 proxyTunnelPolicy → sessionAuth）：
	// 浏览器无法自动携带该头，故不存在 CSWSH 条件。关闭 Origin 校验是为了兼容
	// 不发送 Origin 的桌面客户端。
	//
	// ⚠️ 若将来把本路由的认证改成 Cookie / URL 查询参数等"浏览器自动携带"的凭据，
	// 必须同时移除此选项（改回同源校验），否则任意站点都能劫持用户桌面会话。
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		_ = tcpConn.Close()
		s.logger.Error("WS upgrade failed", "error", err)
		return err
	}
	wsConn.SetReadLimit(WSReadLimit)

	s.logger.Info("Proxy established",
		"connection", session.ConnectionID,
		"resource", session.ResourceID,
		"requester", session.Requester,
		"protocol", protocol,
		"target", targetAddr,
	)

	started := time.Now()
	stats := Bridge(ctx, life, wsConn, tcpConn, s.BridgeWriteDeadline())

	s.logger.Info("Proxy closed",
		"connection", session.ConnectionID,
		"resource", session.ResourceID,
		"requester", session.Requester,
		"protocol", protocol,
		"upBytes", stats.UpBytes,
		"downBytes", stats.DownBytes,
		"durationMs", time.Since(started).Milliseconds(),
	)
	return nil
}
