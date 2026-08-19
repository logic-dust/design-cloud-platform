package api

import (
	"errors"
	"io/fs"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/server/portal/httputil"
	connsvc "design-cloud-engine/internal/server/service/connection"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/connections
func (h *connectionHandler) adminConnectionsAPI(g *echo.Group) {
	g.GET("", h.adminListConnections)
	g.POST("", h.adminCreateFixedConnection)
	g.GET("/:id", h.adminGetConnection)
	g.POST("/batch-delete", h.adminBulkDeleteConnections)
	g.PUT("/batch-upsert", h.adminBatchUpsertFixedConnections)
	g.DELETE("/:id", h.adminDeleteConnection)
	g.POST("/refresh", h.adminRefreshConnections)
	g.POST("/cleanup", h.executeConnectionCleanup)
	g.POST("/:id/vnc-otp", h.adminCreateConnectionVNCOTP)
	g.POST("/:id/:op", h.adminConnectionAction)
}

// adminCreateConnectionVNCOTP generates a TurboVNC OTP for any connection with admin bypass.
func (h *connectionHandler) adminCreateConnectionVNCOTP(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	otp, err := h.connection.GenerateVNCOTP(c.Request().Context(), id, connsvc.AllUsers)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: map[string]string{"otp": otp}})
}

func (h *connectionHandler) adminListConnections(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)
	connections, total, err := h.connection.ListAll(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, connections, total)
}

// POST /api/admin/system/connections — 管理员为指定用户创建固定分配连接
func (h *connectionHandler) adminCreateFixedConnection(c *echo.Context) error {
	var req connsvc.CreateFixedConnectionForm
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	conn, err := h.connection.CreateFixed(c.Request().Context(), req)
	if err != nil {
		if errors.Is(err, fs.ErrInvalid) {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, fs.ErrNotExist) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, connsvc.ErrConnectionAlreadyExists) {
			return c.JSON(http.StatusConflict, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: conn})
}

func (h *connectionHandler) adminGetConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	conn, err := h.connection.GetByID(c.Request().Context(), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: conn})
}

func (h *connectionHandler) adminDeleteConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.connection.Delete(c.Request().Context(), connsvc.AllUsers, id); err != nil {
		return handleConnectionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *connectionHandler) adminBulkDeleteConnections(c *echo.Context) error {
	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids are required"})
	}

	if err := h.connection.Delete(c.Request().Context(), connsvc.AllUsers, req.IDs...); err != nil {
		return handleConnectionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// adminConnectionAction dispatches start/stop/restart with admin bypass.
func (h *connectionHandler) adminConnectionAction(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	var actionErr error
	switch c.Param("op") {
	case "start":
		actionErr = h.connection.StartConnection(ctx, id, connsvc.AllUsers)
	case "stop":
		actionErr = h.connection.StopConnection(ctx, id, connsvc.AllUsers)
	case "restart":
		actionErr = h.connection.RestartConnection(ctx, id, connsvc.AllUsers)
	default:
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown connection action"})
	}
	if actionErr != nil {
		return handleConnectionError(c, actionErr)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 触发全局 VNC 连接刷新（重新搜集各个节点的vnc信息）
func (h *connectionHandler) adminRefreshConnections(c *echo.Context) error {
	if h.connection == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "connection service is disabled"})
	}
	if err := h.connection.TriggerCollect(c.Request().Context(), contract.CollectTargetVNC); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *connectionHandler) executeConnectionCleanup(c *echo.Context) error {
	if h.connection == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "connection service is disabled"})
	}

	result, err := h.connection.ExecuteCleanup(c.Request().Context())
	if err != nil {
		if errors.Is(err, connsvc.ErrCleanupLeaseHeld) {
			return c.JSON(http.StatusConflict, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, connsvc.ErrCleanupServiceUnavailable) {
			return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// PUT /api/admin/system/connections/batch-upsert
// Accepts a list of fixed-connection records and upserts them:
// existing records (matched by username+nodeId+port/display) are updated,
// new records are created. Validation failures are skipped and reported.
func (h *connectionHandler) adminBatchUpsertFixedConnections(c *echo.Context) error {
	var req struct {
		Records []connsvc.CreateFixedConnectionForm `json:"records"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.Records) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "records are required"})
	}

	result, err := h.connection.UpsertFixed(c.Request().Context(), req.Records)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}
