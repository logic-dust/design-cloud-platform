package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	connsvc "design-cloud-engine/internal/server/service/connection"

	"github.com/labstack/echo/v5"
)

type connectionHandler struct {
	connection connService
	logger     *slog.Logger
}

type connService interface {
	ListByUser(ctx context.Context, username string) ([]model.Connection, error)
	ListPage(ctx context.Context, limit, offset int) ([]model.Connection, int64, error)
	GetByID(ctx context.Context, id uint) (model.Connection, error)
	GetByIDForUser(ctx context.Context, id uint, username string) (model.Connection, error)
	GetVisibleByIDForUser(ctx context.Context, id uint, username string) (model.Connection, error)
	Launch(ctx context.Context, username string, resourceID uint) (model.Connection, error)
	Delete(ctx context.Context, requestUser string, ids ...uint) (connsvc.DeleteResult, error)
	RefreshUserConnections(ctx context.Context, username string) error
	GenerateVNCOTP(ctx context.Context, connectionID uint, username string) (string, error)
	StartConnection(ctx context.Context, connectionID uint, username string) error
	StopConnection(ctx context.Context, connectionID uint, username string) error
	RestartConnection(ctx context.Context, connectionID uint, username string) error
	Touch(ctx context.Context, connectionID uint, username string) error
	TriggerCollect(ctx context.Context, target contract.CollectTarget) error
	ExecuteCleanup(ctx context.Context) (connsvc.CleanupRunResult, error)

	// Resource specifications
	ListAccessibleResources(ctx context.Context, username string) ([]connsvc.ResourceDTO, error)
	GetAccessibleResourceByID(ctx context.Context, username string, id uint) (connsvc.ResourceDTO, error)
	AdminListResources(ctx context.Context, limit, offset int, protocol, query string) ([]model.ConnectionResource, int64, error)
	AdminGetResourceByID(ctx context.Context, id uint) (model.ConnectionResource, error)
	AdminCreateResource(ctx context.Context, res *model.ConnectionResource) error
	AdminUpdateResource(ctx context.Context, res *model.ConnectionResource) error
	AdminBatchUpsertResources(ctx context.Context, items []model.ConnectionResource) (*connsvc.BatchResourceUpsertResponse, error)
	AdminDeleteResource(ctx context.Context, id uint) error
	AdminListResourceUsers(ctx context.Context, resourceID uint) ([]model.User, error)
	AdminListResourceUsersPage(ctx context.Context, resourceID uint, limit, offset int) ([]model.User, int64, error)
	AdminReplaceResourceUsers(ctx context.Context, resourceID uint, usernames []string) error
	AdminPatchResourceUsers(ctx context.Context, resourceID uint, addNames, removeNames []string) error
}

func newConnectionHandler(connectionSvc connService, logger *slog.Logger) *connectionHandler {
	return &connectionHandler{
		connection: connectionSvc,
		logger:     logger,
	}
}

// route: /api/connections
func (h *connectionHandler) connectionsAPI(g *echo.Group) {
	// 规格子资源
	g.GET("/resources", h.listAccessibleResources)
	g.GET("/resources/:id", h.getAccessibleResource)

	// 实例资源
	g.GET("", h.listConnections)
	g.POST("", h.launchConnection)
	g.POST("/batch-delete", h.bulkDeleteConnections)
	g.POST("/refresh", h.refreshConnections)
	g.GET("/:id", h.getConnection)
	g.DELETE("/:id", h.deleteConnection)
	g.POST("/:id/vnc-otp", h.createConnectionVNCOTP)
	g.POST("/:id/touch", h.touchConnection)
	g.POST("/:id/:op", h.connectionAction)
}

// --- 规格端点

func (h *connectionHandler) listAccessibleResources(c *echo.Context) error {
	resources, err := h.connection.ListAccessibleResources(c.Request().Context(), httputil.CurrentUsername(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, resources)
}

func (h *connectionHandler) getAccessibleResource(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.connection.GetAccessibleResourceByID(c.Request().Context(), httputil.CurrentUsername(c), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return httputil.RespondOK(c, res)
}

// --- 实例端点

func (h *connectionHandler) listConnections(c *echo.Context) error {
	connections, err := h.connection.ListByUser(c.Request().Context(), httputil.CurrentUsername(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, connections)
}

func (h *connectionHandler) launchConnection(c *echo.Context) error {
	var body struct {
		ResourceID uint `json:"resourceId"`
	}
	if err := echo.BindBody(c, &body); err != nil {
		return httputil.RespondError(c, err)
	}
	if body.ResourceID == 0 {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "resourceId is required")
	}

	conn, err := h.connection.Launch(c.Request().Context(), httputil.CurrentUsername(c), body.ResourceID)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return httputil.RespondCreated(c, conn)
}

func (h *connectionHandler) getConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	conn, err := h.connection.GetVisibleByIDForUser(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}
	return httputil.RespondOK(c, conn)
}

func (h *connectionHandler) deleteConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.connection.Delete(c.Request().Context(), httputil.CurrentUsername(c), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.TerminatingCount > 0 {
		return httputil.RespondAccepted(c, res)
	}
	return httputil.RespondNoContent(c)
}

func (h *connectionHandler) bulkDeleteConnections(c *echo.Context) error {
	var body struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := echo.BindBody(c, &body); err != nil {
		return httputil.RespondError(c, err)
	}
	if len(body.IDs) == 0 {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "ids are required")
	}
	res, err := h.connection.Delete(c.Request().Context(), httputil.CurrentUsername(c), body.IDs...)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.TerminatingCount > 0 {
		return httputil.RespondAccepted(c, res)
	}
	return httputil.RespondNoContent(c)
}

func (h *connectionHandler) refreshConnections(c *echo.Context) error {
	if err := h.connection.RefreshUserConnections(c.Request().Context(), httputil.CurrentUsername(c)); err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondNoContent(c)
}

func (h *connectionHandler) createConnectionVNCOTP(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	otp, err := h.connection.GenerateVNCOTP(c.Request().Context(), id, httputil.CurrentUsername(c))
	if err != nil {
		return handleConnectionError(c, err)
	}
	return httputil.RespondOK(c, map[string]string{"otp": otp})
}

func (h *connectionHandler) touchConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.connection.Touch(c.Request().Context(), id, httputil.CurrentUsername(c)); err != nil {
		return handleConnectionError(c, err)
	}
	return httputil.RespondNoContent(c)
}

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
		return httputil.RespondHTTPError(c, http.StatusNotFound, "unknown connection action")
	}
	if actionErr != nil {
		return handleConnectionError(c, actionErr)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- Shared error mapper

func handleConnectionError(c *echo.Context, err error) error {
	if status, msg, ok := connsvc.PublicHTTP(err); ok {
		return httputil.RespondHTTPError(c, status, msg)
	}
	if _, ok := errors.AsType[*execution.ActionError](err); ok {
		return httputil.RespondHTTPError(c, http.StatusBadGateway, err.Error())
	}
	return httputil.RespondError(c, err)
}
