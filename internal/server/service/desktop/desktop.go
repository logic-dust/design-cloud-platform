// Package desktop 是桌面客户端资产分发域：Velopack 主程序发布与 sidecar 插件
// （latest.zip + latest.version）。属于 L1 业务域，非客户端适配层。
package desktop

import (
	"context"
	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	json "design-cloud-engine/internal/infra/jsoncodec"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/server/cache"
)

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
// Download URLs are not included: clients derive them from FileName as /api/desktop/releases/download/<FileName>.
// PackageID is likewise omitted: each asset carries its own PackageId field.
type DesktopReleaseResponse struct {
	Version string  `json:"version"`
	Assets  []Asset `json:"assets"`
}

// CacheTTL bounds staleness of the parsed manifest / plugin caches for
// out-of-band file replacements (e.g. rsync into the release dir without going
// through Portal admin upload/delete). In-band mutations call bumpCacheVersion
// → InvalidateAllCaches immediately (and broadcast via cache_versions), so TTL
// is only a safety net; 24h matches the long-lived settings-style caches.
const CacheTTL = 24 * time.Hour

// Service manages desktop release packages.
type Service struct {
	baseDir          string
	settingsSvc      SettingsReader
	logger           *slog.Logger
	auditSvc         *audit.Service
	cacheVersionRepo *db.CacheVersionRepo

	// writeMu serializes admin write/delete mutations on baseDir and pluginsDir.
	writeMu sync.Mutex

	// cache 是 Velopack 清单的进程内缓存：键为 channel，值随文件替换在 CacheTTL 后过期。
	cache *cache.TTLCache[string, []Asset]
	// pluginCache caches parsed plugin releases per plugin/platform key.
	pluginCache *cache.TTLCache[string, PluginRelease]

	// installerMu guards installerCache. Installer assets are fixed-name files that
	// are replaced in place on release, so entries are invalidated by size + mtime
	// rather than by TTL (a stale SHA256 would break installer verification).
	installerMu    sync.Mutex
	installerCache map[string]cachedInstallerAsset
}

// Option configures optional dependencies on desktop.Service.
type Option func(*Service)

// WithAudit attaches an audit service for recording admin desktop mutations.
func WithAudit(auditSvc *audit.Service) Option {
	return func(s *Service) {
		s.auditSvc = auditSvc
	}
}

// WithCacheInvalidation wires multi-node cache invalidation via cache_versions.
func WithCacheInvalidation(invalidator *cache.Invalidator, repo *db.CacheVersionRepo) Option {
	return func(s *Service) {
		s.cacheVersionRepo = repo
		if invalidator != nil {
			invalidator.Register(db.CacheVersionTopicDesktopReleases, s.InvalidateAllCaches)
		}
	}
}

// cachedInstallerAsset caches the SHA256/size of a first-run installer file.
type cachedInstallerAsset struct {
	modTime time.Time
	size    int64
	asset   InstallerAsset
}

// NewService creates a new desktop service instance.
func NewService(baseDir string, settingsSvc SettingsReader, logger *slog.Logger, opts ...Option) *Service {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		if logger != nil {
			logger.Error("desktop release baseDir is empty")
		}
	} else if err := os.MkdirAll(baseDir, 0755); err != nil {
		if logger != nil {
			logger.Warn("failed to create desktop release directory", "dir", baseDir, "error", err)
		}
	}
	if baseDir != "" {
		pluginsDir := filepath.Join(baseDir, "plugins")
		if err := os.MkdirAll(pluginsDir, 0755); err != nil && logger != nil {
			logger.Warn("failed to create desktop plugins directory", "dir", pluginsDir, "error", err)
		}
	}
	svc := &Service{
		baseDir:        baseDir,
		settingsSvc:    settingsSvc,
		logger:         logger,
		cache:          cache.NewTTLCache[string, []Asset](CacheTTL),
		pluginCache:    cache.NewTTLCache[string, PluginRelease](CacheTTL),
		installerCache: make(map[string]cachedInstallerAsset),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

// InvalidateAllCaches clears all in-process desktop release, plugin, and installer caches.
func (s *Service) InvalidateAllCaches() {
	if s.cache != nil {
		s.cache.Clear()
	}
	if s.pluginCache != nil {
		s.pluginCache.Clear()
	}
	s.installerMu.Lock()
	s.installerCache = make(map[string]cachedInstallerAsset)
	s.installerMu.Unlock()
}

func (s *Service) bumpCacheVersion(ctx context.Context) {
	s.InvalidateAllCaches()
	if s.cacheVersionRepo == nil {
		return
	}
	if err := s.cacheVersionRepo.Bump(ctx, db.CacheVersionTopicDesktopReleases); err != nil && s.logger != nil {
		s.logger.Warn("failed to bump desktop release cache version", "error", err)
	}
}

func (s *Service) logAudit(ctx context.Context, action string, details map[string]any) {
	if s.auditSvc == nil {
		return
	}
	s.auditSvc.LogSystem(ctx, action, slog.LevelInfo, details)
}

// GetBaseDir returns the base directory configured for desktop release files.
func (s *Service) GetBaseDir() string {
	return s.baseDir
}

// defaultMaxAssets is used when settings are unconfigured or the value is invalid.
const defaultMaxAssets = 5

// capAssets returns the effective asset cap: a positive limit is honored only
// when it is below maxAssets, so clients can never exceed the admin's cap.
func capAssets(limit, maxAssets int) int {
	if maxAssets <= 0 {
		maxAssets = defaultMaxAssets
	}
	if limit > 0 {
		return min(limit, maxAssets)
	}
	return maxAssets
}

// maxAssetsFromSettings returns the admin asset cap. Public availability is
// determined by on-disk manifests/files, not by enable flags.
func (s *Service) maxAssetsFromSettings(ctx context.Context) (int, error) {
	if s.settingsSvc == nil {
		return defaultMaxAssets, nil // unconfigured (tests / minimal mode)
	}
	dSettings, err := s.settingsSvc.GetDesktopSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load desktop settings: %w", err)
	}
	return capAssets(0, dSettings.MaxAssets), nil
}

// CanonicalChannel maps a user-supplied platform hint to the on-disk channel
// (win|linux|osx). Velopack manifests use the same middle segment; macOS is "osx".
// An empty hint defaults to "win"; unknown hints are rejected.
func CanonicalChannel(p string) (string, bool) {
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

// canonicalChannel is the unexported alias used inside this package.
func canonicalChannel(p string) (string, bool) { return CanonicalChannel(p) }

// StrictCanonicalChannel maps an explicit platform hint to win|linux|osx, rejecting empty input.
func StrictCanonicalChannel(p string) (string, bool) {
	if strings.TrimSpace(p) == "" {
		return "", false
	}
	return CanonicalChannel(p)
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
			return nil, contract.NewNotFoundError(fmt.Sprintf("release manifest %s not found", manifestFile(channel)))
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
//   - any other value                            → 422 (contract.NewValidationError)
//
// Assets are parsed per channel and cached (TTL CacheTTL). The manifest's file
// order is the contract (Velopack emits newest-first, Full before Delta), and
// the latest version/package ID are taken from the first asset. The result is
// capped at limit (bounded by settings maxAssets).
func (s *Service) GetDesktopManifest(ctx context.Context, limit int, platform string) (DesktopReleaseResponse, error) {
	maxAssets, err := s.maxAssetsFromSettings(ctx)
	if err != nil {
		return DesktopReleaseResponse{}, err
	}

	channel, ok := canonicalChannel(platform)
	if !ok {
		return DesktopReleaseResponse{}, contract.NewNotFoundError(fmt.Sprintf("unsupported platform %q", platform))
	}

	assets, err := s.getCachedAssets(ctx, channel)
	if err != nil {
		return DesktopReleaseResponse{}, err
	}
	if len(assets) == 0 {
		return DesktopReleaseResponse{}, contract.NewNotFoundError("release manifest or assets not found")
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
		return "", contract.NewNotFoundError("no latest release package found")
	}
	return s.ResolveDownloadPath(ctx, manifest.Assets[0].FileName)
}

// ResolveDownloadPath validates filename against path traversal and verifies file existence.
func (s *Service) ResolveDownloadPath(ctx context.Context, fileName string) (string, error) {
	cleanName := filepath.Base(fileName)
	if cleanName == "" || cleanName == "." || cleanName == ".." {
		return "", contract.NewValidationError("invalid filename")
	}
	if cleanName != fileName || strings.Contains(fileName, "..") || strings.ContainsAny(fileName, "/\\") {
		return "", contract.NewValidationError("invalid filename")
	}

	fullPath := filepath.Join(s.baseDir, cleanName)
	info, err := os.Lstat(fullPath)
	if err != nil {
		return "", contract.NewNotFoundError("file not found")
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", contract.NewValidationError("invalid filename")
	}
	return fullPath, nil
}
