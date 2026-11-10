package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
// Availability is determined by on-disk plugin metadata; there is no settings enable gate.
func (s *Service) GetPluginLatest(ctx context.Context, name, platform string) (PluginRelease, error) {
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

const (
	LatestZipFile     = "latest.zip"
	LatestVersionFile = "latest.version"
)

// FormatLatestVersionLine writes the single-line release descriptor (version|sha256|size).
func FormatLatestVersionLine(version, sha256Hex string, size int64) string {
	return strings.Join([]string{
		strings.TrimSpace(version),
		strings.TrimSpace(sha256Hex),
		strconv.FormatInt(size, 10),
	}, "|")
}

// ParseLatestVersionLine parses version|sha256|size. Empty lines and # comments are ignored;
// the first non-comment line wins.
func ParseLatestVersionLine(content string) (version, sha256Hex string, size int64, err error) {
	for line := range strings.Lines(content) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			return "", "", 0, fmt.Errorf("expected version|sha256|size, got %d fields", len(parts))
		}

		version = strings.TrimSpace(parts[0])
		sha256Hex = strings.TrimSpace(parts[1])
		size, err = strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid size: %w", err)
		}
		if version == "" || sha256Hex == "" {
			return "", "", 0, fmt.Errorf("version and sha256 are required")
		}
		return version, sha256Hex, size, nil
	}

	return "", "", 0, fmt.Errorf("no release line in %s", LatestVersionFile)
}
