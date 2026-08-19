package api

import (
	"errors"
	"net/http"
	"strconv"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/server/portal/httputil"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/tickets
func (h *ticketHandler) adminTicketsAPI(g *echo.Group) {
	ticketsGroup := g.Group("")

	ticketsGroup.GET("", h.adminListTickets)
	ticketsGroup.POST("/batch-delete", h.adminBulkDeleteTickets)
	ticketsGroup.POST("/batch-delete-permanent", h.adminBulkPermanentDeleteTickets)
	ticketsGroup.DELETE("/:id", h.adminDeleteTicket)
	ticketsGroup.DELETE("/:id/permanent", h.adminPermanentDeleteTicket)

	projects := ticketsGroup.Group("/projects")
	projects.GET("", h.adminListProjects)
	projects.POST("", h.adminCreateProject)
	projects.PUT("/:id", h.adminUpdateProject)
	projects.DELETE("/:id", h.adminDeleteProject)
	projects.PATCH("/:id/archive", h.adminArchiveProject)
}

// GET /api/admin/system/tickets
func (h *ticketHandler) adminListTickets(c *echo.Context) error {
	filter := ticketsvc.Filter{IncludeDeleted: true}

	if includeDeletedStr := c.QueryParam("includeDeleted"); includeDeletedStr != "" {
		val, err := strconv.ParseBool(includeDeletedStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [includeDeleted] must be a boolean"})
		}
		filter.IncludeDeleted = val
	}

	if projectID, err := httputil.ParseOptionalUintQuery(c, "projectId"); err != nil {
		return err
	} else if projectID > 0 {
		filter.ProjectID = &projectID
	}
	if typeStr := c.QueryParam("type"); typeStr != "" {
		t, err := strconv.Atoi(typeStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [type] must be an integer"})
		}
		filter.Type = &t
	}
	if statusStr := c.QueryParam("status"); statusStr != "" {
		s, err := strconv.Atoi(statusStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [status] must be an integer"})
		}
		filter.Status = &s
	}

	p := httputil.ParsePagination(c, 50, 200)
	filter.Limit = p.Limit
	filter.Offset = min(10000, p.Offset)

	result, err := h.ticket.List(c.Request().Context(), filter)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// DELETE /api/admin/system/tickets/:id
func (h *ticketHandler) adminDeleteTicket(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.ticket.AdminDelete(c.Request().Context(), id); err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "ticket not found"})
		}
		return httputil.RespondError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

// DELETE /api/admin/system/tickets/:id/permanent
func (h *ticketHandler) adminPermanentDeleteTicket(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.ticket.AdminPermanentDelete(c.Request().Context(), id); err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "ticket not found"})
		}
		return httputil.RespondError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

func (h *ticketHandler) adminBulkDeleteTickets(c *echo.Context) error {
	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids is required"})
	}

	if err := h.ticket.AdminDelete(c.Request().Context(), req.IDs...); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *ticketHandler) adminBulkPermanentDeleteTickets(c *echo.Context) error {
	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids is required"})
	}

	if err := h.ticket.AdminPermanentDelete(c.Request().Context(), req.IDs...); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 项目管理 ---

// GET /api/admin/system/tickets/projects
func (h *ticketHandler) adminListProjects(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)
	includeArchived := c.QueryParam("includeArchived") == "true"
	includeDeleted := c.QueryParam("includeDeleted") == "true"

	result, err := h.ticket.AdminListProjects(c.Request().Context(), p.Limit, min(10000, p.Offset), includeArchived, includeDeleted)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// POST /api/admin/system/tickets/projects
func (h *ticketHandler) adminCreateProject(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	var req ticketsvc.CreateProjectRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	result, err := h.ticket.CreateProject(c.Request().Context(), req, current.UserID, current.Username)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusCreated, httputil.Response{Data: result})
}

// PUT /api/admin/system/tickets/projects/:id
func (h *ticketHandler) adminUpdateProject(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	var req ticketsvc.UpdateProjectRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	result, err := h.ticket.UpdateProject(c.Request().Context(), id, req, current.UserID, current.Username)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "project not found"})
		}
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// DELETE /api/admin/system/tickets/projects/:id
func (h *ticketHandler) adminDeleteProject(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.ticket.DeleteProject(c.Request().Context(), id, current.UserID, current.Username); err != nil {
		switch {
		case errors.Is(err, common.ErrNotFound):
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "project not found"})
		default:
			return httputil.RespondError(c, err)
		}
	}

	return c.NoContent(http.StatusNoContent)
}

// PATCH /api/admin/system/tickets/projects/:id/archive
func (h *ticketHandler) adminArchiveProject(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	var req ticketsvc.ArchiveProjectRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	result, err := h.ticket.ArchiveProject(c.Request().Context(), id, req.Archived, current.UserID, current.Username)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "project not found"})
		}
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}
