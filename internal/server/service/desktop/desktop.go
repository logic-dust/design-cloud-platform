package desktop

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/settings/schema"
)

// desktopSettingsProvider abstracts access to desktop-related settings so tests
// can inject a fake without a database. *settings.Service satisfies it.
type desktopSettingsProvider interface {
	GetDesktopSettings(ctx context.Context) (schema.DesktopSettings, error)
}

// Asset describes an item in a Velopack release manifest (releases.{channel}.json).
type Asset struct {
	PackageID string `json:"PackageId"`
	Version   string `json:"Version"`
	Type      string `json:"Type"`
	FileName  string `json:"FileName"`
	SHA1      string `json:"SHA1,omitempty"`
	SHA256    string `json:"SHA256,omitempty"`
	Size      int64  `json:"Size"`
}

// DesktopReleaseResponse is the top-level API response payload returned for latest desktop release.
// Download URLs are not included: clients derive them from FileName as /api/version/desktop/download/<FileName>.
// PackageID is likewise omitted: each asset carries its own PackageId field.
type DesktopReleaseResponse struct {
	Version string  `json:"version"`
	Assets  []Asset `json:"assets"`
}

// CacheTTL bounds staleness of the parsed manifest cache for out-of-band
// file replacements (releases are uploaded manually; in-band changes are rare).
// A few hours of staleness is acceptable and keeps requests off the filesystem.
const CacheTTL = 2 * time.Hour

// Service manages desktop release packages.
type Service struct {
	baseDir     string
	settingsSvc desktopSettingsProvider
	logger      *slog.Logger

	// cache 是解析结果的进程内缓存：键为 channel，值随文件替换在 CacheTTL 后过期。
	cache *cache.TTLCache[string, []Asset]
}

// NewService creates a new desktop service instance.
func NewService(baseDir string, settingsSvc desktopSettingsProvider, logger *slog.Logger) *Service {
	baseDir = cmp.Or(baseDir, "data/releases/desktop")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		if logger != nil {
			logger.Warn("failed to create desktop release directory", "dir", baseDir, "error", err)
		}
	}
	return &Service{
		baseDir:     baseDir,
		settingsSvc: settingsSvc,
		logger:      logger,
		cache:       cache.NewTTLCache[string, []Asset](CacheTTL),
	}
}

// GetBaseDir returns the base directory configured for desktop release files.
func (s *Service) GetBaseDir() string {
	return s.baseDir
}

// IsEnabled returns whether desktop releases are enabled by settings.
func (s *Service) IsEnabled(ctx context.Context) bool {
	if s.settingsSvc == nil {
		return true
	}
	dSettings, err := s.settingsSvc.GetDesktopSettings(ctx)
	if err != nil {
		return false
	}
	return dSettings.ReleasesEnabled
}

// defaultMaxAssets is used when settings are unconfigured or the value is invalid.
const defaultMaxAssets = 5

// capAssets returns the effective asset cap: a positive limit is honored only
// when it is below maxAssets, so clients can never exceed the admin's cap.
func capAssets(limit, maxAssets int) int {
	if maxAssets <= 0 {
		maxAssets = defaultMaxAssets
	}
	if limit > 0 && limit < maxAssets {
		return limit
	}
	return maxAssets
}

func (s *Service) checkAvailableAndGetMaxAssets(ctx context.Context, platform string) (int, error) {
	if s.settingsSvc == nil {
		return defaultMaxAssets, nil // unconfigured (tests / minimal mode)
	}
	dSettings, err := s.settingsSvc.GetDesktopSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load desktop settings: %w", err)
	}
	if !dSettings.ReleasesEnabled {
		return 0, fmt.Errorf("%w: desktop releases disabled by admin", fs.ErrNotExist)
	}
	if platform != "" && !dSettings.IsPlatformAllowed(platform) {
		return 0, fmt.Errorf("%w: release for platform %q disabled by admin", fs.ErrNotExist, platform)
	}
	return capAssets(0, dSettings.MaxAssets), nil
}

// canonicalChannel maps a user-supplied platform hint to the manifest channel,
// i.e. the middle segment of the on-disk manifest file name.
// Velopack names manifests by channel; the default channel is the pack machine's
// OS short name. Note macOS uses "osx", not "macos".
// An empty hint defaults to "win"; unknown hints are rejected.
func canonicalChannel(p string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", "win", "windows":
		return "win", true
	case "linux":
		return "linux", true
	case "macos", "osx", "darwin":
		return "osx", true
	default:
		return "", false
	}
}

// manifestFile returns the on-disk manifest file name for a channel.
func manifestFile(channel string) string {
	return "releases." + channel + ".json"
}

// loadManifestAssets reads and parses the manifest for the given channel.
// Assets are returned in file order; Velopack emits them newest-first with
// Full before Delta at equal versions, and the file is the contract.
func (s *Service) loadManifestAssets(_ context.Context, channel string) ([]Asset, error) {
	manifestPath := filepath.Join(s.baseDir, manifestFile(channel))
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: release manifest %s not found", fs.ErrNotExist, manifestFile(channel))
		}
		return nil, fmt.Errorf("failed to read release manifest: %w", err)
	}
	var manifest struct {
		Assets []Asset `json:"Assets"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse release manifest %s: %w", manifestFile(channel), err)
	}
	return manifest.Assets, nil
}

// getCachedAssets returns the parsed assets for a channel, served from the
// in-process manifest cache (TTL CacheTTL).
func (s *Service) getCachedAssets(ctx context.Context, channel string) ([]Asset, error) {
	return s.cache.Get(ctx, channel, s.loadManifestAssets)
}

// GetDesktopManifest returns the desktop release payload for a channel:
//
//   - platform "" / "win" / "windows"            → releases.win.json
//   - platform "linux"                           → releases.linux.json
//   - platform "macos" / "osx" / "darwin"        → releases.osx.json
//   - any other value                            → 404 (fs.ErrNotExist)
//
// Assets are parsed per channel and cached (TTL CacheTTL). The manifest's file
// order is the contract (Velopack emits newest-first, Full before Delta), and
// the latest version/package ID are taken from the first asset. The result is
// capped at limit (bounded by settings maxAssets).
func (s *Service) GetDesktopManifest(ctx context.Context, limit int, platform string) (DesktopReleaseResponse, error) {
	maxAssets, err := s.checkAvailableAndGetMaxAssets(ctx, platform)
	if err != nil {
		return DesktopReleaseResponse{}, err
	}

	channel, ok := canonicalChannel(platform)
	if !ok {
		return DesktopReleaseResponse{}, fmt.Errorf("%w: unsupported platform %q", fs.ErrNotExist, platform)
	}

	assets, err := s.getCachedAssets(ctx, channel)
	if err != nil {
		return DesktopReleaseResponse{}, err
	}
	if len(assets) == 0 {
		return DesktopReleaseResponse{}, fmt.Errorf("%w: release manifest or assets not found", fs.ErrNotExist)
	}

	// Cap: client-supplied limit can only narrow, never exceed, the admin's maxAssets.
	effectiveLimit := capAssets(limit, maxAssets)
	if len(assets) > effectiveLimit {
		assets = assets[:effectiveLimit]
	}

	return DesktopReleaseResponse{
		Version: assets[0].Version,
		Assets:  assets,
	}, nil
}

// GetLatestPackagePath resolves the file path for the latest release package of
// the given platform. The manifest file order is the contract (Velopack emits
// newest-first with Full before Delta), so the first asset is the latest.
func (s *Service) GetLatestPackagePath(ctx context.Context, platform string) (string, error) {
	manifest, err := s.GetDesktopManifest(ctx, 0, platform)
	if err != nil {
		return "", err
	}
	if len(manifest.Assets) == 0 || manifest.Assets[0].FileName == "" {
		return "", fmt.Errorf("%w: no latest release package found", fs.ErrNotExist)
	}
	return s.ResolveDownloadPath(ctx, manifest.Assets[0].FileName)
}

// ResolveDownloadPath validates filename against path traversal and verifies file existence.
func (s *Service) ResolveDownloadPath(ctx context.Context, fileName string) (string, error) {
	if _, err := s.checkAvailableAndGetMaxAssets(ctx, ""); err != nil {
		return "", err
	}

	cleanName := filepath.Base(fileName)
	if cleanName != fileName || strings.Contains(fileName, "..") || strings.ContainsAny(fileName, "/\\") {
		return "", fmt.Errorf("%w: invalid filename", fs.ErrInvalid)
	}

	fullPath := filepath.Join(s.baseDir, cleanName)
	if _, err := os.Stat(fullPath); err != nil {
		return "", fmt.Errorf("%w: file not found", fs.ErrNotExist)
	}
	return fullPath, nil
}
