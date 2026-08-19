package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/security/sessions
func (h *sessionHandler) adminSessionsAPI(g *echo.Group) {
	g.GET("", h.adminListSessions)
	g.POST("/batch-delete", h.adminBulkDeleteSessions)
	g.DELETE("/:id", h.adminDeleteSession)
	g.DELETE("", h.deleteSessionsByUsername)
}

// --- 列出所有在线 session（管理员）
func (h *sessionHandler) adminListSessions(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)
	sessions, total, err := h.session.List(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, sessions, total)
}

// --- 根据 session ID 删除指定 session（管理员）
func (h *sessionHandler) adminDeleteSession(c *echo.Context) error {
	sessionID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	if err := h.session.DeleteByIDs(c.Request().Context(), sessionID); err != nil {
		return httputil.RespondError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

// --- 根据用户名删除所有 session（管理员）
func (h *sessionHandler) deleteSessionsByUsername(c *echo.Context) error {
	username := c.QueryParam("username")
	if username == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "username query parameter is required"})
	}

	// Delete all sessions for the user and get count
	if err := h.session.Delete(c.Request().Context(), username); err != nil {
		// Note: Delete() already returns nil if user has no sessions
		return httputil.RespondError(c, err)
	}

	return c.NoContent(http.StatusNoContent)
}

func (h *sessionHandler) adminBulkDeleteSessions(c *echo.Context) error {
	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids is required"})
	}

	if err := h.session.DeleteByIDs(c.Request().Context(), req.IDs...); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
