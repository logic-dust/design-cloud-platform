package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	licensesvc "design-cloud-engine/internal/server/service/license"

	"github.com/labstack/echo/v5"
)

type licenseHandler struct {
	license *licensesvc.Service
}

func newLicenseHandler(licenseSvc *licensesvc.Service) *licenseHandler {
	return &licenseHandler{license: licenseSvc}
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
	p := httputil.ParsePage(c)
	list, total, err := h.license.ListPage(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, list, total)
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
