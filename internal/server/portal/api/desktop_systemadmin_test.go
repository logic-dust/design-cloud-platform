package api

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "design-cloud-engine/internal/infra/jsoncodec"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/portal/middleware"
	"design-cloud-engine/internal/server/service/desktop"

	"github.com/labstack/echo/v5"
)

func buildDesktopTestRouter(t *testing.T) (*echo.Echo, *desktop.Service) {
	t.Helper()
	svc := desktop.NewService(t.TempDir(), nil, nil)

	adminSessionMW := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(httputil.CtxSessionKey, &model.Session{
				Username: model.UsernameBuiltinSuperAdmin,
				Role:     model.RoleSuperAdmin,
			})
			return next(c)
		}
	}

	h := buildZeroHandler()
	h.desktopClientHandler = newDesktopClientHandler(svc)
	h.policies = map[middleware.APIPolicy][]echo.MiddlewareFunc{
		middleware.PolicySysAdmin:             {adminSessionMW},
		middleware.PolicySysAdminFileTransfer: {adminSessionMW},
	}

	e := echo.New()
	h.InitRouter(e)
	return e, svc
}

func makeZipPayload(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestAdminDesktopAPI_ReleasesLifecycle(t *testing.T) {
	t.Parallel()

	e, _ := buildDesktopTestRouter(t)

	// 1. Initial GET /releases -> empty list, installer absent
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/system/desktop/releases", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET releases status=%d body=%s", rec.Code, rec.Body.String())
		}
		overview := decodeAPIResponse[desktop.AdminReleasesOverview](t, rec)
		if overview.Installer.Present || len(overview.Files) != 0 {
			t.Fatalf("unexpected initial overview: %+v", overview)
		}
	}

	// 2. PUT large (>1 MiB) Setup.exe succeeds without hitting DefaultJSONBodyLimitBytes
	largeSetup := bytes.Repeat([]byte("S"), int(middleware.DefaultJSONBodyLimitBytes)+256*1024)
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/releases/files/DesignCloudDesktop-win-Setup.exe", bytes.NewReader(largeSetup))
		req.Header.Set(echo.HeaderContentType, echo.MIMEOctetStream)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT setup status=%d body=%s", rec.Code, rec.Body.String())
		}
		uploaded := decodeAPIResponse[desktop.UploadFileResult](t, rec)
		if uploaded.FileName != "DesignCloudDesktop-win-Setup.exe" || uploaded.Size != int64(len(largeSetup)) || len(uploaded.SHA256) != 64 {
			t.Fatalf("unexpected upload result: %+v", uploaded)
		}
	}

	// 3. PUT nupkg + releases.win.json
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/releases/files/DesignCloudDesktop-1.0.0-full.nupkg", strings.NewReader("nupkg-bytes"))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT nupkg status=%d body=%s", rec.Code, rec.Body.String())
		}

		manifest := `{"Assets":[{"PackageId":"DesignCloudDesktop","Version":"1.0.0","Type":"Full","FileName":"DesignCloudDesktop-1.0.0-full.nupkg","SHA256":"abc","Size":11}]}`
		reqM := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/releases/files/releases.win.json", strings.NewReader(manifest))
		recM := httptest.NewRecorder()
		e.ServeHTTP(recM, reqM)
		if recM.Code != http.StatusOK {
			t.Fatalf("PUT manifest status=%d body=%s", recM.Code, recM.Body.String())
		}
	}

	// 4. GET /releases reflects uploaded files & channel completeness
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/system/desktop/releases", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET releases status=%d body=%s", rec.Code, rec.Body.String())
		}
		overview := decodeAPIResponse[desktop.AdminReleasesOverview](t, rec)
		if !overview.Installer.Present || len(overview.Files) != 3 {
			t.Fatalf("unexpected overview after upload: %+v", overview)
		}
		win := overview.Channels["win"]
		if !win.Present || win.Version != "1.0.0" || len(win.Assets) != 1 || !win.Assets[0].Present {
			t.Fatalf("unexpected win channel overview: %+v", win)
		}
	}

	// 5. Oversized manifest -> 413 Request Entity Too Large
	{
		oversized := strings.Repeat(" ", int(desktop.MaxManifestUploadBytes)+10)
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/releases/files/releases.win.json", strings.NewReader(oversized))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized manifest status=%d, want 413; body=%s", rec.Code, rec.Body.String())
		}
	}

	// 6. DELETE single file + POST batch-delete
	{
		req := httptest.NewRequest(http.MethodDelete, "/api/admin/system/desktop/releases/files/DesignCloudDesktop-1.0.0-full.nupkg", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE release file status=%d body=%s", rec.Code, rec.Body.String())
		}

		body, _ := json.Marshal(map[string]any{
			"fileNames": []string{"DesignCloudDesktop-win-Setup.exe", "releases.win.json"},
		})
		reqBatch := httptest.NewRequest(http.MethodPost, "/api/admin/system/desktop/releases/files/batch-delete", bytes.NewReader(body))
		reqBatch.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		recBatch := httptest.NewRecorder()
		e.ServeHTTP(recBatch, reqBatch)
		if recBatch.Code != http.StatusNoContent {
			t.Fatalf("POST batch-delete status=%d body=%s", recBatch.Code, recBatch.Body.String())
		}
	}
}

func TestAdminDesktopAPI_PluginsLifecycle(t *testing.T) {
	t.Parallel()

	e, _ := buildDesktopTestRouter(t)

	// 1. PUT plugin zip with VERSION.txt and no ?version query
	zipBytes := makeZipPayload(t, map[string]string{
		"VERSION.txt":   "3.1.4\n",
		"vncviewer.exe": "payload",
	})
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/plugins/turbovnc/platforms/win", bytes.NewReader(zipBytes))
		req.Header.Set(echo.HeaderContentType, echo.MIMEOctetStream)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT plugin status=%d body=%s", rec.Code, rec.Body.String())
		}
		rel := decodeAPIResponse[desktop.PluginRelease](t, rec)
		if rel.Version != "3.1.4" || rel.OS != "win" || rel.FileName != desktop.LatestZipFile {
			t.Fatalf("unexpected plugin release: %+v", rel)
		}
	}

	// 2. GET /plugins returns turbovnc
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/system/desktop/plugins", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET plugins status=%d body=%s", rec.Code, rec.Body.String())
		}
		items := decodeAPIResponse[[]desktop.AdminPluginItem](t, rec)
		if len(items) != 1 || items[0].Name != "turbovnc" || items[0].Platforms["win"].Version != "3.1.4" {
			t.Fatalf("unexpected plugins list: %+v", items)
		}
	}

	// 3. Invalid zip -> 422
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/desktop/plugins/turbovnc/platforms/linux?version=3.1.4", strings.NewReader("bad-zip"))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid zip status=%d, want 422; body=%s", rec.Code, rec.Body.String())
		}
	}

	// 4. DELETE plugin platform -> 204, second delete -> 404
	{
		req := httptest.NewRequest(http.MethodDelete, "/api/admin/system/desktop/plugins/turbovnc/platforms/win", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE plugin status=%d body=%s", rec.Code, rec.Body.String())
		}

		req2 := httptest.NewRequest(http.MethodDelete, "/api/admin/system/desktop/plugins/turbovnc/platforms/win", nil)
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusNotFound {
			t.Fatalf("second DELETE plugin status=%d, want 404", rec2.Code)
		}
	}
}
