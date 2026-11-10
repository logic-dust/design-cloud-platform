package api

import (
	"net/http"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/licenses
func (h *licenseHandler) adminLicensesAPI(g *echo.Group) {
	g.GET("", h.adminListLicenseServices)
	g.GET("/:id", h.adminGetLicenseService)
	g.POST("", h.adminCreateLicenseService)
	g.PUT("/:id", h.adminUpdateLicenseService)
	g.DELETE("/:id", h.adminDeleteLicenseService)
}

// --- 获取许可证服务列表（管理员，含未启用）
func (h *licenseHandler) adminListLicenseServices(c *echo.Context) error {
	p := httputil.ParsePage(c)
	list, total, err := h.license.AdminListPage(c.Request().Context(), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, list, total)
}

// --- 获取许可证服务详情（管理员，含未启用）
func (h *licenseHandler) adminGetLicenseService(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	licService, err := h.license.AdminGet(c.Request().Context(), id)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: licService})
}

type createLicenseRequest struct {
	model.LicenseServiceBase
	Enabled *bool `json:"enabled"`
}

// --- 创建许可证服务（管理员）
func (h *licenseHandler) adminCreateLicenseService(c *echo.Context) error {
	var req createLicenseRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	base := req.LicenseServiceBase
	if req.Enabled == nil {
		base.Enabled = true
	} else {
		base.Enabled = *req.Enabled
	}

	licService, err := h.license.CreateFromInput(c.Request().Context(), base)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: licService})
}

// --- 更新许可证服务（管理员）
func (h *licenseHandler) adminUpdateLicenseService(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	var req model.LicenseServiceBase
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	licService, err := h.license.UpdateFromInput(c.Request().Context(), id, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: licService})
}

// --- 删除许可证服务（管理员）
func (h *licenseHandler) adminDeleteLicenseService(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	if err := h.license.Delete(c.Request().Context(), id); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
