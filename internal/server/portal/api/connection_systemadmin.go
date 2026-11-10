package api

import (
	"errors"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	connsvc "design-cloud-engine/internal/server/service/connection"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/connections
func (h *connectionHandler) adminConnectionsAPI(g *echo.Group) {
	// 规格管理子路由 (/api/admin/system/connections/resources)
	resGroup := g.Group("/resources")
	resGroup.GET("", h.adminListResources)
	resGroup.POST("", h.adminCreateResource)
	resGroup.POST("/batch-upsert", h.adminBatchUpsertResources)
	resGroup.GET("/:id", h.adminGetResource)
	resGroup.PUT("/:id", h.adminUpdateResource)
	resGroup.DELETE("/:id", h.adminDeleteResource)
	resGroup.GET("/:id/users", h.adminListResourceUsers)
	resGroup.PUT("/:id/users", h.adminReplaceResourceUsers)
	resGroup.PATCH("/:id/users", h.adminPatchResourceUsers)

	// 实例管理子路由
	g.GET("", h.adminListConnections)
	g.GET("/:id", h.adminGetConnection)
	g.POST("/batch-delete", h.adminBulkDeleteConnections)
	g.DELETE("/:id", h.adminDeleteConnection)
	g.POST("/refresh", h.adminRefreshConnections)
	g.POST("/cleanup", h.executeConnectionCleanup)
	g.POST("/:id/vnc-otp", h.adminCreateConnectionVNCOTP)
	g.POST("/:id/:op", h.adminConnectionAction)
}

// --- 规格管理 Handlers

func (h *connectionHandler) adminListResources(c *echo.Context) error {
	p := httputil.ParsePage(c)
	protocol := c.QueryParam("protocol")
	name := c.QueryParam("name")

	list, total, err := h.connection.AdminListResources(c.Request().Context(), p.Limit, p.Offset, protocol, name)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, list, total)
}

func (h *connectionHandler) adminCreateResource(c *echo.Context) error {
	var res model.ConnectionResource
	if err := c.Bind(&res); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.connection.AdminCreateResource(c.Request().Context(), &res); err != nil {
		return handleConnectionError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: res})
}

func (h *connectionHandler) adminBatchUpsertResources(c *echo.Context) error {
	var req struct {
		Items []model.ConnectionResource `json:"items"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	out, err := h.connection.AdminBatchUpsertResources(c.Request().Context(), req.Items)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: out})
}

func (h *connectionHandler) adminGetResource(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.connection.AdminGetResourceByID(c.Request().Context(), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: res})
}

func (h *connectionHandler) adminUpdateResource(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	var res model.ConnectionResource
	if err := c.Bind(&res); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	res.ID = id

	if err := h.connection.AdminUpdateResource(c.Request().Context(), &res); err != nil {
		return handleConnectionError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: res})
}

func (h *connectionHandler) adminDeleteResource(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.connection.AdminDeleteResource(c.Request().Context(), id); err != nil {
		return handleConnectionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *connectionHandler) adminListResourceUsers(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.connection.AdminGetResourceByID(c.Request().Context(), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.Public {
		return httputil.RespondPage(c, []model.User{}, 0)
	}

	p := httputil.ParsePage(c)
	users, total, err := h.connection.AdminListResourceUsersPage(c.Request().Context(), id, p.Limit, p.Offset)
	if err != nil {
		return handleConnectionError(c, err)
	}
	return httputil.RespondPage(c, users, total)
}

func (h *connectionHandler) adminReplaceResourceUsers(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.connection.AdminGetResourceByID(c.Request().Context(), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.Public {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "public resource does not accept user mappings"})
	}

	var body struct {
		UserIDs []uint `json:"userIds"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.connection.AdminReplaceResourceUsers(c.Request().Context(), id, body.UserIDs); err != nil {
		return handleConnectionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *connectionHandler) adminPatchResourceUsers(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.connection.AdminGetResourceByID(c.Request().Context(), id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.Public {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "public resource does not accept user mappings"})
	}

	var body struct {
		AddUserIDs    []uint `json:"addUserIds"`
		RemoveUserIDs []uint `json:"removeUserIds"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.connection.AdminPatchResourceUsers(c.Request().Context(), id, body.AddUserIDs, body.RemoveUserIDs); err != nil {
		return handleConnectionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 实例管理 Handlers

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
	p := httputil.ParsePage(c)
	connections, total, err := h.connection.ListPage(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, connections, total)
}

func (h *connectionHandler) adminGetConnection(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	conn, err := h.connection.GetVisibleByIDForUser(c.Request().Context(), id, connsvc.AllUsers)
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
	res, err := h.connection.Delete(c.Request().Context(), connsvc.AllUsers, id)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.TerminatingCount > 0 {
		return c.JSON(http.StatusAccepted, httputil.Response{
			Message: "remote session teardown incomplete; marked terminating",
			Data:    res,
		})
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

	res, err := h.connection.Delete(c.Request().Context(), connsvc.AllUsers, req.IDs...)
	if err != nil {
		return handleConnectionError(c, err)
	}
	if res.TerminatingCount > 0 {
		return c.JSON(http.StatusAccepted, httputil.Response{
			Message: "remote session teardown incomplete; marked terminating",
			Data:    res,
		})
	}
	return c.NoContent(http.StatusNoContent)
}

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

func (h *connectionHandler) adminRefreshConnections(c *echo.Context) error {
	if h.connection == nil {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: "connection service is disabled"})
	}
	if err := h.connection.TriggerCollect(c.Request().Context(), contract.CollectTargetConnection); err != nil {
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
