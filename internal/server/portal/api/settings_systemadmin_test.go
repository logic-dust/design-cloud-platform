package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract/schema"
	json "design-cloud-engine/internal/infra/jsoncodec"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/portal/middleware"

	"github.com/labstack/echo/v5"
)

func setupTestAdminSettingsAPI(t *testing.T) (*settingsHandler, *echo.Echo) {
	t.Helper()
	_, settingsSvc := newTestSettingsServiceForAPI(t)
	h := newSettingsHandler(settingsSvc)
	return h, echo.New()
}

func withSuperAdminSession(c *echo.Context) {
	c.Set(httputil.CtxSessionKey, &model.Session{
		Username: model.UsernameBuiltinSuperAdmin,
		Role:     model.RoleSuperAdmin,
	})
}

func TestAdminSettingsAPI_PlatformSchema_NameLengthBounds(t *testing.T) {
	t.Parallel()

	h, e := setupTestAdminSettingsAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/system/settings/platform/schema", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/admin/system/settings/:domain/schema")
	c.SetPathValues(echo.PathValues{{Name: "domain", Value: "platform"}})
	withSuperAdminSession(c)

	if err := h.getSettingsSchemaByDomain(c); err != nil {
		t.Fatalf("getSettingsSchemaByDomain: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	schema := decodeAPIResponse[map[string]any](t, rec)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing: %v", schema)
	}
	nameSchema, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatalf("name property schema missing: %v", props)
	}
	minLen, ok := jsonNumberAsInt(nameSchema["minLength"])
	if !ok || minLen != 1 {
		t.Fatalf("name minLength = %v, want 1", nameSchema["minLength"])
	}
	maxLen, ok := jsonNumberAsInt(nameSchema["maxLength"])
	if !ok || maxLen != 64 {
		t.Fatalf("name maxLength = %v, want 64", nameSchema["maxLength"])
	}
}

func jsonNumberAsInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func TestAdminSettingsAPI_UpdatePlatform_RejectsOversizedName(t *testing.T) {
	t.Parallel()

	h, e := setupTestAdminSettingsAPI(t)
	body := []byte(`{"name":"` + strings.Repeat("a", 65) + `"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/system/settings/platform", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/admin/system/settings/:domain")
	c.SetPathValues(echo.PathValues{{Name: "domain", Value: "platform"}})
	withSuperAdminSession(c)

	if err := h.updateSettingsByDomain(c); err != nil {
		t.Fatalf("updateSettingsByDomain: %v", err)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSettingsAPI_UpdatePlatform_AcceptsValidName(t *testing.T) {
	t.Parallel()

	h, e := setupTestAdminSettingsAPI(t)
	body, err := json.Marshal(map[string]string{"name": "API Test Platform"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/admin/system/settings/platform", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/admin/system/settings/:domain")
	c.SetPathValues(echo.PathValues{{Name: "domain", Value: "platform"}})
	withSuperAdminSession(c)

	if err := h.updateSettingsByDomain(c); err != nil {
		t.Fatalf("updateSettingsByDomain: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSettingsAPI_AllAccessScopesCoverEveryDomain(t *testing.T) {
	t.Parallel()

	for _, name := range schema.GetAllDomains() {
		domain, ok := schema.ParseDomain(name)
		if !ok {
			t.Fatalf("ParseDomain(%q) = false", name)
		}
		for _, scope := range DomainAllowedScopes(domain) {
			if !slices.Contains(allSettingsAccessScopes, scope) {
				t.Fatalf("allSettingsAccessScopes missing scope %v required by domain %s", scope, name)
			}
		}
	}
}

func TestAdminSettingsAPI_DomainScopeFilteringAndAccess(t *testing.T) {
	t.Parallel()

	_, settingsSvc := newTestSettingsServiceForAPI(t)

	var scopes model.AdminScopes
	policy := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(httputil.CtxSessionKey, &model.Session{
				Username:    "scoped-user",
				Role:        model.RoleNormal,
				AdminScopes: scopes,
			})
			return next(c)
		}
	}

	h := buildZeroHandler()
	h.settingsHandler = newSettingsHandler(settingsSvc)
	h.policies = map[middleware.APIPolicy][]echo.MiddlewareFunc{
		middleware.PolicySysAdmin: {policy},
	}
	e := echo.New()
	h.InitRouter(e)

	doReq := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	// 1. ScopeDesktop sees only ["desktop"], accesses desktop domain, rejected on platform.
	scopes = model.AdminScopes(model.ScopeDesktop)
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/domains", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET domains (ScopeDesktop) status=%d body=%s", rec.Code, rec.Body.String())
	} else {
		got := decodeAPIResponse[[]string](t, rec)
		if !slices.Equal(got, []string{"desktop"}) {
			t.Fatalf("GET domains (ScopeDesktop) = %v, want [desktop]", got)
		}
	}
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/desktop", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET desktop (ScopeDesktop) status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/desktop/schema", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET desktop schema (ScopeDesktop) status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doReq(http.MethodPut, "/api/admin/system/settings/desktop", `{"maxAssets":5}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT desktop (ScopeDesktop) status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/system/settings/platform", ""},
		{http.MethodGet, "/api/admin/system/settings/platform/schema", ""},
		{http.MethodPut, "/api/admin/system/settings/platform", `{"name":"x"}`},
	} {
		if rec := doReq(tc.method, tc.path, tc.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s (ScopeDesktop) status=%d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	// 2. ScopeFiles sees only ["file"] and accesses file domain.
	scopes = model.AdminScopes(model.ScopeFiles)
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/domains", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET domains (ScopeFiles) status=%d", rec.Code)
	} else if got := decodeAPIResponse[[]string](t, rec); !slices.Equal(got, []string{"file"}) {
		t.Fatalf("GET domains (ScopeFiles) = %v, want [file]", got)
	}
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/file", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET file (ScopeFiles) status=%d", rec.Code)
	}

	// 3. ScopeNodes sees ["agent", "cert"] and accesses both.
	scopes = model.AdminScopes(model.ScopeNodes)
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/domains", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET domains (ScopeNodes) status=%d", rec.Code)
	} else if got := decodeAPIResponse[[]string](t, rec); !slices.Equal(got, []string{"agent", "cert"}) {
		t.Fatalf("GET domains (ScopeNodes) = %v, want [agent cert]", got)
	}

	// 4. ScopeBans sees ["auth"] and accesses auth domain.
	scopes = model.AdminScopes(model.ScopeBans)
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/domains", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET domains (ScopeBans) status=%d", rec.Code)
	} else if got := decodeAPIResponse[[]string](t, rec); !slices.Equal(got, []string{"auth"}) {
		t.Fatalf("GET domains (ScopeBans) = %v, want [auth]", got)
	}

	// 5. Unrelated scope (ScopeApps) is denied on /settings/domains.
	scopes = model.AdminScopes(model.ScopeApps)
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/domains", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("GET domains (ScopeApps) status=%d, want 403", rec.Code)
	}

	// 6. ScopeSettings sees all 12 domains.
	scopes = model.AdminScopes(model.ScopeSettings)
	if rec := doReq(http.MethodGet, "/api/admin/system/settings/domains", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET domains (ScopeSettings) status=%d", rec.Code)
	} else if got := decodeAPIResponse[[]string](t, rec); !slices.Equal(got, schema.GetAllDomains()) {
		t.Fatalf("GET domains (ScopeSettings) = %v, want %v", got, schema.GetAllDomains())
	}
}
