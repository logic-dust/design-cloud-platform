package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"design-cloud-engine/internal/contract"
)

var pluginNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// PluginRelease describes the latest sidecar package for a named plugin on one platform.
type PluginRelease struct {
	Version  string `json:"version"`
	OS       string `json:"os"`
	FileName string `json:"fileName"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// ValidatePluginName ensures a plugin route/name is safe for filesystem use.
func ValidatePluginName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if !pluginNamePattern.MatchString(trimmed) {
		return "", contract.NewValidationError("invalid plugin name")
	}
	return trimmed, nil
}

// GetPluginsBaseDir returns the on-disk root for desktop sidecar plugins (…/desktop/plugins).
func (s *Service) GetPluginsBaseDir() string {
	return filepath.Join(s.baseDir, "plugins")
}

func (s *Service) checkPluginAvailable(ctx context.Context, platform string) error {
	if s.settingsSvc == nil {
		return nil
	}
	dSettings, err := s.settingsSvc.GetDesktopSettings(ctx)
	if err != nil {
		return fmt.Errorf("failed to load desktop settings: %w", err)
	}
	if !dSettings.PluginsEnabled {
		return contract.NewNotFoundError("desktop plugins disabled by admin")
	}
	if platform != "" && !dSettings.IsPluginPlatformAllowed(platform) {
		return contract.NewNotFoundError(fmt.Sprintf("plugin release for platform %q disabled by admin", platform))
	}
	return nil
}

func (s *Service) pluginPlatformDir(name, channel string) (string, error) {
	valid, err := ValidatePluginName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.GetPluginsBaseDir(), valid, channel), nil
}

func pluginCacheKey(pluginName, channel string) string {
	return "plugin:" + pluginName + "/" + channel
}

func (s *Service) loadPluginRelease(_ context.Context, pluginName, channel string) (PluginRelease, error) {
	dir, err := s.pluginPlatformDir(pluginName, channel)
	if err != nil {
		return PluginRelease{}, err
	}

	metaPath := filepath.Join(dir, LatestVersionFile)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PluginRelease{}, contract.NewNotFoundError(fmt.Sprintf("%s not found", LatestVersionFile))
		}
		return PluginRelease{}, fmt.Errorf("failed to read %s: %w", LatestVersionFile, err)
	}

	version, sha256Hex, size, err := ParseLatestVersionLine(string(data))
	if err != nil {
		return PluginRelease{}, contract.NewNotFoundError(err.Error())
	}

	zipPath := filepath.Join(dir, LatestZipFile)
	info, err := os.Stat(zipPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PluginRelease{}, contract.NewNotFoundError(fmt.Sprintf("%s not found", LatestZipFile))
		}
		return PluginRelease{}, fmt.Errorf("failed to stat %s: %w", LatestZipFile, err)
	}

	if size > 0 && info.Size() != size {
		return PluginRelease{}, contract.NewNotFoundError(fmt.Sprintf("%s size mismatch", LatestZipFile))
	}

	return PluginRelease{
		Version:  version,
		OS:       channel,
		FileName: LatestZipFile,
		SHA256:   sha256Hex,
		Size:     info.Size(),
	}, nil
}

func (s *Service) getCachedPluginRelease(ctx context.Context, pluginName, channel string) (PluginRelease, error) {
	key := pluginCacheKey(pluginName, channel)
	return s.pluginCache.Get(ctx, key, func(ctx context.Context, _ string) (PluginRelease, error) {
		return s.loadPluginRelease(ctx, pluginName, channel)
	})
}

// GetPluginLatest returns release metadata for a sidecar plugin on the given platform.
func (s *Service) GetPluginLatest(ctx context.Context, name, platform string) (PluginRelease, error) {
	if err := s.checkPluginAvailable(ctx, platform); err != nil {
		return PluginRelease{}, err
	}

	validName, err := ValidatePluginName(name)
	if err != nil {
		return PluginRelease{}, err
	}

	channel, ok := canonicalChannel(platform)
	if !ok {
		return PluginRelease{}, contract.NewNotFoundError(fmt.Sprintf("unsupported platform %q", platform))
	}

	return s.getCachedPluginRelease(ctx, validName, channel)
}

// ResolvePluginDownloadPath returns the on-disk path for a plugin package file.
func (s *Service) ResolvePluginDownloadPath(ctx context.Context, name, platform, fileName string) (string, error) {
	if err := s.checkPluginAvailable(ctx, platform); err != nil {
		return "", err
	}

	channel, ok := canonicalChannel(platform)
	if !ok {
		return "", contract.NewNotFoundError(fmt.Sprintf("unsupported platform %q", platform))
	}

	dir, err := s.pluginPlatformDir(name, channel)
	if err != nil {
		return "", err
	}

	cleanName := filepath.Base(fileName)
	if cleanName != fileName || strings.Contains(fileName, "..") || strings.ContainsAny(fileName, "/\\") {
		return "", contract.NewValidationError("invalid filename")
	}
	if cleanName != LatestZipFile {
		return "", contract.NewNotFoundError("file not found")
	}

	fullPath := filepath.Join(dir, cleanName)
	if _, err := os.Stat(fullPath); err != nil {
		return "", contract.NewNotFoundError("file not found")
	}
	return fullPath, nil
}

// GetPluginLatestPackagePath resolves latest.zip for the given plugin and platform.
func (s *Service) GetPluginLatestPackagePath(ctx context.Context, name, platform string) (string, error) {
	release, err := s.GetPluginLatest(ctx, name, platform)
	if err != nil {
		return "", err
	}
	return s.ResolvePluginDownloadPath(ctx, name, platform, release.FileName)
}
