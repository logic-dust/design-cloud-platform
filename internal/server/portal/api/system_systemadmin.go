package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"

	"design-cloud-engine/internal/server/service"
	"design-cloud-engine/internal/server/service/system"

	"github.com/labstack/echo/v5"
)

type systemHandler struct {
	system *system.Service
}

func newSystemHandler(svc *service.Container) *systemHandler {
	return &systemHandler{system: svc.System}
}

// statusAPI registers the engine component health endpoint.
// route: /api/admin/system/status
func (h *systemHandler) statusAPI(g *echo.Group) {
	g.GET("", h.getStatus)
}

func (h *systemHandler) getStatus(c *echo.Context) error {
	result := h.system.GetStatus(c.Request().Context())
	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}
