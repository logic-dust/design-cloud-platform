package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

// desktopClientHandler serves Design Cloud Desktop release and plugin downloads.
// Canonical routes: /api/desktop/releases/* and /api/desktop/plugins/*.
type desktopClientHandler struct {
	svc *desktop.Service
}

func newDesktopClientHandler(svc *desktop.Service) *desktopClientHandler {
	return &desktopClientHandler{svc: svc}
}

// route: /api/desktop
func (h *desktopClientHandler) desktopAPI(g *echo.Group) {
	g.GET("/releases/latest", h.getReleasesLatest)
	g.GET("/releases/installer", h.getReleasesInstaller)
	g.GET("/releases/bootstrap-installer", h.getBootstrapInstaller)
	g.GET("/releases/download/latest", h.downloadReleasesLatest)
	g.GET("/releases/download/:filename", h.downloadReleases)
	g.GET("/plugins/:name/latest", h.getPluginLatest)
	g.GET("/plugins/:name/download/latest", h.downloadPluginLatest)
	g.GET("/plugins/:name/download/:filename", h.downloadPlugin)
}

func (h *desktopClientHandler) getReleasesLatest(c *echo.Context) error {
	limit, _ := httputil.ParseOptionalUintQuery(c, "limit")
	manifest, err := h.svc.GetDesktopManifest(c.Request().Context(), int(limit), requestPlatform(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: manifest})
}

// getReleasesInstaller returns the first-run Setup.exe sibling (not Velopack nupkg).
func (h *desktopClientHandler) getReleasesInstaller(c *echo.Context) error {
	asset, err := h.svc.GetInstallerAsset(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: asset})
}

// getBootstrapInstaller returns the optional cert-injected bootstrap installer
// (CA + client config + online Setup download/verification) when published.
func (h *desktopClientHandler) getBootstrapInstaller(c *echo.Context) error {
	asset, err := h.svc.GetBootstrapInstallerAsset(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: asset})
}

func (h *desktopClientHandler) downloadReleasesLatest(c *echo.Context) error {
	filePath, err := h.svc.GetLatestPackagePath(c.Request().Context(), requestPlatform(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}

func (h *desktopClientHandler) downloadReleases(c *echo.Context) error {
	filename := c.Param("filename")
	filePath, err := h.svc.ResolveDownloadPath(c.Request().Context(), filename)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}

func (h *desktopClientHandler) getPluginLatest(c *echo.Context) error {
	name := c.Param("name")
	release, err := h.svc.GetPluginLatest(c.Request().Context(), name, requestPlatform(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: release})
}

func (h *desktopClientHandler) downloadPluginLatest(c *echo.Context) error {
	name := c.Param("name")
	filePath, err := h.svc.GetPluginLatestPackagePath(c.Request().Context(), name, requestPlatform(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}

func (h *desktopClientHandler) downloadPlugin(c *echo.Context) error {
	name := c.Param("name")
	filename := c.Param("filename")
	filePath, err := h.svc.ResolvePluginDownloadPath(c.Request().Context(), name, requestPlatform(c), filename)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}
