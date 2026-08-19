package api

import (
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/alert"

	"github.com/labstack/echo/v5"
)

type alertHandler struct {
	evaluator *alert.Evaluator
}

func newAlertHandler(evaluator *alert.Evaluator) *alertHandler {
	return &alertHandler{evaluator: evaluator}
}

// route: /api/admin/system/alerts
func (h *alertHandler) adminAlertsAPI(g *echo.Group) {
	g.GET("/history", h.getAlertHistory)
}

func (h *alertHandler) getAlertHistory(c *echo.Context) error {
	limit, _ := httputil.ParseOptionalIntQuery(c, "limit")
	if limit <= 0 {
		limit = 100
	}

	if h.evaluator == nil {
		return c.JSON(http.StatusOK, httputil.Response{Data: []any{}})
	}

	items, err := h.evaluator.GetHistory(c.Request().Context(), limit)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: items})
}
