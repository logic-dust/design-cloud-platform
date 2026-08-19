package file

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
)

func newTestSettingsSvcWithFileSettings(t *testing.T, fs schema.FileSettings) *settings.Service {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(store)
	svc, err := settings.NewService(repos.Setting, &audit.Service{}, nil)
	if err != nil {
		t.Fatalf("settings service: %v", err)
	}
	if err := repos.Setting.Save(context.Background(), schema.DomainFile.String(), fs); err != nil {
		t.Fatalf("save file settings: %v", err)
	}
	return svc
}

func TestFileService_Disabled(t *testing.T) {
	t.Parallel()

	svc := &Service{
		settingsSvc: nil,
	}

	// When settings is nil, default is enabled
	if !svc.IsEnabled(context.Background()) {
		t.Errorf("expected IsEnabled to be true when settingsSvc is nil")
	}

	// When settings has Enabled: false
	settingsSvc := newTestSettingsSvcWithFileSettings(t, schema.FileSettings{
		Enabled:             false,
		DownloadEnabled:     true,
		UploadEnabled:       true,
		DownloadApprovalTTL: 86400,
		UploadMaxSizeBytes:  1024 * 1024,
	})
	svcWithMock := &Service{
		settingsSvc: settingsSvc,
	}

	// Test IsEnabled
	if svcWithMock.IsEnabled(context.Background()) {
		t.Errorf("expected IsEnabled to be false")
	}

	// Test downloadPolicy
	if perm := svcWithMock.downloadPolicy(context.Background()); perm != DownloadPermissionDeny {
		t.Errorf("expected downloadPolicy to return Deny when disabled, got %v", perm)
	}

	// Test CheckUploadPermission
	if err := svcWithMock.CheckUploadPermission(context.Background(), "alice"); !errors.Is(err, ErrFileDisabled) {
		t.Errorf("expected ErrFileDisabled, got %v", err)
	}

	// Test ResolveLocationPath
	if _, err := svcWithMock.ResolveLocationPath(context.Background(), "home", "foo.txt", "alice"); !errors.Is(err, ErrFileDisabled) {
		t.Errorf("expected ErrFileDisabled, got %v", err)
	}

	// Test ListLocations
	if _, err := svcWithMock.ListLocations(context.Background(), "alice"); !errors.Is(err, ErrFileDisabled) {
		t.Errorf("expected ErrFileDisabled, got %v", err)
	}
}

func TestExtraLocation_PolicyNotReadOnly(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	extraRoot := filepath.Join(tmpDir, "shared")

	settingsSvc := newTestSettingsSvcWithFileSettings(t, schema.FileSettings{
		Enabled:             true,
		DownloadEnabled:     true,
		UploadEnabled:       true,
		DownloadApprovalTTL: 86400,
		UploadMaxSizeBytes:  1024 * 1024,
		Locations: []schema.FileLocationSettings{
			{
				Name:    "shared",
				Root:    extraRoot,
				Enabled: true,
			},
		},
	})
	svc := &Service{
		settingsSvc: settingsSvc,
	}

	// 1. GetLocationPolicy for extra location should be Exists=true, ReadOnly=false
	policy, err := svc.GetLocationPolicy(context.Background(), "shared", "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !policy.Exists {
		t.Errorf("expected location to exist")
	}
	if policy.ReadOnly {
		t.Errorf("expected extra location ReadOnly to be false, got true")
	}

	// 2. ensureWritableLocation should succeed for extra location
	if err := svc.ensureWritableLocation(context.Background(), "shared", "alice"); err != nil {
		t.Errorf("expected ensureWritableLocation to succeed for extra location, got %v", err)
	}
}
