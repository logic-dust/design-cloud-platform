package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

type desktopSettingsReader interface {
	GetDesktopSettingsStrict(ctx context.Context) (schema.DesktopSettings, error)
}

type versionHandler struct {
	desktopSvc *desktop.Service
	settings   desktopSettingsReader
}

type engineVersionResponse struct {
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	ReleaseDate       string `json:"releaseDate"`
	DesktopEnabled    bool   `json:"desktopEnabled"`
	MinDesktopVersion string `json:"minDesktopVersion"`
}

func newVersionHandler(desktopSvc *desktop.Service, settings desktopSettingsReader) *versionHandler {
	return &versionHandler{desktopSvc: desktopSvc, settings: settings}
}

// route: /api/version
func (h *versionHandler) versionAPI(g *echo.Group) {
	g.GET("", h.getEngineVersion)
}

func (h *versionHandler) getEngineVersion(c *echo.Context) error {
	if h.settings == nil {
		return httputil.RespondError(c, errors.New("desktop settings unavailable"))
	}
	desktopSettings, err := h.settings.GetDesktopSettingsStrict(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, fmt.Errorf("load desktop settings: %v", err))
	}
	if err := schema.ValidateMinSupportedVersion(desktopSettings.MinSupportedVersion); err != nil {
		return httputil.RespondError(c, fmt.Errorf("desktop settings: %v", err))
	}

	desktopEnabled := true
	if h.desktopSvc != nil {
		desktopEnabled = h.desktopSvc.IsEnabled(c.Request().Context())
	}

	return c.JSON(http.StatusOK, httputil.Response{
		Data: engineVersionResponse{
			Version:           config.VERSION,
			Commit:            config.COMMIT,
			ReleaseDate:       config.DATE,
			DesktopEnabled:    desktopEnabled,
			MinDesktopVersion: desktopSettings.MinSupportedVersion,
		},
	})
}

// requestPlatform returns the platform hint from query params (os or platform).
func requestPlatform(c *echo.Context) string {
	return cmp.Or(c.QueryParam("os"), c.QueryParam("platform"))
}
