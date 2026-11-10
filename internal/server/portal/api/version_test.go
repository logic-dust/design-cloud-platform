package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract/schema"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

func TestVersionAPI(t *testing.T) {
	tmpDir := t.TempDir()
	desktopSvc := desktop.NewService(tmpDir, nil, nil)
	handler := newDesktopClientHandler(desktopSvc)

	e := echo.New()

	// Test 1: GET /api/desktop/releases/latest (No manifest) -> 404
	req := httptest.NewRequest(http.MethodGet, "/api/desktop/releases/latest", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handler.getReleasesLatest(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 StatusNotFound when manifest missing, got %d", rec.Code)
	}

	// Test 1b: unsupported os -> 404
	reqBad := httptest.NewRequest(http.MethodGet, "/api/desktop/releases/latest?os=android", nil)
	recBad := httptest.NewRecorder()
	cBad := e.NewContext(reqBad, recBad)
	if err := handler.getReleasesLatest(cBad); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recBad.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unsupported os, got %d", recBad.Code)
	}

	// Test 2: Add releases.win.json and test latest (empty os defaults to win)
	manifestContent := `{
		"Assets": [
			{
				"PackageId": "DesignCloudDesktop",
				"Version": "1.0.0",
				"Type": "Full",
				"FileName": "DesignCloudDesktop-1.0.0.nupkg",
				"SHA256": "hash100",
				"Size": 500
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "releases.win.json"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write releases.win.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "DesignCloudDesktop-1.0.0.nupkg"), []byte("package data"), 0644); err != nil {
		t.Fatalf("failed to write nupkg file: %v", err)
	}

	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	if err := handler.getReleasesLatest(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp httputil.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	dataMap, ok := resp.Data.(map[string]any)
	assets, ok2 := dataMap["assets"].([]any)
	if !ok || !ok2 || len(assets) == 0 {
		t.Fatalf("expected assets array in response, got %v", resp.Data)
	}
	if dataMap["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", dataMap["version"])
	}
	if _, hasDownloadURL := dataMap["downloadUrl"]; hasDownloadURL {
		t.Errorf("expected no downloadUrl in response (clients derive it from FileName), got %v", dataMap["downloadUrl"])
	}

	// Test 2b: Bootstrap installer metadata is optional; 404 until published.
	reqBootstrap := httptest.NewRequest(http.MethodGet, "/api/desktop/releases/bootstrap-installer", nil)
	recBootstrap := httptest.NewRecorder()
	cBootstrap := e.NewContext(reqBootstrap, recBootstrap)
	if err := handler.getBootstrapInstaller(cBootstrap); err != nil {
		t.Fatalf("unexpected bootstrap error: %v", err)
	}
	if recBootstrap.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing bootstrap installer, got %d", recBootstrap.Code)
	}

	bootstrapPayload := []byte("bootstrap data")
	if err := os.WriteFile(filepath.Join(tmpDir, desktop.BootstrapInstallerFileName), bootstrapPayload, 0644); err != nil {
		t.Fatalf("failed to write bootstrap installer: %v", err)
	}
	recBootstrap = httptest.NewRecorder()
	cBootstrap = e.NewContext(reqBootstrap, recBootstrap)
	if err := handler.getBootstrapInstaller(cBootstrap); err != nil {
		t.Fatalf("unexpected bootstrap error: %v", err)
	}
	if recBootstrap.Code != http.StatusOK {
		t.Fatalf("expected 200 for bootstrap installer, got %d", recBootstrap.Code)
	}
	var bootstrapResp struct {
		Data desktop.InstallerAsset `json:"data"`
	}
	if err := json.Unmarshal(recBootstrap.Body.Bytes(), &bootstrapResp); err != nil {
		t.Fatalf("failed to parse bootstrap response: %v", err)
	}
	if bootstrapResp.Data.FileName != desktop.BootstrapInstallerFileName {
		t.Fatalf("bootstrap fileName = %q", bootstrapResp.Data.FileName)
	}

	// Test 3: Download file by filename
	reqDL := httptest.NewRequest(http.MethodGet, "/api/desktop/releases/download/DesignCloudDesktop-1.0.0.nupkg", nil)
	recDL := httptest.NewRecorder()
	cDL := e.NewContext(reqDL, recDL)
	cDL.SetPathValues(echo.PathValues{{Name: "filename", Value: "DesignCloudDesktop-1.0.0.nupkg"}})

	if err := handler.downloadReleases(cDL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if recDL.Code != http.StatusOK {
		t.Errorf("expected 200 on download, got %d", recDL.Code)
	}
	if recDL.Body.String() != "package data" {
		t.Errorf("expected body 'package data', got %s", recDL.Body.String())
	}

	// Test 4: Download latest file directly
	reqLatestDL := httptest.NewRequest(http.MethodGet, "/api/desktop/releases/download/latest", nil)
	recLatestDL := httptest.NewRecorder()
	cLatestDL := e.NewContext(reqLatestDL, recLatestDL)
	if err := handler.downloadReleasesLatest(cLatestDL); err != nil {
		t.Fatalf("unexpected error downloading latest: %v", err)
	}
	if recLatestDL.Code != http.StatusOK {
		t.Errorf("expected 200 on latest download, got %d", recLatestDL.Code)
	}
	if recLatestDL.Body.String() != "package data" {
		t.Errorf("expected body 'package data', got %s", recLatestDL.Body.String())
	}

	// Plugin API: missing manifest -> 404
	reqPlugin := httptest.NewRequest(http.MethodGet, "/api/desktop/plugins/turbovnc/latest", nil)
	recPlugin := httptest.NewRecorder()
	cPlugin := e.NewContext(reqPlugin, recPlugin)
	cPlugin.SetPathValues(echo.PathValues{{Name: "name", Value: "turbovnc"}})
	if err := handler.getPluginLatest(cPlugin); err != nil {
		t.Fatalf("unexpected plugin error: %v", err)
	}
	if recPlugin.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing plugin manifest, got %d", recPlugin.Code)
	}

	pluginDir := filepath.Join(tmpDir, "plugins", "turbovnc", "win")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "latest.version"), []byte("3.1.4|abc|4\n"), 0644); err != nil {
		t.Fatalf("write plugin meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "latest.zip"), []byte("zip!"), 0644); err != nil {
		t.Fatalf("write plugin zip: %v", err)
	}

	recPlugin = httptest.NewRecorder()
	cPlugin = e.NewContext(reqPlugin, recPlugin)
	cPlugin.SetPathValues(echo.PathValues{{Name: "name", Value: "turbovnc"}})
	if err := handler.getPluginLatest(cPlugin); err != nil {
		t.Fatalf("unexpected plugin error: %v", err)
	}
	if recPlugin.Code != http.StatusOK {
		t.Fatalf("expected 200 for plugin latest, got %d", recPlugin.Code)
	}

	reqPluginDL := httptest.NewRequest(http.MethodGet, "/api/desktop/plugins/turbovnc/download/latest.zip?os=win", nil)
	recPluginDL := httptest.NewRecorder()
	cPluginDL := e.NewContext(reqPluginDL, recPluginDL)
	cPluginDL.SetPathValues(echo.PathValues{
		{Name: "name", Value: "turbovnc"},
		{Name: "filename", Value: "latest.zip"},
	})
	if err := handler.downloadPlugin(cPluginDL); err != nil {
		t.Fatalf("unexpected plugin download error: %v", err)
	}
	if recPluginDL.Code != http.StatusOK || recPluginDL.Body.String() != "zip!" {
		t.Fatalf("unexpected plugin download response: %d %q", recPluginDL.Code, recPluginDL.Body.String())
	}
}

func TestGetEngineVersion(t *testing.T) {
	rec, err := doVersionRequest(t, stubDesktopSettings{min: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	data := versionData(t, rec)
	for _, field := range []string{"version", "commit", "releaseDate", "desktopEnabled"} {
		if _, ok := data[field]; !ok {
			t.Errorf("expected %s field in response", field)
		}
	}
	if _, ok := data["desktop"]; ok {
		t.Error("minDesktopVersion must be a top-level data field")
	}
	if got := versionMinDesktopVersion(t, rec); got != "" {
		t.Errorf("minDesktopVersion = %#v, want empty string", got)
	}
}

func TestGetEngineVersionDesktopMinSupported(t *testing.T) {
	t.Parallel()

	t.Run("configured", func(t *testing.T) {
		rec, err := doVersionRequest(t, stubDesktopSettings{min: "1.4.0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := versionMinDesktopVersion(t, rec); got != "1.4.0" {
			t.Fatalf("minDesktopVersion = %q, want 1.4.0", got)
		}
		data := versionData(t, rec)
		for _, field := range []string{"version", "commit", "releaseDate", "desktopEnabled"} {
			if _, ok := data[field]; !ok {
				t.Errorf("missing original field %s", field)
			}
		}
		if _, ok := data["desktop"]; ok {
			t.Error("minDesktopVersion must be a top-level data field")
		}
	})

	t.Run("independent of release files", func(t *testing.T) {
		svc := desktop.NewService(t.TempDir(), nil, nil)
		rec, err := doVersionRequestWith(t, svc, stubDesktopSettings{min: "2.0.0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (min version must not depend on release files)", rec.Code)
		}
		if got := versionMinDesktopVersion(t, rec); got != "2.0.0" {
			t.Fatalf("minDesktopVersion = %q, want 2.0.0", got)
		}
	})

	t.Run("publishing disabled", func(t *testing.T) {
		settings := stubDesktopSettings{min: "1.4.0", releasesEnabled: false}
		svc := desktop.NewService(t.TempDir(), settings, nil)
		rec, err := doVersionRequestWith(t, svc, settings)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 when desktop publishing is disabled", rec.Code)
		}
		data := versionData(t, rec)
		if data["desktopEnabled"] != false {
			t.Fatalf("desktopEnabled = %#v, want false", data["desktopEnabled"])
		}
		if got := versionMinDesktopVersion(t, rec); got != "1.4.0" {
			t.Fatalf("minDesktopVersion = %q, want 1.4.0", got)
		}
	})

	t.Run("settings unavailable", func(t *testing.T) {
		rec, err := doVersionRequest(t, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("settings load error", func(t *testing.T) {
		rec, err := doVersionRequest(t, stubDesktopSettings{err: errors.New("db down")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("invalid stored value", func(t *testing.T) {
		rec, err := doVersionRequest(t, stubDesktopSettings{min: "v1.0.0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

type stubDesktopSettings struct {
	min             string
	releasesEnabled bool
	err             error
}

func (s stubDesktopSettings) GetDesktopSettings(context.Context) (schema.DesktopSettings, error) {
	if s.err != nil {
		return schema.DesktopSettings{}, s.err
	}
	return schema.DesktopSettings{
		ReleasesEnabled:     s.releasesEnabled,
		MinSupportedVersion: s.min,
	}, nil
}

func (s stubDesktopSettings) GetDesktopSettingsStrict(ctx context.Context) (schema.DesktopSettings, error) {
	return s.GetDesktopSettings(ctx)
}

func doVersionRequest(t *testing.T, settings desktopSettingsReader) (*httptest.ResponseRecorder, error) {
	t.Helper()
	return doVersionRequestWith(t, nil, settings)
}

func doVersionRequestWith(t *testing.T, svc *desktop.Service, settings desktopSettingsReader) (*httptest.ResponseRecorder, error) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	err := newVersionHandler(svc, settings).getEngineVersion(c)
	return rec, err
}

func versionData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp httputil.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", resp.Data)
	}
	return data
}

func versionMinDesktopVersion(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	data := versionData(t, rec)
	if _, ok := data["desktop"]; ok {
		t.Fatal("minDesktopVersion must be a top-level data field")
	}
	value, ok := data["minDesktopVersion"].(string)
	if !ok {
		t.Fatalf("minDesktopVersion type %T", data["minDesktopVersion"])
	}
	return value
}
