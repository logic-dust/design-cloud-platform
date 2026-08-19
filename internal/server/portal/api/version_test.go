package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

func TestVersionAPI(t *testing.T) {
	tmpDir := t.TempDir()
	desktopSvc := desktop.NewService(tmpDir, nil, nil)
	handler := newVersionHandler(desktopSvc)

	e := echo.New()

	// Test 1: GET /api/version/desktop/latest (No manifest) -> 404
	req := httptest.NewRequest(http.MethodGet, "/api/version/desktop/latest", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handler.getDesktopLatest(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 StatusNotFound when manifest missing, got %d", rec.Code)
	}

	// Test 1b: unsupported os -> 404
	reqBad := httptest.NewRequest(http.MethodGet, "/api/version/desktop/latest?os=android", nil)
	recBad := httptest.NewRecorder()
	cBad := e.NewContext(reqBad, recBad)
	if err := handler.getDesktopLatest(cBad); err != nil {
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
	if err := handler.getDesktopLatest(c); err != nil {
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

	// Test 3: Download file by filename
	reqDL := httptest.NewRequest(http.MethodGet, "/api/version/desktop/download/DesignCloudDesktop-1.0.0.nupkg", nil)
	recDL := httptest.NewRecorder()
	cDL := e.NewContext(reqDL, recDL)
	cDL.SetPathValues(echo.PathValues{{Name: "filename", Value: "DesignCloudDesktop-1.0.0.nupkg"}})

	if err := handler.downloadDesktop(cDL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if recDL.Code != http.StatusOK {
		t.Errorf("expected 200 on download, got %d", recDL.Code)
	}
	if recDL.Body.String() != "package data" {
		t.Errorf("expected body 'package data', got %s", recDL.Body.String())
	}

	// Test 4: Download latest file directly (/api/version/desktop/download/latest)
	reqLatestDL := httptest.NewRequest(http.MethodGet, "/api/version/desktop/download/latest", nil)
	recLatestDL := httptest.NewRecorder()
	cLatestDL := e.NewContext(reqLatestDL, recLatestDL)
	if err := handler.downloadDesktopLatest(cLatestDL); err != nil {
		t.Fatalf("unexpected error downloading latest: %v", err)
	}
	if recLatestDL.Code != http.StatusOK {
		t.Errorf("expected 200 on latest download, got %d", recLatestDL.Code)
	}
	if recLatestDL.Body.String() != "package data" {
		t.Errorf("expected body 'package data', got %s", recLatestDL.Body.String())
	}
}
