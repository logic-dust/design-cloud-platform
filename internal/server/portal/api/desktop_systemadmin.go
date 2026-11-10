package api

import (
	"errors"
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/desktop (PolicySysAdmin + ScopeDesktop)
func (h *desktopClientHandler) adminDesktopAPI(g *echo.Group) {
	g.GET("/releases", h.listAdminReleases)
	g.DELETE("/releases/files/:filename", h.deleteAdminReleaseFile)
	g.POST("/releases/files/batch-delete", h.batchDeleteAdminReleaseFiles)
	g.GET("/plugins", h.listAdminPlugins)
	g.DELETE("/plugins/:name/platforms/:platform", h.deleteAdminPluginPackage)
}

// route: /api/admin/system/desktop (PolicySysAdminFileTransfer + ScopeDesktop)
func (h *desktopClientHandler) adminDesktopTransferAPI(g *echo.Group) {
	g.PUT("/releases/files/:filename", h.uploadAdminReleaseFile)
	g.PUT("/plugins/:name/platforms/:platform", h.uploadAdminPluginPackage)
}

func handleAdminDesktopError(c *echo.Context, err error) error {
	if errors.Is(err, desktop.ErrUploadTooLarge) {
		return httputil.RespondHTTPError(c, http.StatusRequestEntityTooLarge, err.Error())
	}
	return httputil.RespondError(c, err)
}

func (h *desktopClientHandler) listAdminReleases(c *echo.Context) error {
	overview, err := h.svc.GetReleasesOverview(c.Request().Context())
	if err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondOK(c, overview)
}

func (h *desktopClientHandler) uploadAdminReleaseFile(c *echo.Context) error {
	filename := c.Param("filename")
	result, err := h.svc.UploadReleaseFile(
		c.Request().Context(),
		httputil.CurrentUsername(c),
		filename,
		c.Request().Body,
		c.Request().ContentLength,
	)
	if err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondOK(c, result)
}

func (h *desktopClientHandler) deleteAdminReleaseFile(c *echo.Context) error {
	filename := c.Param("filename")
	if err := h.svc.DeleteReleaseFile(c.Request().Context(), httputil.CurrentUsername(c), filename); err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondNoContent(c)
}

func (h *desktopClientHandler) batchDeleteAdminReleaseFiles(c *echo.Context) error {
	var req struct {
		FileNames []string `json:"fileNames"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	if err := h.svc.BatchDeleteReleaseFiles(c.Request().Context(), httputil.CurrentUsername(c), req.FileNames); err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondNoContent(c)
}

func (h *desktopClientHandler) listAdminPlugins(c *echo.Context) error {
	plugins, err := h.svc.ListPlugins(c.Request().Context())
	if err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondOK(c, plugins)
}

func (h *desktopClientHandler) uploadAdminPluginPackage(c *echo.Context) error {
	name := c.Param("name")
	platform := c.Param("platform")
	version := c.QueryParam("version")
	release, err := h.svc.UploadPluginPackage(
		c.Request().Context(),
		httputil.CurrentUsername(c),
		name,
		platform,
		version,
		c.Request().Body,
		c.Request().ContentLength,
	)
	if err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondOK(c, release)
}

func (h *desktopClientHandler) deleteAdminPluginPackage(c *echo.Context) error {
	name := c.Param("name")
	platform := c.Param("platform")
	if err := h.svc.DeletePluginPackage(c.Request().Context(), httputil.CurrentUsername(c), name, platform); err != nil {
		return handleAdminDesktopError(c, err)
	}
	return httputil.RespondNoContent(c)
}
