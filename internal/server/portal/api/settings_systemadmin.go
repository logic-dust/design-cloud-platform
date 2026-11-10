package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

const maxSettingsBodyBytes int64 = 1 << 20

var allSettingsAccessScopes = []model.AdminScope{
	model.ScopeSettings,
	model.ScopeDesktop,
	model.ScopeConnections,
	model.ScopeSchedulers,
	model.ScopeLicenses,
	model.ScopeStorages,
	model.ScopeNodes,
	model.ScopeFiles,
	model.ScopeUsers,
	model.ScopeSessions,
	model.ScopeBans,
}

var settingsDomainScopes = map[schema.Domain][]model.AdminScope{
	schema.DomainDesktop:    {model.ScopeSettings, model.ScopeDesktop},
	schema.DomainConnection: {model.ScopeSettings, model.ScopeConnections},
	schema.DomainScheduler:  {model.ScopeSettings, model.ScopeSchedulers},
	schema.DomainLicense:    {model.ScopeSettings, model.ScopeLicenses},
	schema.DomainStorage:    {model.ScopeSettings, model.ScopeStorages},
	schema.DomainAgent:      {model.ScopeSettings, model.ScopeNodes},
	schema.DomainCert:       {model.ScopeSettings, model.ScopeNodes},
	schema.DomainFile:       {model.ScopeSettings, model.ScopeFiles},
	schema.DomainAuth:       {model.ScopeSettings, model.ScopeUsers, model.ScopeSessions, model.ScopeBans},
}

// DomainAllowedScopes returns the AdminScopes permitted to read/update the given settings domain.
func DomainAllowedScopes(domain schema.Domain) []model.AdminScope {
	if scopes, ok := settingsDomainScopes[domain]; ok {
		return scopes
	}
	return []model.AdminScope{model.ScopeSettings}
}

func canAccessSettingsDomain(c *echo.Context, domain schema.Domain) bool {
	if c == nil {
		return false
	}
	session, err := echo.ContextGet[*model.Session](c, httputil.CtxSessionKey)
	if err != nil || session == nil {
		// Fail closed: missing session must never bypass domain Scope checks
		// (unit tests that call handlers directly must inject a session).
		return false
	}
	if session.Role == model.RoleSuperAdmin || session.Role > model.RoleNormal {
		return true
	}
	return session.AdminScopes.HasAny(DomainAllowedScopes(domain)...)
}

// route: /api/admin/system/settings
func (h *settingsHandler) adminSettingsAPI(g *echo.Group) {
	g.GET("/domains", h.getSettingsDomains)
	g.GET("/:domain/schema", h.getSettingsSchemaByDomain)
	g.GET("/:domain", h.getSettingsByDomain)
	g.PUT("/:domain", h.updateSettingsByDomain)
}

func (h *settingsHandler) getSettingsDomains(c *echo.Context) error {
	all := schema.GetAllDomains()
	domains := make([]string, 0, len(all))
	for _, name := range all {
		if domain, ok := schema.ParseDomain(name); ok && canAccessSettingsDomain(c, domain) {
			domains = append(domains, name)
		}
	}
	return httputil.RespondOK(c, domains)
}

func (h *settingsHandler) getSettingsByDomain(c *echo.Context) error {
	domainStr := c.Param("domain")
	domain, ok := schema.ParseDomain(domainStr)
	if !ok {
		return httputil.RespondHTTPError(c, http.StatusNotFound, "unknown domain: "+domainStr)
	}
	if !canAccessSettingsDomain(c, domain) {
		return httputil.RespondHTTPError(c, http.StatusForbidden, "insufficient AdminScope")
	}
	settings, err := h.settings.GetByDomain(c.Request().Context(), domain)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return httputil.RespondHTTPError(c, http.StatusNotFound, "unknown domain: "+domainStr)
		}
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, settings)
}

// updateSettingsByDomain accepts a full domain JSON blob, validates it, and persists atomically.
func (h *settingsHandler) updateSettingsByDomain(c *echo.Context) error {
	domainStr := c.Param("domain")
	domain, ok := schema.ParseDomain(domainStr)
	if !ok {
		return httputil.RespondHTTPError(c, http.StatusNotFound, "unknown domain: "+domainStr)
	}
	if !canAccessSettingsDomain(c, domain) {
		return httputil.RespondHTTPError(c, http.StatusForbidden, "insufficient AdminScope")
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxSettingsBodyBytes)
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "read body: "+err.Error())
	}
	if err := h.settings.UpdateByDomain(c.Request().Context(), domain, raw); err != nil {
		switch {
		case errors.Is(err, contract.ErrNotFound):
			return httputil.RespondHTTPError(c, http.StatusNotFound, "unknown domain: "+domainStr)
		default:
			return httputil.RespondError(c, err)
		}
	}
	return httputil.RespondNoContent(c)
}

func (h *settingsHandler) getSettingsSchemaByDomain(c *echo.Context) error {
	domainStr := c.Param("domain")
	domain, ok := schema.ParseDomain(domainStr)
	if !ok {
		return httputil.RespondHTTPError(c, http.StatusNotFound, "unknown domain: "+domainStr)
	}
	if !canAccessSettingsDomain(c, domain) {
		return httputil.RespondHTTPError(c, http.StatusForbidden, "insufficient AdminScope")
	}
	defaultSettings, ok := schema.DefaultSettings(domain)
	if !ok {
		return httputil.RespondError(c, fmt.Errorf("domain %s has no default schema", domainStr))
	}

	jsonSchema := schema.GenerateJSONSchema(defaultSettings)
	return httputil.RespondOK(c, jsonSchema)
}
