package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/tickets
func (h *ticketHandler) adminTicketsAPI(g *echo.Group) {
	g.GET("", h.adminListTickets)
	g.POST("/batch-delete", h.adminBulkDeleteTickets)
	g.POST("/batch-delete-permanent", h.adminBulkPermanentDeleteTickets)
	g.DELETE("/:id", h.adminDeleteTicket)
	g.DELETE("/:id/permanent", h.adminPermanentDeleteTicket)
	g.POST("/:id/restore", h.adminRestoreTicket)

	projects := g.Group("/projects")
	projects.GET("", h.adminListProjects)
	projects.POST("", h.adminCreateProject)
	projects.PATCH("/:id", h.adminUpdateProject)
	projects.DELETE("/:id", h.adminDeleteProject)
}

// GET /api/admin/system/tickets
func (h *ticketHandler) adminListTickets(c *echo.Context) error {
	q, err := parseTicketListQuery(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	if q.IncludeDeleted, err = queryBool(c, "includeDeleted"); err != nil {
		return httputil.RespondError(c, err)
	}
	page, err := h.ticket.List(c.Request().Context(), q)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: page})
}

func (h *ticketHandler) adminIDAction(c *echo.Context, fn func(ticketsvc.Actor, uint) error) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	if err := fn(actor, id); err != nil {
		return respondTicketError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *ticketHandler) adminBatchAction(c *echo.Context, fn func(ticketsvc.Actor, []uint) error) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req batchIDsRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	if err := fn(actor, req.IDs); err != nil {
		return respondTicketError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// DELETE /api/admin/system/tickets/:id
func (h *ticketHandler) adminDeleteTicket(c *echo.Context) error {
	return h.adminIDAction(c, func(a ticketsvc.Actor, id uint) error {
		return h.ticket.AdminDelete(c.Request().Context(), a, id)
	})
}

// DELETE /api/admin/system/tickets/:id/permanent
func (h *ticketHandler) adminPermanentDeleteTicket(c *echo.Context) error {
	return h.adminIDAction(c, func(a ticketsvc.Actor, id uint) error {
		return h.ticket.AdminPermanentDelete(c.Request().Context(), a, id)
	})
}

// POST /api/admin/system/tickets/batch-delete
func (h *ticketHandler) adminBulkDeleteTickets(c *echo.Context) error {
	return h.adminBatchAction(c, func(a ticketsvc.Actor, ids []uint) error {
		return h.ticket.AdminDelete(c.Request().Context(), a, ids...)
	})
}

// POST /api/admin/system/tickets/batch-delete-permanent
func (h *ticketHandler) adminBulkPermanentDeleteTickets(c *echo.Context) error {
	return h.adminBatchAction(c, func(a ticketsvc.Actor, ids []uint) error {
		return h.ticket.AdminPermanentDelete(c.Request().Context(), a, ids...)
	})
}

// POST /api/admin/system/tickets/:id/restore
func (h *ticketHandler) adminRestoreTicket(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	t, err := h.ticket.AdminRestore(c.Request().Context(), actor, id)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: t})
}

// --- 项目 ---

// GET /api/admin/system/tickets/projects
func (h *ticketHandler) adminListProjects(c *echo.Context) error {
	limit, offset, err := queryPage(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	includeArchived, err := queryBool(c, "includeArchived")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	includeDeleted, err := queryBool(c, "includeDeleted")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	page, err := h.ticket.ListProjects(c.Request().Context(), limit, offset, includeArchived, includeDeleted)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: page})
}

// POST /api/admin/system/tickets/projects
func (h *ticketHandler) adminCreateProject(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.CreateProjectRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	p, err := h.ticket.CreateProject(c.Request().Context(), actor, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: p})
}

// PATCH /api/admin/system/tickets/projects/:id
func (h *ticketHandler) adminUpdateProject(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.UpdateProjectRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	p, err := h.ticket.UpdateProject(c.Request().Context(), actor, id, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: p})
}

// DELETE /api/admin/system/tickets/projects/:id
func (h *ticketHandler) adminDeleteProject(c *echo.Context) error {
	return h.adminIDAction(c, func(a ticketsvc.Actor, id uint) error {
		return h.ticket.DeleteProject(c.Request().Context(), a, id)
	})
}
