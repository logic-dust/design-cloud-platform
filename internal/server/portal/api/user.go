package api

import (
	"errors"
	"log/slog"
	"net/http"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	"design-cloud-engine/internal/server/service/role"
	usersvc "design-cloud-engine/internal/server/service/user"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"github.com/labstack/echo/v5"
)

type userHandler struct {
	user    *usersvc.Service
	roleSvc *role.Service
	logger  *slog.Logger
	cfg     *config.Config
}

func newUserHandler(svc *service.Container, cfg *config.Config, logger *slog.Logger) *userHandler {
	return &userHandler{user: svc.User, roleSvc: svc.Role, logger: logger, cfg: cfg}
}

// route: /api/users
func (h *userHandler) usersAPI(g *echo.Group) {
	g.GET("/me", h.getMyProfile)
	g.PATCH("/me/password", h.updateMyPassword)
	g.POST("/id-mappings", h.resolveIDMappings)
}

// --- 批量解析 UID/GID → 名字
func (h *userHandler) resolveIDMappings(c *echo.Context) error {
	var req usersvc.IDMappingsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	result, err := h.user.ResolveIDMappings(c.Request().Context(), req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, result)
}

// --- 获取用户基本信息
func (h *userHandler) getMyProfile(c *echo.Context) error {
	userInfo, err := h.user.Get(c.Request().Context(), httputil.CurrentUsername(c))
	if err != nil {
		if errors.Is(err, userprovider.ErrUserNotFound) || errors.Is(err, common.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "User profile not found"})
		}
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: userInfo})
}

// --- 修改密码
func (h *userHandler) updateMyPassword(c *echo.Context) error {
	var passwordInfo struct {
		Password    string `json:"password" validate:"required"`
		OldPassword string `json:"oldPassword" validate:"required"`
	}
	if err := c.Bind(&passwordInfo); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.user.ChangePassword(c.Request().Context(), httputil.CurrentUsername(c), passwordInfo.OldPassword, passwordInfo.Password); err != nil {
		switch {
		case errors.Is(err, userprovider.ErrUserPasswordIncorrect):
			return c.JSON(http.StatusUnauthorized, httputil.Response{Message: "Current password is incorrect"})
		case errors.Is(err, userprovider.ErrUserPasswordSameAsOld):
			return c.JSON(http.StatusUnprocessableEntity, httputil.Response{Message: "New password cannot be the same as the current password"})
		case errors.Is(err, usersvc.ErrPasswordPolicy):
			return c.JSON(http.StatusUnprocessableEntity, httputil.Response{Message: err.Error()})
		case errors.Is(err, userprovider.ErrUserNotActive):
			return c.JSON(http.StatusForbidden, httputil.Response{Message: "User account is not active"})
		case errors.Is(err, userprovider.ErrLDAPPasswordNotSupported):
			return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
		case errors.Is(err, userprovider.ErrUserNotFound) || errors.Is(err, common.ErrNotFound):
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "User not found"})
		default:
			return httputil.RespondError(c, err)
		}
	}
	return c.NoContent(http.StatusNoContent)
}
