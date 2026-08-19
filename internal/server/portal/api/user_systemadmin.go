package api

import (
	"context"
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	usersvc "design-cloud-engine/internal/server/service/user"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/users
func (h *userHandler) adminSystemUsersAPI(g *echo.Group) {
	g.GET("", h.adminListUsers)
	g.GET("/groups", h.adminGetGroups)
	g.GET("/groups/:group", h.adminGetGroupMembers)
	g.GET("/:user", h.adminGetUser)
	g.POST("/sync", h.adminSyncUsers)
	g.POST("/id-mappings/refresh", h.adminRefreshIDMappingCache)
	g.PATCH("/:user", h.adminUpdateUser)
	g.POST("/:user/reset-password", h.adminResetUserPassword)
}

// --- 获取所有用户（管理员）
func (h *userHandler) adminListUsers(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)
	userList, total, err := h.user.List(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, userList, total)
}

// --- 获取单个用户详情（管理员）
func (h *userHandler) adminGetUser(c *echo.Context) error {
	user, err := h.user.Get(c.Request().Context(), c.Param("user"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, user)
}

// --- 同步系统/LDAP用户（管理员）
func (h *userHandler) adminSyncUsers(c *echo.Context) error {
	go h.user.SyncUsers(context.Background(), h.logger.With("component", "user_sync_api"))
	return c.NoContent(http.StatusAccepted)
}

// --- 清空 UID/GID 名字映射缓存（管理员）
func (h *userHandler) adminRefreshIDMappingCache(c *echo.Context) error {
	if err := h.user.RefreshIDMappingCache(c.Request().Context(), httputil.CurrentUsername(c)); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 获取用户组列表（管理员）
func (h *userHandler) adminGetGroups(c *echo.Context) error {
	groups, err := h.user.ListUserGroups(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, groups)
}

// --- 获取特定组的成员（管理员）
func (h *userHandler) adminGetGroupMembers(c *echo.Context) error {
	members, err := h.user.GetGroupMembers(c.Request().Context(), c.Param("group"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, members)
}

// --- 管理员更新用户记录
func (h *userHandler) adminUpdateUser(c *echo.Context) error {
	session, err := httputil.SessionOrForbidden(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	var req usersvc.AdminUserUpdateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	result, err := h.user.AdminUpdate(c.Request().Context(), session, c.Param("user"), req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, result)
}

// --- 重置用户密码（管理员）
func (h *userHandler) adminResetUserPassword(c *echo.Context) error {
	session, err := httputil.SessionOrForbidden(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	newPassword, err := h.user.AdminResetPassword(c.Request().Context(), session, c.Param("user"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, newPassword)
}
