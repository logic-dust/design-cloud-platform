package desktop

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/server/service/settings/schema"
)

type mockDesktopSettingsProvider struct {
	settings schema.DesktopSettings
	err      error
}

func (m *mockDesktopSettingsProvider) GetDesktopSettings(ctx context.Context) (schema.DesktopSettings, error) {
	if m.err != nil {
		return schema.DesktopSettings{}, m.err
	}
	return m.settings, nil
}

func TestDesktopService(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mockSettings := &mockDesktopSettingsProvider{
		settings: schema.DesktopSettings{
			ReleasesEnabled: true,
			MaxAssets:       5,
			WindowsEnabled:  true,
			LinuxEnabled:    true,
			MacosEnabled:    true,
		},
	}

	svc := NewService(tmpDir, mockSettings, nil)

	// 1. IsEnabled
	if !svc.IsEnabled(context.Background()) {
		t.Errorf("expected IsEnabled to be true")
	}

	mockSettings.settings.ReleasesEnabled = false
	if svc.IsEnabled(context.Background()) {
		t.Errorf("expected IsEnabled to be false when disabled")
	}

	mockSettings.settings.ReleasesEnabled = true

	// 2. Missing manifest -> 404 (fs.ErrNotExist)
	_, err := svc.GetDesktopManifest(context.Background(), 10, "win")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected fs.ErrNotExist when manifest missing, got %v", err)
	}

	// 3. Create releases.win.json
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
	pkgPath := filepath.Join(tmpDir, "DesignCloudDesktop-1.0.0.nupkg")
	if err := os.WriteFile(pkgPath, []byte("package data"), 0644); err != nil {
		t.Fatalf("failed to write package: %v", err)
	}

	// 4. GetDesktopManifest
	resp, err := svc.GetDesktopManifest(context.Background(), 10, "windows")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Version != "1.0.0" || len(resp.Assets) != 1 {
		t.Errorf("unexpected manifest response: %+v", resp)
	}

	// 5. GetLatestPackagePath
	latestPath, err := svc.GetLatestPackagePath(context.Background(), "windows")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if latestPath != pkgPath {
		t.Errorf("expected %s, got %s", pkgPath, latestPath)
	}

	// 6. ResolveDownloadPath
	resolved, err := svc.ResolveDownloadPath(context.Background(), "DesignCloudDesktop-1.0.0.nupkg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != pkgPath {
		t.Errorf("expected %s, got %s", pkgPath, resolved)
	}

	// 7. Path traversal prevention
	if _, err := svc.ResolveDownloadPath(context.Background(), "../secret"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for path traversal, got %v", err)
	}
}
