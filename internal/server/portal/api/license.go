package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	licensesvc "design-cloud-engine/internal/server/service/license"

	"github.com/labstack/echo/v5"
)

type licenseHandler struct {
	license *licensesvc.Service
}

func newLicenseHandler(svc *service.Container) *licenseHandler {
	return &licenseHandler{license: svc.License}
}

// route: /api/licenses
func (h *licenseHandler) licensesAPI(g *echo.Group) {
	g.GET("", h.listLicenseServices)
	g.GET("/:id", h.getLicenseService)
	g.GET("/:id/features", h.getLicenseFeatures)
	g.GET("/:id/status", h.getLicenseStatus)
}

// --- 获取许可证服务列表
func (h *licenseHandler) listLicenseServices(c *echo.Context) error {
	list, err := h.license.List(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: list})
}

func (h *licenseHandler) getLicenseService(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	licService, err := h.license.Get(c.Request().Context(), id)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: licService})
}

// --- 获取许可证服务的功能列表
func (h *licenseHandler) getLicenseFeatures(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	features, err := h.license.ListFeatures(c.Request().Context(), id)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: features})
}

// --- 获取许可证服务状态
func (h *licenseHandler) getLicenseStatus(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	result, err := h.license.GetStatus(c.Request().Context(), id)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}
