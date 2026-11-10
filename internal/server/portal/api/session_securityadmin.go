package api

import (
	"net/http"
	"strings"

	"design-cloud-engine/internal/model"
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

// --- 列出在线 session（管理员）；可选 username 精确过滤单用户
func (h *sessionHandler) adminListSessions(c *echo.Context) error {
	p := httputil.ParsePage(c)
	username := strings.TrimSpace(c.QueryParam("username"))
	var (
		sessions []model.Session
		total    int64
		err      error
	)
	if username != "" {
		sessions, total, err = h.session.ListByUsername(c.Request().Context(), username, p.Limit, p.Offset)
	} else {
		sessions, total, err = h.session.List(c.Request().Context(), p.Limit, p.Offset)
	}
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

	return httputil.RespondNoContent(c)
}

// --- 根据用户名删除所有 session（管理员）
func (h *sessionHandler) deleteSessionsByUsername(c *echo.Context) error {
	username := strings.TrimSpace(c.QueryParam("username"))
	if username == "" {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "username query parameter is required")
	}

	// Delete all sessions for the user and get count
	if err := h.session.Delete(c.Request().Context(), username); err != nil {
		// Note: Delete() already returns nil if user has no sessions
		return httputil.RespondError(c, err)
	}

	return httputil.RespondNoContent(c)
}

func (h *sessionHandler) adminBulkDeleteSessions(c *echo.Context) error {
	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	if len(req.IDs) == 0 {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "ids is required")
	}

	if err := h.session.DeleteByIDs(c.Request().Context(), req.IDs...); err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondNoContent(c)
}
