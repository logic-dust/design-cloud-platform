package api

import (
	"net/http"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

type versionHandler struct {
	desktop *desktop.Service
}

func newVersionHandler(desktopSvc *desktop.Service) *versionHandler {
	return &versionHandler{desktop: desktopSvc}
}

// route: /api/version
func (h *versionHandler) versionAPI(g *echo.Group) {
	g.GET("", h.getEngineVersion)
	g.GET("/desktop/latest", h.getDesktopLatest)
	g.GET("/desktop/download/latest", h.downloadDesktopLatest)
	g.GET("/desktop/download/:filename", h.downloadDesktop)
}

func (h *versionHandler) getEngineVersion(c *echo.Context) error {
	desktopEnabled := true
	if h.desktop != nil {
		desktopEnabled = h.desktop.IsEnabled(c.Request().Context())
	}

	return c.JSON(http.StatusOK, httputil.Response{
		Data: struct {
			Version        string `json:"version"`
			Commit         string `json:"commit"`
			ReleaseDate    string `json:"releaseDate"`
			DesktopEnabled bool   `json:"desktopEnabled"`
		}{
			Version:        config.VERSION,
			Commit:         config.COMMIT,
			ReleaseDate:    config.DATE,
			DesktopEnabled: desktopEnabled,
		},
	})
}

// requestPlatform returns the platform hint from query params (os or platform).
func requestPlatform(c *echo.Context) string {
	if p := c.QueryParam("os"); p != "" {
		return p
	}
	return c.QueryParam("platform")
}

// route: /api/version/desktop/latest
func (h *versionHandler) getDesktopLatest(c *echo.Context) error {
	limit, _ := httputil.ParseOptionalUintQuery(c, "limit")
	manifest, err := h.desktop.GetDesktopManifest(c.Request().Context(), int(limit), requestPlatform(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: manifest})
}

// route: /api/version/desktop/download/latest
func (h *versionHandler) downloadDesktopLatest(c *echo.Context) error {
	filePath, err := h.desktop.GetLatestPackagePath(c.Request().Context(), requestPlatform(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}

// route: /api/version/desktop/download/:filename
func (h *versionHandler) downloadDesktop(c *echo.Context) error {
	filename := c.Param("filename")
	filePath, err := h.desktop.ResolveDownloadPath(c.Request().Context(), filename)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}
