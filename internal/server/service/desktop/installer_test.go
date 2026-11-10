package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/contract/schema"
)

type fakeDesktopSettings struct {
	settings schema.DesktopSettings
}

func (f fakeDesktopSettings) GetDesktopSettings(context.Context) (schema.DesktopSettings, error) {
	return f.settings, nil
}

func TestGetInstallerAsset_FindsSetupExe(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("setup-bytes")
	path := filepath.Join(dir, "DesignCloudDesktop-win-Setup.exe")
	if err := os.WriteFile(path, payload, 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "releases.win.json"), []byte(`{"Assets":[]}`), 0644)

	svc := NewService(dir, nil, nil)
	asset, err := svc.GetInstallerAsset(context.Background())
	if err != nil {
		t.Fatalf("GetInstallerAsset: %v", err)
	}
	sum := sha256.Sum256(payload)
	if asset.FileName != "DesignCloudDesktop-win-Setup.exe" {
		t.Fatalf("name = %q", asset.FileName)
	}
	if asset.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha = %q", asset.SHA256)
	}
}

func TestGetInstallerAsset_UsesConfiguredFileName(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("custom-setup")
	name := "DesignCloudDesktop-win-x64.exe"
	if err := os.WriteFile(filepath.Join(dir, name), payload, 0644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(dir, fakeDesktopSettings{settings: schema.DesktopSettings{
		ReleasesEnabled:   true,
		WindowsEnabled:    true,
		MaxAssets:         5,
		InstallerFileName: name,
	}}, nil)
	asset, err := svc.GetInstallerAsset(context.Background())
	if err != nil {
		t.Fatalf("GetInstallerAsset: %v", err)
	}
	if asset.FileName != name {
		t.Fatalf("name = %q, want %q", asset.FileName, name)
	}
}

func TestGetInstallerAsset_Missing(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	if _, err := svc.GetInstallerAsset(context.Background()); err == nil {
		t.Fatal("expected not found")
	}
}

func TestGetBootstrapInstallerAsset(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("bootstrap-bytes")
	if err := os.WriteFile(filepath.Join(dir, BootstrapInstallerFileName), payload, 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(dir, nil, nil)
	asset, err := svc.GetBootstrapInstallerAsset(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapInstallerAsset: %v", err)
	}
	sum := sha256.Sum256(payload)
	if asset.FileName != BootstrapInstallerFileName {
		t.Fatalf("name = %q", asset.FileName)
	}
	if asset.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha = %q", asset.SHA256)
	}
	if asset.Size != int64(len(payload)) {
		t.Fatalf("size = %d", asset.Size)
	}
}

func TestGetBootstrapInstallerAsset_Missing(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	if _, err := svc.GetBootstrapInstallerAsset(context.Background()); err == nil {
		t.Fatal("expected not found")
	}
}

func TestInstallerAssetCache_InvalidatesOnFileChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, BootstrapInstallerFileName)
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(dir, nil, nil)
	first, err := svc.GetBootstrapInstallerAsset(context.Background())
	if err != nil {
		t.Fatalf("first read: %v", err)
	}

	if err := os.WriteFile(path, []byte("version-two"), 0644); err != nil {
		t.Fatal(err)
	}
	// Force a distinct mtime so the cache key changes even on coarse filesystems.
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	second, err := svc.GetBootstrapInstallerAsset(context.Background())
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second.SHA256 == first.SHA256 {
		t.Fatal("expected cache to invalidate after installer file change")
	}
	if second.Size != int64(len("version-two")) {
		t.Fatalf("size = %d", second.Size)
	}
}
