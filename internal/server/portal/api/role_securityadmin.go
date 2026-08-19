package api

import (
	"net/http"
	"strings"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/security/roles
func (h *userHandler) adminRolesAPI(g *echo.Group) {
	g.GET("", h.listRoles)
	g.GET("/:role/members", h.listRoleMembers)
}

// GET /roles — 角色目录（固定枚举，无成员）
func (h *userHandler) listRoles(c *echo.Context) error {
	type roleMeta struct {
		Role string `json:"role"`
		Name string `json:"name"`
	}
	roles := make([]roleMeta, 0, len(model.AllRoles()))
	for _, r := range model.AllRoles() {
		roles = append(roles, roleMeta{
			Role: strings.ToLower(r.String()),
			Name: r.String(),
		})
	}
	return httputil.RespondData(c, roles)
}

// GET /roles/:role/members — 分页成员列表
func (h *userHandler) listRoleMembers(c *echo.Context) error {
	parsedRole, err := model.ParseRole(c.Param("role"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	p := httputil.ParsePagination(c, 50, 200)
	members, total, err := h.roleSvc.ListRoleMembers(c.Request().Context(), parsedRole, p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, members, total)
}

// PUT /api/admin/security/users/:user/role
func (h *userHandler) setUserRole(c *echo.Context) error {
	targetUser := c.Param("user")
	var req struct {
		Role string `json:"role"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	parsedRole, err := model.ParseRole(req.Role)
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	updatedUser, err := h.roleSvc.SetUserRole(c.Request().Context(), targetUser, parsedRole)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, updatedUser)
}
