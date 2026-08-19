package api

import (
	"context"
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

type banService interface {
	ListBannedRecords() map[string]any
	Unban(ctx context.Context, key string)
	ClearAllBans(ctx context.Context) int
}

type banHandler struct {
	security banService
}

func newBanHandler(securitySvc banService) *banHandler {
	return &banHandler{security: securitySvc}
}

// route: /api/admin/security/bans
func (h *banHandler) adminBansAPI(g *echo.Group) {
	g.GET("", h.adminListBannedList)
	g.DELETE("/:key", h.adminUnbanRecord)
	g.DELETE("", h.adminClearAllBans)
}

// --- 获取所有封禁记录（管理员）
func (h *banHandler) adminListBannedList(c *echo.Context) error {
	return httputil.RespondData(c, h.security.ListBannedRecords())
}

// --- 解除指定封禁（管理员）
func (h *banHandler) adminUnbanRecord(c *echo.Context) error {
	h.security.Unban(c.Request().Context(), c.Param("key"))
	return c.NoContent(http.StatusNoContent)
}

// --- 清除所有封禁（管理员）
// DELETE /api/admin/security/bans
func (h *banHandler) adminClearAllBans(c *echo.Context) error {
	count := h.security.ClearAllBans(c.Request().Context())
	return httputil.RespondData(c, map[string]any{"count": count})
}
