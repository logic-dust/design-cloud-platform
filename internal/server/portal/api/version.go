package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

type desktopSettingsReader interface {
	GetDesktopSettingsStrict(ctx context.Context) (schema.DesktopSettings, error)
}

type versionHandler struct {
	settings desktopSettingsReader
}

type engineVersionResponse struct {
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	ReleaseDate       string `json:"releaseDate"`
	// MinDesktopVersion projects desktop.minSupportedVersion onto the Engine
	// handshake. The public name is explicit about "desktop client", unlike the
	// settings-domain field which is already scoped under DomainDesktop.
	MinDesktopVersion string `json:"minDesktopVersion"`
}

func newVersionHandler(settings desktopSettingsReader) *versionHandler {
	return &versionHandler{settings: settings}
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

	return httputil.RespondOK(c, engineVersionResponse{
		Version:           config.VERSION,
		Commit:            config.COMMIT,
		ReleaseDate:       config.DATE,
		MinDesktopVersion: desktopSettings.MinSupportedVersion,
	})
}

// requestPlatform returns the platform hint from query params (os or platform).
func requestPlatform(c *echo.Context) string {
	return cmp.Or(c.QueryParam("os"), c.QueryParam("platform"))
}
