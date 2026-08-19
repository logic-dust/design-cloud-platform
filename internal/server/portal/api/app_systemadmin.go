package api

import (
	"net/http"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/apps
func (h *appHandler) adminAppsAPI(g *echo.Group) {
	g.GET("", h.adminListApps)
	g.GET("/:id", h.adminGetApp)
	g.GET("/:id/users", h.adminListAppUsers)
	g.POST("", h.adminCreateApp)
	g.PUT("/:id", h.adminUpdateApp)
	g.DELETE("/:id", h.adminDeleteApp)
	g.PUT("/:id/users", h.adminSetAppUsers)
	g.PATCH("/:id/users", h.adminPatchAppUsers)
}

// --- 获取所有应用（管理员）
func (h *appHandler) adminListApps(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)
	list, total, err := h.app.List(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, list, total)
}

// --- 获取单个应用详情（管理员）
func (h *appHandler) adminGetApp(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	app, err := h.app.GetByID(c.Request().Context(), id)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, app)
}

// --- 获取应用的用户列表
// GET /api/admin/system/apps/:id/users
func (h *appHandler) adminListAppUsers(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	p := httputil.ParsePagination(c, 50, 200)
	users, err := h.app.ListUsers(c.Request().Context(), appID)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	total := len(users)
	if p.Offset >= total {
		return httputil.RespondPage(c, []string{}, total)
	}
	end := min(p.Offset+p.Limit, total)
	return httputil.RespondPage(c, users[p.Offset:end], total)
}

// --- 创建新应用
func (h *appHandler) adminCreateApp(c *echo.Context) error {
	var req model.AppBase
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	app, err := h.app.Create(c.Request().Context(), req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: app})
}

// --- 更新应用信息
func (h *appHandler) adminUpdateApp(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	var req model.AppBase
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	app, err := h.app.Update(c.Request().Context(), appID, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, app)
}

// --- 删除指定应用
func (h *appHandler) adminDeleteApp(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.app.Delete(c.Request().Context(), appID); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 设置应用的完整用户列表（替换式）
// PUT /api/admin/system/apps/:id/users
// Body: {"users": ["user1", "user2", "user3"]}
func (h *appHandler) adminSetAppUsers(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	var req struct {
		Users []string `json:"users"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.Users) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "users list is required"})
	}
	if err := h.app.SetUsers(c.Request().Context(), appID, req.Users); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 增量更新应用用户（补丁式）
// PATCH /api/admin/system/apps/:id/users
// Body: {"add": ["user4", "user5"], "remove": ["user1"]}
func (h *appHandler) adminPatchAppUsers(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	var req struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.Add) == 0 && len(req.Remove) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "at least one of 'add' or 'remove' is required"})
	}
	if len(req.Add) > 0 {
		if err := h.app.AddUsers(c.Request().Context(), appID, req.Add); err != nil {
			return httputil.RespondError(c, err)
		}
	}
	if len(req.Remove) > 0 {
		if err := h.app.RemoveUsers(c.Request().Context(), appID, req.Remove); err != nil {
			return httputil.RespondError(c, err)
		}
	}
	return c.NoContent(http.StatusNoContent)
}
