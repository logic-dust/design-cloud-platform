package api

import (
	"context"
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	"design-cloud-engine/internal/server/service/settings/schema"

	"github.com/labstack/echo/v5"
)

type settingsService interface {
	GetPlatformSettings(ctx context.Context) (schema.PlatformSettings, error)
	GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error)
	GetTunnelSettings(ctx context.Context) (schema.TunnelSettings, error)
	GetFileSettings(ctx context.Context) (schema.FileSettings, error)
	GetStorageSettings(ctx context.Context) (schema.StorageSettings, error)
	GetDesktopSettings(ctx context.Context) (schema.DesktopSettings, error)
	GetDashboardSettings(ctx context.Context) (schema.DashboardSettings, error)
	GetByDomain(ctx context.Context, domain schema.Domain) (any, error)
	UpdateByDomain(ctx context.Context, domain schema.Domain, raw []byte) error
}

type settingsHandler struct {
	settings settingsService
}

func newSettingsHandler(svc *service.Container) *settingsHandler {
	return &settingsHandler{settings: svc.Settings}
}

// route: /api/settings
func (h *settingsHandler) settingsAPI(g *echo.Group) {
	g.GET("/platform", h.getPublicPlatformSettings)
}

func (h *settingsHandler) getPublicPlatformSettings(c *echo.Context) error {
	platform, err := h.settings.GetPlatformSettings(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: platform})
}
