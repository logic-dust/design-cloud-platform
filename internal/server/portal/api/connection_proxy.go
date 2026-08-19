package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	"design-cloud-engine/internal/server/service/connection"
	"design-cloud-engine/internal/server/service/proxy"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v5"
)

type connProxyHandler struct {
	proxy      *proxy.Service
	connection *connection.Service
	logger     *slog.Logger
	portalHost string // set when WebSocket Host verification is desired (browser support)
}

func newConnProxyHandler(svc *service.Container, logger *slog.Logger, portalHost string) *connProxyHandler {
	return &connProxyHandler{proxy: svc.Proxy, connection: svc.Connection, logger: logger, portalHost: portalHost}
}

const (
	wsTokenSubprotocolPrefix = "dc-proxy-token."
	proxyWSBufSize           = 32768
)

var proxyBufferPool = sync.Pool{
	New: func() any {
		return new([proxyWSBufSize]byte)
	},
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// proxySessionAPI registers proxy-session sub-resources under /api/connections.
// route: /api/connections/:id/proxy-session
func (h *connProxyHandler) proxySessionAPI(g *echo.Group) {
	g.POST("/:id/proxy-session", h.createConnectionProxySession)
	g.GET("/:id/proxy-session", h.getConnectionProxySession)
	g.DELETE("/:id/proxy-session", h.deleteConnectionProxySession)
}

// route: /api/connection-proxy/ws (OTP auth, no session cookie)
func (h *connProxyHandler) proxyAccessAPI(g *echo.Group) {
	if h.proxy != nil {
		g.GET("/ws", h.handleProxyAccessWS)
	}
}

// ---------------------------------------------------------------------------
// Proxy session CRUD handlers
// ---------------------------------------------------------------------------

func (h *connProxyHandler) createConnectionProxySession(c *echo.Context) error {
	if h.proxy == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled on this node"})
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	conn, err := h.connection.GetByIDForUser(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}

	req := &proxy.CreateTunnelRequest{
		ConnectionID: conn.ID,
	}

	tunnel, err := h.proxy.Create(c.Request().Context(), req)
	if err != nil {
		return handleProxyError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: tunnel})
}

func (h *connProxyHandler) getConnectionProxySession(c *echo.Context) error {
	if h.proxy == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled"})
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	conn, err := h.connection.GetByIDForUser(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}

	tunnel, err := h.proxy.GetActiveByConnectionID(conn.ID)
	if err != nil {
		return handleProxyError(c, err)
	}

	tunnel.OTP = ""
	return c.JSON(http.StatusOK, httputil.Response{Data: tunnel})
}

func (h *connProxyHandler) deleteConnectionProxySession(c *echo.Context) error {
	if h.proxy == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled"})
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	conn, err := h.connection.GetByIDForUser(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}

	if _, err := h.proxy.GetActiveByConnectionID(conn.ID); err != nil {
		return handleProxyError(c, err)
	}
	if err := h.proxy.CloseByConnectionID(conn.ID); err != nil {
		return handleProxyError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Error mapper
// ---------------------------------------------------------------------------

func handleProxyError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, proxy.ErrConnectionNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "connection not found"})
	case errors.Is(err, proxy.ErrProxyTunnelNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "no active proxy tunnel found"})
	case errors.Is(err, proxy.ErrProxyServiceDisabled):
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled"})
	case errors.Is(err, proxy.ErrProxyProtocolNotAllowed):
		return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
	case errors.Is(err, proxy.ErrProxyMaxTunnelsReached), errors.Is(err, proxy.ErrProxyMaxTunnelsPerUserReached):
		return c.JSON(http.StatusTooManyRequests, httputil.Response{Message: err.Error()})
	case errors.Is(err, fs.ErrInvalid):
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	default:
		return httputil.RespondError(c, err)
	}
}

// ---------------------------------------------------------------------------
// WebSocket data-plane handler
// ---------------------------------------------------------------------------

// handleProxyAccessWS is the WebSocket data-plane handler for proxy-mode access.
// Moved from service layer to API layer to ensure service layer is framework-agnostic.
func (h *connProxyHandler) handleProxyAccessWS(c *echo.Context) error {
	token, selectedSubprotocol := extractWebSocketToken(c.Request())
	if token == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "Missing token"})
	}

	tunnel, err := h.proxy.GetByOTP(token)
	if err != nil {
		if errors.Is(err, proxy.ErrProxyTunnelNotFound) {
			return c.JSON(http.StatusUnauthorized, httputil.Response{Message: "Invalid token"})
		}
		return httputil.RespondError(c, err)
	}

	if tunnel.Status != proxy.StatusPending {
		return c.JSON(http.StatusUnauthorized, httputil.Response{Message: "Token already used"})
	}

	// Protocol, host and port are snapshotted into the tunnel at creation time,
	// so the OTP phase does not require a Connection DB round-trip.
	handler, protocol, targetAddr, err := h.prepareProxyBackend(c, tunnel)
	if err != nil {
		return err
	}

	// Activate tunnel
	tunnel, err = h.proxy.Activate(token)
	if err != nil {
		if errors.Is(err, proxy.ErrProxyTunnelNotFound) || errors.Is(err, fs.ErrPermission) {
			return c.JSON(http.StatusUnauthorized, httputil.Response{Message: "Token is no longer valid"})
		}
		return httputil.RespondError(c, err)
	}

	// Dial backend using protocol handler
	dialCtx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()

	tcpConn, err := handler.Connect(dialCtx, targetAddr)
	if err != nil {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on backend connect error")
		h.logger.Error("Failed to connect to backend", "protocol", protocol, "target", targetAddr, "error", err)
		return c.JSON(http.StatusBadGateway, httputil.Response{Message: err.Error()})
	}
	defer func() { _ = tcpConn.Close() }()

	// Origin check
	if !allowWailsOrigin(c.Request(), h.portalHost) {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on origin check failure")
		return c.JSON(http.StatusForbidden, httputil.Response{Message: "forbidden origin"})
	}

	acceptOpts := &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Origin is explicitly verified by allowWailsOrigin above
	}
	if selectedSubprotocol != "" {
		acceptOpts.Subprotocols = []string{selectedSubprotocol}
	}

	wsConn, err := websocket.Accept(c.Response(), c.Request(), acceptOpts)
	if err != nil {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on WS upgrade error")
		h.logger.Error("WS upgrade failed", "error", err)
		return err
	}
	defer func() { _ = wsConn.Close(websocket.StatusNormalClosure, "") }()

	h.logger.Info("Proxy established", "connection", tunnel.ConnectionID, "protocol", protocol, "target", targetAddr)

	h.bridgeProxyConnections(c.Request().Context(), wsConn, tcpConn)

	// Close tunnel
	if err := h.proxy.CloseByConnectionID(tunnel.ConnectionID); err != nil && !errors.Is(err, proxy.ErrProxyTunnelNotFound) {
		h.logger.Warn("failed to close proxy tunnel", "connection", tunnel.ConnectionID, "error", err)
	}

	h.logger.Info("Proxy closed", "connection", tunnel.ConnectionID, "protocol", protocol)
	return nil
}

func (h *connProxyHandler) prepareProxyBackend(c *echo.Context, tunnel *proxy.Tunnel) (proxy.ProtocolHandler, contract.Protocol, string, error) {
	protocol := tunnel.Protocol
	if protocol == "" {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on missing protocol")
		return nil, "", "", c.JSON(http.StatusBadRequest, httputil.Response{Message: "proxy protocol not defined"})
	}
	if err := h.proxy.EnsureProtocolAllowed(c.Request().Context(), protocol); err != nil {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on disallowed protocol", "protocol", protocol)
		return nil, "", "", handleProxyError(c, err)
	}
	handler, err := h.proxy.GetProtocolHandler(protocol)
	if err != nil {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on protocol error")
		return nil, "", "", c.JSON(http.StatusBadRequest, httputil.Response{Message: fmt.Sprintf("Unsupported protocol: %s", protocol)})
	}
	if tunnel.Host == "" {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on missing target host")
		return nil, "", "", c.JSON(http.StatusBadRequest, httputil.Response{Message: "proxy target host not defined"})
	}
	if tunnel.Port == 0 || tunnel.Port > 65535 {
		h.closeProxySessionWithWarn(tunnel.ConnectionID, "failed to close proxy tunnel on invalid target port")
		return nil, "", "", c.JSON(http.StatusBadRequest, httputil.Response{Message: "proxy target port is invalid"})
	}
	return handler, protocol, net.JoinHostPort(tunnel.Host, strconv.FormatUint(uint64(tunnel.Port), 10)), nil
}

func (h *connProxyHandler) closeProxySessionWithWarn(connectionID uint, msg string, attrs ...any) {
	if err := h.proxy.CloseByConnectionID(connectionID); err != nil && !errors.Is(err, proxy.ErrProxyTunnelNotFound) {
		logAttrs := append([]any{"connection", connectionID}, attrs...)
		logAttrs = append(logAttrs, "error", err)
		h.logger.Warn(msg, logAttrs...)
	}
}

func (h *connProxyHandler) bridgeProxyConnections(ctx context.Context, wsConn *websocket.Conn, tcpConn net.Conn) {
	wsNet := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	defer func() { _ = wsNet.Close() }()
	defer func() { _ = tcpConn.Close() }()

	var wg sync.WaitGroup
	var closeOnce sync.Once
	closeConnections := func() {
		closeOnce.Do(func() {
			_ = wsNet.Close()
			_ = tcpConn.Close()
		})
	}

	// WS -> TCP
	wg.Go(func() {
		defer closeConnections()
		bufPtr := proxyBufferPool.Get().(*[proxyWSBufSize]byte)
		defer proxyBufferPool.Put(bufPtr)
		_, _ = io.CopyBuffer(tcpConn, wsNet, bufPtr[:])
	})

	// TCP -> WS
	wg.Go(func() {
		defer closeConnections()
		bufPtr := proxyBufferPool.Get().(*[proxyWSBufSize]byte)
		defer proxyBufferPool.Put(bufPtr)
		_, _ = io.CopyBuffer(wsNet, tcpConn, bufPtr[:])
	})

	wg.Wait()
}

func extractWebSocketToken(r *http.Request) (string, string) {
	if token := httputil.ExtractBearerToken(r); token != "" {
		return token, ""
	}

	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for protocol := range strings.SplitSeq(v, ",") {
			trimmed := strings.TrimSpace(protocol)
			if after, ok := strings.CutPrefix(trimmed, wsTokenSubprotocolPrefix); ok && after != "" {
				return after, trimmed
			}
		}
	}

	return "", ""
}

// allowWailsOrigin 校验 WebSocket 请求的来源是否受信。
//
// 当前架构（原生 app → WS → TCP → VNC）下非浏览器客户端的 Origin 不可靠，
// 因此当未配置 portalHost（默认）时：放行空 Origin（适配非浏览器客户端）和
// 已知受信 scheme（wails://, localhost 开发模式）。
//
// 当 portalHost 已配置（浏览器支持场景）时：额外校验 Origin 是否匹配
// https://<portalHost>，并校验 Host 头防止 DNS rebinding。
func allowWailsOrigin(r *http.Request, portalHost string) bool {
	origin := r.Header.Get("Origin")

	// 浏览器支持已开启但 Origin 为空 → 拒绝（浏览器强制要求 Origin）
	if origin == "" {
		return portalHost == "" // 未配置 browser 模式时仍然放行（非浏览器客户端）
	}

	// 通用受信源：桌面客户端 + 开发模式
	if strings.HasPrefix(origin, "wails://") ||
		strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") {
		return true
	}

	// 浏览器模式：校验 Origin 与 Host 均匹配配置的公网域名
	if portalHost != "" {
		if origin == "https://"+portalHost {
			return r.Host == portalHost
		}
	}

	return false
}
