package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

const maxSettingsBodyBytes int64 = 1 << 20

// route: /api/admin/system/settings
func (h *settingsHandler) adminSettingsAPI(g *echo.Group) {
	g.GET("/domains", h.getSettingsDomains)
	g.GET("/:domain/schema", h.getSettingsSchemaByDomain)
	g.GET("/:domain", h.getSettingsByDomain)
	g.PUT("/:domain", h.updateSettingsByDomain)
}

func (h *settingsHandler) getSettingsDomains(c *echo.Context) error {
	domains := schema.GetAllDomains()
	return c.JSON(http.StatusOK, httputil.Response{Data: domains})
}

func (h *settingsHandler) getSettingsByDomain(c *echo.Context) error {
	domainStr := c.Param("domain")
	domain, ok := schema.ParseDomain(domainStr)
	if !ok {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown domain: " + domainStr})
	}
	settings, err := h.settings.GetByDomain(c.Request().Context(), domain)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown domain: " + domainStr})
		}
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: settings})
}

// updateSettingsByDomain accepts a full domain JSON blob, validates it, and persists atomically.
func (h *settingsHandler) updateSettingsByDomain(c *echo.Context) error {
	domainStr := c.Param("domain")
	domain, ok := schema.ParseDomain(domainStr)
	if !ok {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown domain: " + domainStr})
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxSettingsBodyBytes)
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "read body: " + err.Error()})
	}
	if err := h.settings.UpdateByDomain(c.Request().Context(), domain, raw); err != nil {
		switch {
		case errors.Is(err, contract.ErrNotFound):
			return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown domain: " + domainStr})
		default:
			return httputil.RespondError(c, err)
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *settingsHandler) getSettingsSchemaByDomain(c *echo.Context) error {
	domainStr := c.Param("domain")
	domain, ok := schema.ParseDomain(domainStr)
	if !ok {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "unknown domain: " + domainStr})
	}
	defaultSettings, ok := schema.DefaultSettings(domain)
	if !ok {
		return httputil.RespondError(c, fmt.Errorf("domain %s has no default schema", domainStr))
	}

	jsonSchema := schema.GenerateJSONSchema(defaultSettings)
	return c.JSON(http.StatusOK, httputil.Response{Data: jsonSchema})
}
