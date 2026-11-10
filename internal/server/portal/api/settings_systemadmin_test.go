package api

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func setupTestAdminSettingsAPI(t *testing.T) (*settingsHandler, *echo.Echo) {
	t.Helper()
	_, settingsSvc := newTestSettingsServiceForAPI(t)
	h := newSettingsHandler(settingsSvc)
	return h, echo.New()
}

func TestAdminSettingsAPI_PlatformSchema_NameLengthBounds(t *testing.T) {
	t.Parallel()

	h, e := setupTestAdminSettingsAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/system/settings/platform/schema", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/admin/system/settings/:domain/schema")
	c.SetPathValues(echo.PathValues{{Name: "domain", Value: "platform"}})

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

	if err := h.updateSettingsByDomain(c); err != nil {
		t.Fatalf("updateSettingsByDomain: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}
}
