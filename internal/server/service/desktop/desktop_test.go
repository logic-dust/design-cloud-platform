package desktop

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
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
			MaxAssets: 5,
		},
	}

	svc := NewService(tmpDir, mockSettings, nil)

	// 1. Missing manifest -> 404 (contract.NewNotFoundError)
	_, err := svc.GetDesktopManifest(context.Background(), 10, "win")
	if err == nil {
		t.Errorf("expected error when manifest missing, got nil")
	} else {
		if contractErr, ok := errors.AsType[contract.DomainError](err); !ok {
			t.Errorf("expected contract.DomainError, got %T: %v", err, err)
		} else if contractErr.HTTPStatus() != http.StatusNotFound {
			t.Errorf("expected 404 for missing manifest, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}

	// 2. Create releases.win.json
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

	// 7. Path traversal prevention -> 422 (contract.NewValidationError)
	if _, err := svc.ResolveDownloadPath(context.Background(), "../secret"); err == nil {
		t.Errorf("expected error for path traversal, got nil")
	} else {
		if contractErr, ok := errors.AsType[contract.DomainError](err); !ok {
			t.Errorf("expected contract.DomainError for path traversal, got %T: %v", err, err)
		} else if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for path traversal, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}
}

func TestDesktopPluginRelease(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	svc := NewService(tmpDir, &mockDesktopSettingsProvider{
		settings: schema.DesktopSettings{MaxAssets: 5},
	}, nil)

	platformDir := filepath.Join(tmpDir, "plugins", "turbovnc", "win")
	if err := os.MkdirAll(platformDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(platformDir, LatestVersionFile), []byte("3.1.4|abc|4\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(platformDir, LatestZipFile), []byte("zip!"), 0644); err != nil {
		t.Fatalf("write zip: %v", err)
	}

	release, err := svc.GetPluginLatest(context.Background(), "turbovnc", "win")
	if err != nil || release.Version != "3.1.4" {
		t.Fatalf("GetPluginLatest: %v %+v", err, release)
	}
}

func TestParseFormatLatestVersionLine(t *testing.T) {
	t.Parallel()

	line := FormatLatestVersionLine("3.1.4", "abcDEF", 42)
	v, h, s, err := ParseLatestVersionLine(line)
	if err != nil || v != "3.1.4" || h != "abcDEF" || s != 42 {
		t.Fatalf("round-trip failed: %v %s %s %d", err, v, h, s)
	}
}
