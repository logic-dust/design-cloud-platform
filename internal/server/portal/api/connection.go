package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	connsvc "design-cloud-engine/internal/server/service/connection"

	"github.com/labstack/echo/v5"
)

type connectionHandler struct {
	connection connService
	logger     *slog.Logger
}

type connService interface {
	ListByUser(ctx context.Context, username string) ([]model.Connection, error)
	ListAll(ctx context.Context, limit, offset int) ([]model.Connection, int64, error)
	GetByID(ctx context.Context, id uint) (model.Connection, error)
	GetByIDForUser(ctx context.Context, id uint, username string) (model.Connection, error)
	CreateManual(ctx context.Context, requestUser string, form connsvc.CreateManualConnectionForm) (model.Connection, error)
	CreateFixed(ctx context.Context, form connsvc.CreateFixedConnectionForm) (model.Connection, error)
	UpsertFixed(ctx context.Context, forms []connsvc.CreateFixedConnectionForm) (connsvc.UpsertFixedResult, error)
	Delete(ctx context.Context, requestUser string, ids ...uint) error
	RefreshUserConnections(ctx context.Context, username string) error
	GenerateVNCOTP(ctx context.Context, connectionID uint, username string) (string, error)
	StartConnection(ctx context.Context, connectionID uint, username string) error
	StopConnection(ctx context.Context, connectionID uint, username string) error
	RestartConnection(ctx context.Context, connectionID uint, username string) error
	TriggerCollect(ctx context.Context, target contract.CollectTarget) error
	ExecuteCleanup(ctx context.Context) (connsvc.CleanupRunResult, error)
}

func newConnectionHandler(svc *service.Container, logger *slog.Logger) *connectionHandler {
	return &connectionHandler{
		connection: svc.Connection,
		logger:     logger,
	}
}

// route: /api/connections
func (h *connectionHandler) connectionsAPI(g *echo.Group) {
	g.POST("", h.createConnection)
	g.GET("", h.listConnections)
	g.POST("/refresh", h.refreshConnections)
	g.POST("/batch-delete", h.bulkDeleteConnections)
	g.GET("/:id", h.getConnection)
	g.DELETE("/:id", h.deleteConnection)
	g.POST("/:id/vnc-otp", h.createConnectionVNCOTP)
	g.POST("/:id/:op", h.connectionAction)
}

// --- Connection CRUD and lifecycle handlers

// --- 获取当前用户的连接列表
func (h *connectionHandler) listConnections(c *echo.Context) error {
	connections, err := h.connection.ListByUser(c.Request().Context(), httputil.CurrentUsername(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: connections})
}

// --- 根据连接ID获取详细信息
func (h *connectionHandler) getConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	conn, err := h.connection.GetByIDForUser(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: conn})
}

// --- 删除单个连接
func (h *connectionHandler) deleteConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	if err := h.connection.Delete(c.Request().Context(), httputil.CurrentUsername(c), id); err != nil {
		return handleConnectionError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

// --- 批量删除连接
func (h *connectionHandler) bulkDeleteConnections(c *echo.Context) error {
	var body struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(body.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids are required"})
	}

	if err := h.connection.Delete(c.Request().Context(), httputil.CurrentUsername(c), body.IDs...); err != nil {
		return handleConnectionError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

// --- 刷新当前用户的连接状态
func (h *connectionHandler) refreshConnections(c *echo.Context) error {
	if err := h.connection.RefreshUserConnections(c.Request().Context(), httputil.CurrentUsername(c)); err != nil {
		return httputil.RespondError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

// --- 生成连接的 TurboVNC 一次性密码（OTP）
func (h *connectionHandler) createConnectionVNCOTP(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	otp, err := h.connection.GenerateVNCOTP(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: map[string]string{"otp": otp}})
}

// connectionAction dispatches start/stop/restart connection requests.
func (h *connectionHandler) connectionAction(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	username := httputil.CurrentUsername(c)
	ctx := c.Request().Context()
	var actionErr error
	switch c.Param("op") {
	case "start":
		actionErr = h.connection.StartConnection(ctx, id, username)
	case "stop":
		actionErr = h.connection.StopConnection(ctx, id, username)
	case "restart":
		actionErr = h.connection.RestartConnection(ctx, id, username)
	default:
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown connection action"})
	}
	if actionErr != nil {
		return handleConnectionError(c, actionErr)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- Shared error mapper

func handleConnectionError(c *echo.Context, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "connection not found"})
	}
	if errors.Is(err, fs.ErrPermission) {
		return c.JSON(http.StatusForbidden, httputil.Response{Message: "permission denied"})
	}
	if errors.Is(err, fs.ErrInvalid) {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if _, ok := errors.AsType[*execution.ActionError](err); ok {
		return c.JSON(http.StatusBadGateway, httputil.Response{Message: err.Error()})
	}
	return httputil.RespondError(c, err)
}

func (h *connectionHandler) createConnection(c *echo.Context) error {
	var req connsvc.CreateManualConnectionForm
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	conn, err := h.connection.CreateManual(c.Request().Context(), httputil.CurrentUsername(c), req)
	if err != nil {
		if errors.Is(err, fs.ErrInvalid) {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, fs.ErrNotExist) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, fs.ErrPermission) {
			return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, connsvc.ErrConnectionAlreadyExists) {
			return c.JSON(http.StatusConflict, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusCreated, httputil.Response{Data: conn})
}
