package api

import (
	"net/http"
	"strings"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	usersvc "design-cloud-engine/internal/server/service/user"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/users
func (h *userHandler) adminSystemUsersAPI(g *echo.Group) {
	g.GET("", h.adminListUsers)
	g.GET("/:user/groups", h.adminListUserGroups)
	g.GET("/:user", h.adminGetUser)
	g.POST("/sync", h.adminSyncUsers)
	g.PATCH("/:user", h.adminUpdateUser)
	g.DELETE("/:user", h.adminDeleteUser)
	g.POST("/:user/reset-password", h.adminResetUserPassword)
}

// route: /api/admin/system/groups
func (h *userHandler) adminGroupsAPI(g *echo.Group) {
	g.GET("", h.adminListGroups)
	g.GET("/:group", h.adminGetGroup)
}

// route: /api/admin/system/id-mappings
func (h *userHandler) adminIDMappingsAPI(g *echo.Group) {
	g.POST("/refresh", h.adminRefreshIDMappingCache)
}

// --- 获取所有用户（管理员）
func (h *userHandler) adminListUsers(c *echo.Context) error {
	p := httputil.ParsePage(c)
	filter, err := parseUserListFilter(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	userList, total, err := h.user.List(c.Request().Context(), p.Limit, p.Offset, filter)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, userList, total)
}

func parseUserListFilter(c *echo.Context) (usersvc.ListFilter, error) {
	f := usersvc.ListFilter{Q: strings.TrimSpace(c.QueryParam("q"))}
	if raw := strings.TrimSpace(c.QueryParam("type")); raw != "" {
		tp, err := model.ParseUserType(raw)
		if err != nil {
			return f, contract.NewInvalidParamError("invalid type (want system or app)")
		}
		f.Type = &tp
	}
	if raw := strings.TrimSpace(c.QueryParam("status")); raw != "" {
		st, err := model.ParseUserStatusCode(raw)
		if err != nil {
			return f, contract.NewInvalidParamError("invalid status")
		}
		f.Status = &st
	}
	return f, nil
}

// --- 获取单个用户详情（管理员）
func (h *userHandler) adminGetUser(c *echo.Context) error {
	user, err := h.user.Get(c.Request().Context(), c.Param("user"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, user)
}

// --- 用户所属目录组（管理员）
func (h *userHandler) adminListUserGroups(c *echo.Context) error {
	groups, err := h.user.ListGroupsForUser(c.Request().Context(), c.Param("user"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, groups)
}

// --- 同步系统/LDAP用户（管理员）
func (h *userHandler) adminSyncUsers(c *echo.Context) error {
	if !h.user.TriggerSyncAsync(h.logger.With("component", "user_sync_api")) {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 清空 UID/GID 名字映射缓存（管理员）
func (h *userHandler) adminRefreshIDMappingCache(c *echo.Context) error {
	if err := h.user.RefreshIDMappingCache(c.Request().Context(), httputil.CurrentUsername(c)); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 目录组列表（管理员）
func (h *userHandler) adminListGroups(c *echo.Context) error {
	groups, err := h.user.ListGroups(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, groups)
}

// --- 目录组详情（含成员）
func (h *userHandler) adminGetGroup(c *echo.Context) error {
	group, err := h.user.GetGroup(c.Request().Context(), c.Param("group"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, group)
}

// --- 管理员更新用户记录
func (h *userHandler) adminUpdateUser(c *echo.Context) error {
	session, err := httputil.SessionOrForbidden(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	var req usersvc.AdminUserUpdateRequest
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}

	result, err := h.identity.UpdateUserStatus(c.Request().Context(), session, c.Param("user"), req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, result)
}

// --- 删除用户（管理员）：关闭代理、结束 VNC，并删除连接行与会话存在记录
func (h *userHandler) adminDeleteUser(c *echo.Context) error {
	session, err := httputil.SessionOrForbidden(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	if err := h.identity.DeleteUser(c.Request().Context(), session, c.Param("user")); err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondNoContent(c)
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
	return httputil.RespondOK(c, newPassword)
}
