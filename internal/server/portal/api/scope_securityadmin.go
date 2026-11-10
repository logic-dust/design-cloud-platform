package api

import (
	"errors"
	"net/http"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	usersvc "design-cloud-engine/internal/server/service/user"

	"design-cloud-engine/internal/contract"
	"github.com/labstack/echo/v5"
)

// route: /api/admin/security/scopes (role-protected, no scope)
func (h *userHandler) adminScopesAPI(g *echo.Group) {
	g.GET("", h.listAdminScopes)
	g.GET("/schemas", h.getScopeSchemas)
	g.PUT("/:username", h.updateScope)
}

func (h *userHandler) getScopeSchemas(c *echo.Context) error {
	schemas := usersvc.GetScopeSchema()
	return c.JSON(http.StatusOK, httputil.Response{Data: schemas})
}

type updateScopesRequest struct {
	Scopes *[]string `json:"scopes"`
}

func (h *userHandler) updateScope(c *echo.Context) error {
	username := c.Param("username")
	if username == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "username is required"})
	}

	var req updateScopesRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if req.Scopes == nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "scopes is required"})
	}

	if err := h.user.SetScopesByNames(c.Request().Context(), username, *req.Scopes); err != nil {
		return scopeErrorResponse(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Message: "admin scope updated"})
}

func (h *userHandler) listAdminScopes(c *echo.Context) error {
	scopes, err := h.user.ListScopes(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}

	type entry struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}
	var result []entry
	for _, d := range scopes {
		result = append(result, entry{Username: d.Username, Scopes: usersvc.ScopeNames(d.AdminScopes)})
	}
	if result == nil {
		result = []entry{}
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

func scopeErrorResponse(c *echo.Context, err error) error {
	if errors.Is(err, contract.ErrNotFound) {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "user not found"})
	}
	if errors.Is(err, usersvc.ErrScopeRequiresNormalRole) ||
		errors.Is(err, model.ErrInvalidScope) {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	return httputil.RespondError(c, err)
}
