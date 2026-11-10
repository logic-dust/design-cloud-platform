package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/connection"
	"design-cloud-engine/internal/server/service/proxy"

	"github.com/labstack/echo/v5"
)

type connProxyHandler struct {
	proxy      *proxy.Service
	connection *connection.Service
	logger     *slog.Logger
	portalHost string // set when WebSocket Host verification is desired (browser support)
}

func newConnProxyHandler(proxySvc *proxy.Service, connectionSvc *connection.Service, logger *slog.Logger, portalHost string) *connProxyHandler {
	return &connProxyHandler{proxy: proxySvc, connection: connectionSvc, logger: logger, portalHost: portalHost}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// proxyConnectionAPI registers proxy data-plane routes under /api/connections.
// Static /ssh must be registered before /:id/...
func (h *connProxyHandler) proxyConnectionAPI(g *echo.Group) {
	g.GET("/ssh", h.handleSSHProxy)
	g.GET("/:id/proxy-connection", h.handleConnectionProxyConnection)
}

// ---------------------------------------------------------------------------
// Connection-anchored proxy (VNC / RDP / XRDP)
// ---------------------------------------------------------------------------

func (h *connProxyHandler) handleConnectionProxyConnection(c *echo.Context) error {
	if isWebSocketUpgrade(c.Request()) {
		return h.handleConnectionProxyWS(c)
	}
	return h.getConnectionProxyConnection(c)
}

func (h *connProxyHandler) getConnectionProxyConnection(c *echo.Context) error {
	if h.proxy == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled"})
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	requester := httputil.CurrentUsername(c)
	conn, err := h.connection.GetByIDForUser(c.Request().Context(), id, requester)
	if err != nil {
		return handleConnectionError(c, err)
	}

	session, err := h.proxy.GetActiveByConnectionIDAndRequester(conn.ID, requester)
	if err != nil {
		return handleProxyError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: session})
}

// ---------------------------------------------------------------------------
// SSH resource proxy (no connections row): GET /ssh?resourceId= + Upgrade only
// ---------------------------------------------------------------------------

func (h *connProxyHandler) handleSSHProxy(c *echo.Context) error {
	if !isWebSocketUpgrade(c.Request()) {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "WebSocket Upgrade required"})
	}
	return h.handleSSHProxyWS(c)
}

func (h *connProxyHandler) handleSSHProxyWS(c *echo.Context) error {
	if h.proxy == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled on this node"})
	}

	resourceID, err := httputil.ParseOptionalUintQuery(c, "resourceId")
	if err != nil {
		return err
	}
	if resourceID == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [resourceId] is required"})
	}
	requester := httputil.CurrentUsername(c)
	if requester == "" {
		return c.JSON(http.StatusUnauthorized, httputil.Response{Message: "authentication required"})
	}
	if !allowWailsOrigin(c.Request(), h.portalHost) {
		return c.JSON(http.StatusForbidden, httputil.Response{Message: "forbidden origin"})
	}

	if err := h.proxy.ServeDataPlane(c.Request().Context(), &proxy.OpenDataPlaneRequest{
		ResourceID: resourceID,
		Requester:  requester,
	}, c.Response(), c.Request()); err != nil {
		return handleProxyError(c, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Error mapper
// ---------------------------------------------------------------------------

func handleProxyError(c *echo.Context, err error) error {
	if dialErr, ok := errors.AsType[*proxy.BackendDialError](err); ok {
		return c.JSON(http.StatusBadGateway, httputil.Response{Message: dialErr.Error()})
	}
	if status, msg, ok := connection.PublicHTTP(err); ok {
		return c.JSON(status, httputil.Response{Message: msg})
	}
	switch {
	case errors.Is(err, proxy.ErrConnectionNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "connection not found"})
	case errors.Is(err, proxy.ErrProxyConnectionNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "no active proxy session found"})
	case errors.Is(err, proxy.ErrSSHUsesResourceProxy):
		return c.JSON(http.StatusUnprocessableEntity, httputil.Response{Message: err.Error()})
	case errors.Is(err, proxy.ErrProxyServiceDisabled),
		errors.Is(err, proxy.ErrNodeInMaintenance),
		errors.Is(err, proxy.ErrNodeConnectionsDisallowed),
		errors.Is(err, proxy.ErrSSHDialResolverUnavailable):
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: err.Error()})
	case errors.Is(err, proxy.ErrProxyProtocolNotAllowed), errors.Is(err, proxy.ErrNotProxyable), errors.Is(err, proxy.ErrProxyConnectionForbidden):
		return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
	case errors.Is(err, proxy.ErrProxyMaxSessionsReached), errors.Is(err, proxy.ErrProxyMaxSessionsPerUserReached):
		return c.JSON(http.StatusTooManyRequests, httputil.Response{Message: err.Error()})
	default:
		return httputil.RespondError(c, err)
	}
}

// ---------------------------------------------------------------------------
// WebSocket data-plane handler
// ---------------------------------------------------------------------------

func (h *connProxyHandler) handleConnectionProxyWS(c *echo.Context) error {
	if h.proxy == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "proxy access is disabled on this node"})
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	requester := httputil.CurrentUsername(c)
	if requester == "" {
		return c.JSON(http.StatusUnauthorized, httputil.Response{Message: "authentication required"})
	}

	if !allowWailsOrigin(c.Request(), h.portalHost) {
		return c.JSON(http.StatusForbidden, httputil.Response{Message: "forbidden origin"})
	}

	if err := h.proxy.ServeDataPlane(c.Request().Context(), &proxy.OpenDataPlaneRequest{
		ConnectionID: id,
		Requester:    requester,
	}, c.Response(), c.Request()); err != nil {
		return handleProxyError(c, err)
	}
	return nil
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// allowWailsOrigin 校验 WebSocket 请求的来源是否受信。
func allowWailsOrigin(r *http.Request, portalHost string) bool {
	origin := r.Header.Get("Origin")

	if origin == "" {
		return portalHost == ""
	}

	if strings.HasPrefix(origin, "wails://") ||
		strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") {
		return true
	}

	if portalHost != "" {
		if origin == "https://"+portalHost {
			return r.Host == portalHost
		}
	}

	return false
}
