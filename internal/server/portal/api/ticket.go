package api

import (
	"cmp"
	"errors"
	"net/http"
	"strconv"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/server/portal/httputil"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"
	"design-cloud-engine/internal/server/service/user"

	"github.com/labstack/echo/v5"
)

type ticketHandler struct {
	ticket *ticketsvc.Service
	user   *user.Service
}

func newTicketHandler(ticket *ticketsvc.Service, user *user.Service) *ticketHandler {
	return &ticketHandler{ticket: ticket, user: user}
}

// route: /api/tickets
func (h *ticketHandler) ticketsAPI(g *echo.Group) {
	g.GET("", h.listTickets)
	g.GET("/:id", h.getTicketByID)
	g.POST("", h.createTicket)
	g.PATCH("/:id", h.updateTicket)
	g.POST("/batch-delete", h.bulkDeleteTickets)
	g.DELETE("/:id", h.deleteTicket)
	g.GET("/users", h.listTicketUsers)
	g.GET("/projects", h.listTicketProjects)
	g.GET("/summary", h.getTicketSummary)
}

// POST /api/tickets
func (h *ticketHandler) createTicket(c *echo.Context) error {
	var req ticketsvc.CreateTicketRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	result, err := h.ticket.Create(c.Request().Context(), req, current.UserID, current.Username)
	if err != nil {
		switch {
		case errors.Is(err, ticketsvc.ErrTitleRequired):
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		case errors.Is(err, common.ErrNotFound):
			return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
		default:
			return httputil.RespondError(c, err)
		}
	}

	return c.JSON(http.StatusCreated, httputil.Response{Data: result})
}

// GET /api/tickets
func (h *ticketHandler) listTickets(c *echo.Context) error {
	filter := ticketsvc.Filter{}

	if projectID, err := httputil.ParseOptionalUintQuery(c, "projectId"); err != nil {
		return err
	} else if projectID > 0 {
		filter.ProjectID = &projectID
	}
	if typeStr := c.QueryParam("type"); typeStr != "" {
		if t, err := strconv.Atoi(typeStr); err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [type] must be an integer"})
		} else {
			filter.Type = &t
		}
	}
	if statusStr := c.QueryParam("status"); statusStr != "" {
		if s, err := strconv.Atoi(statusStr); err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [status] must be an integer"})
		} else {
			filter.Status = &s
		}
	}
	if priorityStr := c.QueryParam("priority"); priorityStr != "" {
		if p, err := strconv.Atoi(priorityStr); err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query [priority] must be an integer"})
		} else {
			filter.Priority = &p
		}
	}
	if reporterID, err := httputil.ParseOptionalUintQuery(c, "reporterId"); err != nil {
		return err
	} else if reporterID > 0 {
		filter.ReporterID = &reporterID
	}
	if assigneeID, err := httputil.ParseOptionalUintQuery(c, "assigneeId"); err != nil {
		return err
	} else if assigneeID > 0 {
		filter.AssigneeID = &assigneeID
	}

	limit, _ := httputil.ParseOptionalIntQuery(c, "limit")
	filter.Limit = cmp.Or(limit, 50)
	filter.Limit = min(200, max(1, filter.Limit))

	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	filter.Offset = min(10000, max(0, offset))

	result, err := h.ticket.List(c.Request().Context(), filter)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// GET /api/tickets/:id
func (h *ticketHandler) getTicketByID(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	result, err := h.ticket.GetByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "ticket not found"})
		}
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// PATCH /api/tickets/:id
func (h *ticketHandler) updateTicket(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	var req ticketsvc.UpdateTicketRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	result, err := h.ticket.Update(c.Request().Context(), id, req, current.Username)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "ticket not found"})
		}
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// DELETE /api/tickets/:id
func (h *ticketHandler) deleteTicket(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	if err := h.ticket.Delete(c.Request().Context(), current.UserID, current.Username, id); err != nil {
		switch {
		case errors.Is(err, ticketsvc.ErrPermissionDenied):
			return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
		case errors.Is(err, common.ErrNotFound):
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "ticket not found"})
		default:
			return httputil.RespondError(c, err)
		}
	}

	return c.NoContent(http.StatusNoContent)
}

func (h *ticketHandler) bulkDeleteTickets(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids is required"})
	}

	if err := h.ticket.Delete(c.Request().Context(), current.UserID, current.Username, req.IDs...); err != nil {
		switch {
		case errors.Is(err, ticketsvc.ErrPermissionDenied):
			return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
		default:
			return httputil.RespondError(c, err)
		}
	}

	return c.NoContent(http.StatusNoContent)
}

// GET /api/tickets/users — 工单指派用户选择器（分页，任意已登录用户可用）
func (h *ticketHandler) listTicketUsers(c *echo.Context) error {
	p := httputil.ParsePagination(c, 200, 500)

	users, total, err := h.user.List(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	type userItem struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}

	items := make([]userItem, 0, len(users))
	for _, u := range users {
		items = append(items, userItem{ID: u.ID, Name: u.Username})
	}

	return httputil.RespondPage(c, items, total)
}

// GET /api/tickets/projects
func (h *ticketHandler) listTicketProjects(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)
	result, err := h.ticket.ListProjects(c.Request().Context(), p.Limit, min(10000, p.Offset), false)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

// GET /api/tickets/summary
func (h *ticketHandler) getTicketSummary(c *echo.Context) error {
	projectID, err := httputil.ParseOptionalUintQuery(c, "projectId")
	if err != nil {
		return err
	}

	result, err := h.ticket.GetSummary(c.Request().Context(), projectID)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}
