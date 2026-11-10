package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
)

type mutableDesktopSettings struct {
	settings schema.DesktopSettings
}

func (m *mutableDesktopSettings) GetDesktopSettings(context.Context) (schema.DesktopSettings, error) {
	return m.settings, nil
}

func buildTestZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func assertValidationStatus(t *testing.T, err error, wantStatus int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with status %d, got nil", wantStatus)
	}
	domainErr, ok := errors.AsType[contract.DomainError](err)
	if !ok {
		t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
	}
	if domainErr.HTTPStatus() != wantStatus {
		t.Fatalf("status = %d, want %d (err=%v)", domainErr.HTTPStatus(), wantStatus, err)
	}
}

func TestAdminReleases_UploadListDeleteAndCacheInvalidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	settings := &mutableDesktopSettings{
		settings: schema.DesktopSettings{
			MaxAssets: 5,
		},
	}
	svc := NewService(dir, settings, nil)

	// 1. Admin overview & upload succeed independently of public download traffic.
	setupData := []byte("setup-v1-bytes")
	setupRes, err := svc.UploadReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, schema.DefaultInstallerFileName, bytes.NewReader(setupData), int64(len(setupData)))
	if err != nil {
		t.Fatalf("upload setup: %v", err)
	}
	wantSum := sha256.Sum256(setupData)
	if setupRes.SHA256 != hex.EncodeToString(wantSum[:]) || setupRes.Size != int64(len(setupData)) {
		t.Fatalf("unexpected upload result: %+v", setupRes)
	}

	nupkgData := []byte("nupkg-v1")
	if _, err := svc.UploadReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, "DesignCloudDesktop-1.2.0-full.nupkg", bytes.NewReader(nupkgData), int64(len(nupkgData))); err != nil {
		t.Fatalf("upload nupkg: %v", err)
	}

	manifestV1 := `{
		"Assets": [
			{"PackageId":"DesignCloudDesktop","Version":"1.2.0","Type":"Full","FileName":"DesignCloudDesktop-1.2.0-full.nupkg","SHA256":"abc","Size":8},
			{"PackageId":"DesignCloudDesktop","Version":"1.2.0","Type":"Delta","FileName":"DesignCloudDesktop-1.2.0-delta.nupkg","SHA256":"def","Size":4}
		]
	}`
	if _, err := svc.UploadReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, "releases.win.json", strings.NewReader(manifestV1), int64(len(manifestV1))); err != nil {
		t.Fatalf("upload manifest v1: %v", err)
	}

	overview, err := svc.GetReleasesOverview(ctx)
	if err != nil {
		t.Fatalf("GetReleasesOverview: %v", err)
	}
	if !overview.Installer.Present || overview.Installer.SHA256 != setupRes.SHA256 {
		t.Fatalf("unexpected installer status: %+v", overview.Installer)
	}
	if overview.BootstrapInstaller.Present {
		t.Fatalf("expected bootstrap installer absent, got %+v", overview.BootstrapInstaller)
	}
	winCh := overview.Channels["win"]
	if !winCh.Present || winCh.Version != "1.2.0" || len(winCh.Assets) != 2 {
		t.Fatalf("unexpected win channel status: %+v", winCh)
	}
	if !winCh.Assets[0].Present || winCh.Assets[1].Present {
		t.Fatalf("expected full asset present and delta absent: %+v", winCh.Assets)
	}
	if len(overview.Files) != 3 {
		t.Fatalf("expected 3 files, got %+v", overview.Files)
	}

	// 2. Populate public caches, then verify upload invalidates them immediately.
	m1, err := svc.GetDesktopManifest(ctx, 5, "win")
	if err != nil || m1.Version != "1.2.0" {
		t.Fatalf("GetDesktopManifest v1: %v %+v", err, m1)
	}
	inst1, err := svc.GetInstallerAsset(ctx)
	if err != nil || inst1.SHA256 != setupRes.SHA256 {
		t.Fatalf("GetInstallerAsset v1: %v %+v", err, inst1)
	}

	manifestV2 := `{"Assets":[{"PackageId":"DesignCloudDesktop","Version":"1.3.0","Type":"Full","FileName":"DesignCloudDesktop-1.3.0-full.nupkg","SHA256":"xyz","Size":10}]}`
	if _, err := svc.UploadReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, "releases.win.json", strings.NewReader(manifestV2), -1); err != nil {
		t.Fatalf("upload manifest v2 (chunked): %v", err)
	}
	m2, err := svc.GetDesktopManifest(ctx, 5, "win")
	if err != nil || m2.Version != "1.3.0" {
		t.Fatalf("expected cache invalidation to 1.3.0, got err=%v resp=%+v", err, m2)
	}

	setupV2 := []byte("setup-v2-bytes-longer")
	if _, err := svc.UploadReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, schema.DefaultInstallerFileName, bytes.NewReader(setupV2), int64(len(setupV2))); err != nil {
		t.Fatalf("upload setup v2: %v", err)
	}
	inst2, err := svc.GetInstallerAsset(ctx)
	if err != nil || inst2.SHA256 == inst1.SHA256 {
		t.Fatalf("expected installer cache to update, got err=%v asset=%+v", err, inst2)
	}

	// 3. Delete single file + batch delete (all-or-nothing on missing).
	if err := svc.DeleteReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, "DesignCloudDesktop-1.2.0-full.nupkg"); err != nil {
		t.Fatalf("DeleteReleaseFile: %v", err)
	}
	assertValidationStatus(t, svc.DeleteReleaseFile(ctx, model.UsernameBuiltinSuperAdmin, "DesignCloudDesktop-1.2.0-full.nupkg"), http.StatusNotFound)

	assertValidationStatus(t, svc.BatchDeleteReleaseFiles(ctx, model.UsernameBuiltinSuperAdmin, []string{"releases.win.json", "does-not-exist.nupkg"}), http.StatusNotFound)
	if _, err := os.Stat(filepath.Join(dir, "releases.win.json")); err != nil {
		t.Fatalf("all-or-nothing batch delete removed file despite missing sibling: %v", err)
	}

	if err := svc.BatchDeleteReleaseFiles(ctx, model.UsernameBuiltinSuperAdmin, []string{"releases.win.json", schema.DefaultInstallerFileName, "releases.win.json"}); err != nil {
		t.Fatalf("BatchDeleteReleaseFiles: %v", err)
	}
	assertValidationStatus(t, func() error {
		_, err := svc.GetDesktopManifest(ctx, 5, "win")
		return err
	}(), http.StatusNotFound)
}

func TestAdminReleases_ValidationAndSizeLimits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	svc := NewService(dir, nil, nil)

	validManifest := `{"Assets":[{"PackageId":"DesignCloudDesktop","Version":"1.0.0","Type":"Full","FileName":"DesignCloudDesktop-1.0.0-full.nupkg","SHA256":"aaa","Size":10}]}`
	if _, err := svc.UploadReleaseFile(ctx, "admin", "releases.win.json", strings.NewReader(validManifest), int64(len(validManifest))); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	badNames := []string{
		"",
		".",
		"..",
		".hidden.exe",
		"../evil.exe",
		"sub/file.exe",
		`sub\file.exe`,
		"plugins",
		"other.json",
		"readme.txt",
		".exe",
	}
	for _, bad := range badNames {
		_, err := svc.UploadReleaseFile(ctx, "admin", bad, strings.NewReader("data"), 4)
		assertValidationStatus(t, err, http.StatusUnprocessableEntity)
	}

	// Bad manifest payloads must not overwrite existing valid manifest.
	badManifests := []string{
		`{not json`,
		`{"Assets":[]}`,
		`{"Assets":[{"PackageId":"X","Version":"1.0.0","Type":"Full","FileName":"../escape.nupkg"}]}`,
		`{"Assets":[{"PackageId":"X","Version":"","Type":"Full","FileName":"ok.nupkg"}]}`,
	}
	for _, bad := range badManifests {
		_, err := svc.UploadReleaseFile(ctx, "admin", "releases.win.json", strings.NewReader(bad), int64(len(bad)))
		assertValidationStatus(t, err, http.StatusUnprocessableEntity)
	}
	gotManifest, err := os.ReadFile(filepath.Join(dir, "releases.win.json"))
	if err != nil || string(gotManifest) != validManifest {
		t.Fatalf("manifest was corrupted by failed upload: err=%v content=%q", err, string(gotManifest))
	}

	// Empty upload & Content-Length mismatch -> 422.
	_, err = svc.UploadReleaseFile(ctx, "admin", "app.exe", strings.NewReader(""), 0)
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	_, err = svc.UploadReleaseFile(ctx, "admin", "app.exe", strings.NewReader("abc"), 10)
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	// Oversized manifest declared length -> ErrUploadTooLarge.
	_, err = svc.UploadReleaseFile(ctx, "admin", "releases.win.json", strings.NewReader("{}"), MaxManifestUploadBytes+1)
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("expected ErrUploadTooLarge, got %v", err)
	}
}

func TestAdminPlugins_UploadListDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	settings := &mutableDesktopSettings{
		settings: schema.DesktopSettings{
			MaxAssets: 5,
		},
	}
	svc := NewService(dir, settings, nil)

	// 1. Explicit version upload.
	zip1 := buildTestZipBytes(t, map[string]string{"vncviewer.exe": "binary1"})
	rel1, err := svc.UploadPluginPackage(ctx, "admin", "turbovnc", "windows", "3.1.4", bytes.NewReader(zip1), int64(len(zip1)))
	if err != nil {
		t.Fatalf("UploadPluginPackage explicit version: %v", err)
	}
	if rel1.Version != "3.1.4" || rel1.OS != "win" || rel1.FileName != LatestZipFile || rel1.Size != int64(len(zip1)) {
		t.Fatalf("unexpected plugin release: %+v", rel1)
	}

	got1, err := svc.GetPluginLatest(ctx, "turbovnc", "win")
	if err != nil || got1.Version != "3.1.4" {
		t.Fatalf("GetPluginLatest: %v %+v", err, got1)
	}

	// 2. Omitted version falls back to VERSION.txt in zip and invalidates cache.
	zip2 := buildTestZipBytes(t, map[string]string{
		"VERSION.txt":   "# comment\n  3.2.0 \n",
		"vncviewer.exe": "binary2",
	})
	rel2, err := svc.UploadPluginPackage(ctx, "admin", "turbovnc", "win", "", bytes.NewReader(zip2), int64(len(zip2)))
	if err != nil {
		t.Fatalf("UploadPluginPackage VERSION.txt: %v", err)
	}
	if rel2.Version != "3.2.0" {
		t.Fatalf("expected version 3.2.0 from VERSION.txt, got %+v", rel2)
	}
	got2, err := svc.GetPluginLatest(ctx, "turbovnc", "win")
	if err != nil || got2.Version != "3.2.0" {
		t.Fatalf("expected plugin cache invalidated to 3.2.0, got err=%v rel=%+v", err, got2)
	}

	// 3. Invalid uploads do not overwrite existing 3.2.0 release.
	_, err = svc.UploadPluginPackage(ctx, "admin", "turbovnc", "win", "3.3.0", strings.NewReader("not-a-zip"), 9)
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	zipNoVer := buildTestZipBytes(t, map[string]string{"bin": "x"})
	_, err = svc.UploadPluginPackage(ctx, "admin", "turbovnc", "win", "", bytes.NewReader(zipNoVer), int64(len(zipNoVer)))
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	_, err = svc.UploadPluginPackage(ctx, "admin", "turbovnc", "win", "3.3.0|bad", bytes.NewReader(zipNoVer), int64(len(zipNoVer)))
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	_, err = svc.UploadPluginPackage(ctx, "admin", "turbovnc", "", "3.3.0", bytes.NewReader(zipNoVer), int64(len(zipNoVer)))
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	_, err = svc.UploadPluginPackage(ctx, "admin", "BadName", "win", "3.3.0", bytes.NewReader(zipNoVer), int64(len(zipNoVer)))
	assertValidationStatus(t, err, http.StatusUnprocessableEntity)

	gotAfterBad, err := svc.GetPluginLatest(ctx, "turbovnc", "win")
	if err != nil || gotAfterBad.Version != "3.2.0" {
		t.Fatalf("failed upload modified existing plugin: err=%v rel=%+v", err, gotAfterBad)
	}

	// 4. ListPlugins & DeletePluginPackage.
	items, err := svc.ListPlugins(ctx)
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(items) != 1 || items[0].Name != "turbovnc" || items[0].Platforms["win"].Version != "3.2.0" {
		t.Fatalf("unexpected ListPlugins result: %+v", items)
	}

	if err := svc.DeletePluginPackage(ctx, "admin", "turbovnc", "win"); err != nil {
		t.Fatalf("DeletePluginPackage: %v", err)
	}
	assertValidationStatus(t, svc.DeletePluginPackage(ctx, "admin", "turbovnc", "win"), http.StatusNotFound)

	items, err = svc.ListPlugins(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("expected empty plugin list after delete, got err=%v items=%+v", err, items)
	}
}

func TestAdminCacheInvalidation_BumpsClusterCacheVersion(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tempDir, "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repos := db.NewRepos(store)
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tempDir, "audit.log"),
		SecurityLogPath: filepath.Join(tempDir, "security.log"),
		NodeID:          "node-a",
		Version:         config.VERSION,
	})
	if err != nil {
		t.Fatalf("audit.NewService: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	invalidator := cache.NewInvalidator(repos.CacheVersion, slog.Default())
	releaseDir := filepath.Join(tempDir, "releases")
	svc := NewService(
		releaseDir,
		nil,
		slog.Default(),
		WithAudit(auditSvc),
		WithCacheInvalidation(invalidator, repos.CacheVersion),
	)

	before, err := repos.CacheVersion.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll before: %v", err)
	}
	beforeVer := before[db.CacheVersionTopicDesktopReleases]

	manifest := `{"Assets":[{"PackageId":"DesignCloudDesktop","Version":"1.0.0","Type":"Full","FileName":"DesignCloudDesktop-1.0.0.nupkg","SHA256":"abc","Size":4}]}`
	if _, err := svc.UploadReleaseFile(context.Background(), model.UsernameBuiltinSuperAdmin, "releases.win.json", strings.NewReader(manifest), int64(len(manifest))); err != nil {
		t.Fatalf("UploadReleaseFile: %v", err)
	}

	after, err := repos.CacheVersion.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll after: %v", err)
	}
	if after[db.CacheVersionTopicDesktopReleases] != beforeVer+1 {
		t.Fatalf("cache version = %d, want %d", after[db.CacheVersionTopicDesktopReleases], beforeVer+1)
	}
}
