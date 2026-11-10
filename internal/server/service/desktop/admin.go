package desktop

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	json "design-cloud-engine/internal/infra/jsoncodec"
)

const (
	// MaxDesktopUploadBytes caps a single desktop release or plugin package upload (2 GiB).
	MaxDesktopUploadBytes int64 = 2 << 30
	// MaxManifestUploadBytes caps a single Velopack release manifest JSON upload (1 MiB).
	MaxManifestUploadBytes int64 = 1 << 20

	uploadTempPrefix        = ".desktop-upload-"
	maxBatchDeleteFileCount = 100
)

// ErrUploadTooLarge indicates the uploaded artifact exceeds the allowed size limit.
var ErrUploadTooLarge = errors.New("desktop package exceeds maximum upload size")

var supportedChannels = []string{"win", "linux", "osx"}

var allowedReleaseExtensions = []string{
	".exe",
	".nupkg",
	".zip",
	".dmg",
	".pkg",
	".appimage",
	".deb",
	".rpm",
	".tar.gz",
}

// ReleaseFileStat describes a regular file under the desktop release root.
type ReleaseFileStat struct {
	FileName string    `json:"fileName"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
}

// UploadFileResult is returned after uploading a desktop release artifact.
type UploadFileResult struct {
	FileName string    `json:"fileName"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	ModTime  time.Time `json:"modTime"`
}

// AdminInstallerStatus summarizes a single first-run or bootstrap installer on disk.
type AdminInstallerStatus struct {
	FileName string     `json:"fileName"`
	Present  bool       `json:"present"`
	Size     int64      `json:"size,omitempty"`
	SHA256   string     `json:"sha256,omitempty"`
	ModTime  *time.Time `json:"modTime,omitempty"`
}

// AdminChannelAsset describes an asset declared in a Velopack manifest and whether it exists on disk.
type AdminChannelAsset struct {
	PackageID string `json:"packageId"`
	Version   string `json:"version"`
	Type      string `json:"type"`
	FileName  string `json:"fileName"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size"`
	Present   bool   `json:"present"`
}

// AdminChannelStatus summarizes one platform channel's Velopack manifest and asset presence.
type AdminChannelStatus struct {
	ManifestFile string              `json:"manifestFile"`
	Present      bool                `json:"present"`
	Version      string              `json:"version,omitempty"`
	Assets       []AdminChannelAsset `json:"assets"`
}

// AdminReleasesOverview aggregates desktop release files, installers, and channel manifests for admins.
type AdminReleasesOverview struct {
	Installer          AdminInstallerStatus          `json:"installer"`
	BootstrapInstaller AdminInstallerStatus          `json:"bootstrapInstaller"`
	Channels           map[string]AdminChannelStatus `json:"channels"`
	Files              []ReleaseFileStat             `json:"files"`
}

// AdminPluginItem summarizes all published platform packages for one named sidecar plugin.
type AdminPluginItem struct {
	Name      string                   `json:"name"`
	Platforms map[string]PluginRelease `json:"platforms"`
}

func (s *Service) ensureBaseDir() error {
	if s.baseDir == "" {
		return errors.New("desktop release baseDir is not configured")
	}
	return nil
}

// GetReleasesOverview returns an admin inventory of desktop release files, installers, and manifests.
// Public download APIs expose the same on-disk truth; this endpoint additionally lists raw files.
func (s *Service) GetReleasesOverview(ctx context.Context) (AdminReleasesOverview, error) {
	if err := s.ensureBaseDir(); err != nil {
		return AdminReleasesOverview{}, err
	}

	installerName := schema.DefaultInstallerFileName
	if s.settingsSvc != nil {
		dSettings, err := s.settingsSvc.GetDesktopSettings(ctx)
		if err != nil {
			return AdminReleasesOverview{}, fmt.Errorf("failed to load desktop settings: %w", err)
		}
		installerName = dSettings.EffectiveInstallerFileName()
	}

	overview := AdminReleasesOverview{
		Installer:          s.buildAdminInstallerStatus(installerName),
		BootstrapInstaller: s.buildAdminInstallerStatus(BootstrapInstallerFileName),
		Channels:           make(map[string]AdminChannelStatus, len(supportedChannels)),
		Files:              []ReleaseFileStat{},
	}

	for _, ch := range supportedChannels {
		overview.Channels[ch] = s.buildAdminChannelStatus(ctx, ch)
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return overview, nil
		}
		return AdminReleasesOverview{}, fmt.Errorf("read desktop release dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, uploadTempPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		overview.Files = append(overview.Files, ReleaseFileStat{
			FileName: name,
			Size:     info.Size(),
			ModTime:  info.ModTime().UTC(),
		})
	}
	slices.SortFunc(overview.Files, func(a, b ReleaseFileStat) int {
		return strings.Compare(a.FileName, b.FileName)
	})

	return overview, nil
}

func (s *Service) buildAdminInstallerStatus(name string) AdminInstallerStatus {
	status := AdminInstallerStatus{FileName: name}
	if !isPlainSafeFileName(name) {
		return status
	}
	full := filepath.Join(s.baseDir, name)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return status
	}
	asset, err := s.loadInstallerAsset(name)
	if err != nil {
		return status
	}
	mt := info.ModTime().UTC()
	status.Present = true
	status.Size = asset.Size
	status.SHA256 = asset.SHA256
	status.ModTime = &mt
	return status
}

func (s *Service) buildAdminChannelStatus(ctx context.Context, channel string) AdminChannelStatus {
	mFile := manifestFile(channel)
	status := AdminChannelStatus{
		ManifestFile: mFile,
		Assets:       []AdminChannelAsset{},
	}
	assets, err := s.loadManifestAssets(ctx, channel)
	if err != nil {
		if !errors.Is(err, contract.ErrNotFound) {
			// Manifest file exists on disk but could not be parsed; expose Present=true
			// so admins can inspect and overwrite/delete it instead of blocking the overview.
			manifestPath := filepath.Join(s.baseDir, mFile)
			if info, statErr := os.Lstat(manifestPath); statErr == nil && info.Mode().IsRegular() {
				status.Present = true
				if s.logger != nil {
					s.logger.Warn("failed to parse existing desktop release manifest", "file", mFile, "error", err)
				}
			}
		}
		return status
	}

	status.Present = true
	if len(assets) > 0 {
		status.Version = assets[0].Version
	}
	for _, asset := range assets {
		present := false
		if isPlainSafeFileName(asset.FileName) {
			assetPath := filepath.Join(s.baseDir, asset.FileName)
			if info, statErr := os.Lstat(assetPath); statErr == nil && info.Mode().IsRegular() {
				present = true
			}
		}
		status.Assets = append(status.Assets, AdminChannelAsset{
			PackageID: asset.PackageID,
			Version:   asset.Version,
			Type:      asset.Type,
			FileName:  asset.FileName,
			SHA256:    asset.SHA256,
			Size:      asset.Size,
			Present:   present,
		})
	}
	return status
}

// UploadReleaseFile streams a desktop release file atomically into baseDir.
func (s *Service) UploadReleaseFile(ctx context.Context, actor, fileName string, r io.Reader, contentLength int64) (UploadFileResult, error) {
	if err := s.ensureBaseDir(); err != nil {
		return UploadFileResult{}, err
	}
	isManifest, err := validateReleaseFileName(fileName)
	if err != nil {
		return UploadFileResult{}, err
	}
	if r == nil {
		return UploadFileResult{}, contract.NewValidationError("upload body is required")
	}

	maxBytes := MaxDesktopUploadBytes
	if isManifest {
		maxBytes = MaxManifestUploadBytes
	}
	if contentLength > maxBytes {
		return UploadFileResult{}, ErrUploadTooLarge
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := os.MkdirAll(s.baseDir, 0755); err != nil {
		return UploadFileResult{}, fmt.Errorf("create desktop release dir: %w", err)
	}

	tmpPath, sha256Hex, size, err := writeStreamToTempFile(s.baseDir, r, contentLength, maxBytes)
	if err != nil {
		return UploadFileResult{}, err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp && tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	if isManifest {
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return UploadFileResult{}, fmt.Errorf("read uploaded manifest: %w", err)
		}
		if err := validateManifestBytes(data); err != nil {
			return UploadFileResult{}, err
		}
	}

	targetPath := filepath.Join(s.baseDir, fileName)
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return UploadFileResult{}, fmt.Errorf("chmod desktop release file: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return UploadFileResult{}, fmt.Errorf("commit desktop release file: %w", err)
	}
	cleanupTmp = false

	modTime := time.Now().UTC()
	if info, err := os.Stat(targetPath); err == nil {
		modTime = info.ModTime().UTC()
	}

	s.bumpCacheVersion(ctx)
	s.logAudit(ctx, "DESKTOP_RELEASE_UPLOAD", map[string]any{
		"actor":    actor,
		"fileName": fileName,
		"size":     size,
		"sha256":   sha256Hex,
	})

	return UploadFileResult{
		FileName: fileName,
		Size:     size,
		SHA256:   sha256Hex,
		ModTime:  modTime,
	}, nil
}

// DeleteReleaseFile deletes a single file from the desktop release root.
func (s *Service) DeleteReleaseFile(ctx context.Context, actor, fileName string) error {
	if err := s.ensureBaseDir(); err != nil {
		return err
	}
	if _, err := validateReleaseFileName(fileName); err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	targetPath := filepath.Join(s.baseDir, fileName)
	info, err := os.Lstat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return contract.NewNotFoundError("file not found")
		}
		return fmt.Errorf("stat desktop release file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return contract.NewValidationError("invalid filename")
	}
	if err := os.Remove(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return contract.NewNotFoundError("file not found")
		}
		return fmt.Errorf("delete desktop release file: %w", err)
	}

	s.bumpCacheVersion(ctx)
	s.logAudit(ctx, "DESKTOP_RELEASE_DELETE", map[string]any{
		"actor":    actor,
		"fileName": fileName,
	})
	return nil
}

// BatchDeleteReleaseFiles deletes up to 100 files from the desktop release root atomically (all-or-nothing check).
func (s *Service) BatchDeleteReleaseFiles(ctx context.Context, actor string, fileNames []string) error {
	if err := s.ensureBaseDir(); err != nil {
		return err
	}
	if len(fileNames) == 0 {
		return contract.NewValidationError("fileNames cannot be empty")
	}
	if len(fileNames) > maxBatchDeleteFileCount {
		return contract.NewValidationError(fmt.Sprintf("fileNames cannot exceed %d items", maxBatchDeleteFileCount))
	}

	uniqueNames := make([]string, 0, len(fileNames))
	seen := make(map[string]struct{}, len(fileNames))
	for _, fn := range fileNames {
		if _, err := validateReleaseFileName(fn); err != nil {
			return err
		}
		if _, ok := seen[fn]; ok {
			continue
		}
		seen[fn] = struct{}{}
		uniqueNames = append(uniqueNames, fn)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	targets := make([]string, 0, len(uniqueNames))
	for _, fn := range uniqueNames {
		targetPath := filepath.Join(s.baseDir, fn)
		info, err := os.Lstat(targetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return contract.NewNotFoundError(fmt.Sprintf("file not found: %s", fn))
			}
			return fmt.Errorf("stat desktop release file %s: %w", fn, err)
		}
		if !info.Mode().IsRegular() {
			return contract.NewValidationError(fmt.Sprintf("invalid filename: %s", fn))
		}
		targets = append(targets, targetPath)
	}

	for _, targetPath := range targets {
		if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete desktop release file: %w", err)
		}
	}

	s.bumpCacheVersion(ctx)
	s.logAudit(ctx, "DESKTOP_RELEASE_BATCH_DELETE", map[string]any{
		"actor":     actor,
		"fileNames": uniqueNames,
	})
	return nil
}

// ListPlugins returns all published sidecar plugins and their per-platform release metadata.
func (s *Service) ListPlugins(ctx context.Context) ([]AdminPluginItem, error) {
	if err := s.ensureBaseDir(); err != nil {
		return nil, err
	}

	pluginsDir := s.GetPluginsBaseDir()
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []AdminPluginItem{}, nil
		}
		return nil, fmt.Errorf("read desktop plugins dir: %w", err)
	}

	items := make([]AdminPluginItem, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := ValidatePluginName(name); err != nil {
			continue
		}
		platforms := make(map[string]PluginRelease)
		for _, ch := range supportedChannels {
			rel, err := s.loadPluginRelease(ctx, name, ch)
			if err == nil {
				platforms[ch] = rel
			}
		}
		if len(platforms) > 0 {
			items = append(items, AdminPluginItem{
				Name:      name,
				Platforms: platforms,
			})
		}
	}

	slices.SortFunc(items, func(a, b AdminPluginItem) int {
		return strings.Compare(a.Name, b.Name)
	})
	return items, nil
}

// UploadPluginPackage streams a plugin zip archive, validates it, extracts VERSION.txt if version is omitted,
// and atomically publishes latest.zip + latest.version under <baseDir>/plugins/<name>/<channel>.
func (s *Service) UploadPluginPackage(ctx context.Context, actor, name, platform, version string, r io.Reader, contentLength int64) (PluginRelease, error) {
	if err := s.ensureBaseDir(); err != nil {
		return PluginRelease{}, err
	}
	validName, err := ValidatePluginName(name)
	if err != nil {
		return PluginRelease{}, err
	}
	channel, ok := StrictCanonicalChannel(platform)
	if !ok {
		return PluginRelease{}, contract.NewValidationError(fmt.Sprintf("unsupported platform %q", platform))
	}

	trimmedVer := strings.TrimSpace(version)
	if trimmedVer != "" {
		trimmedVer, err = validatePluginVersion(trimmedVer)
		if err != nil {
			return PluginRelease{}, err
		}
	}
	if r == nil {
		return PluginRelease{}, contract.NewValidationError("upload body is required")
	}
	if contentLength > MaxDesktopUploadBytes {
		return PluginRelease{}, ErrUploadTooLarge
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	pluginsDir := s.GetPluginsBaseDir()
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return PluginRelease{}, fmt.Errorf("create desktop plugins dir: %w", err)
	}

	tmpPath, sha256Hex, size, err := writeStreamToTempFile(pluginsDir, r, contentLength, MaxDesktopUploadBytes)
	if err != nil {
		return PluginRelease{}, err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp && tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	resolvedVersion, err := validatePluginZipAndVersion(tmpPath, size, trimmedVer)
	if err != nil {
		return PluginRelease{}, err
	}

	platformDir := filepath.Join(pluginsDir, validName, channel)
	if err := os.MkdirAll(platformDir, 0755); err != nil {
		return PluginRelease{}, fmt.Errorf("create plugin platform dir: %w", err)
	}

	targetZip := filepath.Join(platformDir, LatestZipFile)
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return PluginRelease{}, fmt.Errorf("chmod plugin zip: %w", err)
	}
	if err := os.Rename(tmpPath, targetZip); err != nil {
		return PluginRelease{}, fmt.Errorf("commit plugin zip: %w", err)
	}
	cleanupTmp = false

	metaLine := FormatLatestVersionLine(resolvedVersion, sha256Hex, size)
	metaContent := metaLine + "\n"
	targetMeta := filepath.Join(platformDir, LatestVersionFile)
	if err := writeFileAtomic(platformDir, targetMeta, []byte(metaContent)); err != nil {
		return PluginRelease{}, fmt.Errorf("write %s: %w", LatestVersionFile, err)
	}

	s.bumpCacheVersion(ctx)
	s.logAudit(ctx, "DESKTOP_PLUGIN_UPLOAD", map[string]any{
		"actor":    actor,
		"plugin":   validName,
		"platform": channel,
		"version":  resolvedVersion,
		"size":     size,
		"sha256":   sha256Hex,
	})

	return PluginRelease{
		Version:  resolvedVersion,
		OS:       channel,
		FileName: LatestZipFile,
		SHA256:   sha256Hex,
		Size:     size,
	}, nil
}

// DeletePluginPackage deletes the published package for a plugin and platform channel.
func (s *Service) DeletePluginPackage(ctx context.Context, actor, name, platform string) error {
	if err := s.ensureBaseDir(); err != nil {
		return err
	}
	validName, err := ValidatePluginName(name)
	if err != nil {
		return err
	}
	channel, ok := StrictCanonicalChannel(platform)
	if !ok {
		return contract.NewValidationError(fmt.Sprintf("unsupported platform %q", platform))
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	platformDir := filepath.Join(s.GetPluginsBaseDir(), validName, channel)
	zipPath := filepath.Join(platformDir, LatestZipFile)
	metaPath := filepath.Join(platformDir, LatestVersionFile)
	_, zipErr := os.Lstat(zipPath)
	_, metaErr := os.Lstat(metaPath)
	if errors.Is(zipErr, os.ErrNotExist) && errors.Is(metaErr, os.ErrNotExist) {
		return contract.NewNotFoundError("plugin release not found")
	}

	if err := os.RemoveAll(platformDir); err != nil {
		return fmt.Errorf("delete plugin platform dir: %w", err)
	}

	pluginDir := filepath.Join(s.GetPluginsBaseDir(), validName)
	if remaining, err := os.ReadDir(pluginDir); err == nil && len(remaining) == 0 {
		_ = os.Remove(pluginDir)
	}

	s.bumpCacheVersion(ctx)
	s.logAudit(ctx, "DESKTOP_PLUGIN_DELETE", map[string]any{
		"actor":    actor,
		"plugin":   validName,
		"platform": channel,
	})
	return nil
}

func isPlainSafeFileName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return false
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\\x00\r\n\t") {
		return false
	}
	return filepath.Base(name) == name
}

func validateReleaseFileName(fileName string) (isManifest bool, err error) {
	if !isPlainSafeFileName(fileName) {
		return false, contract.NewValidationError("invalid filename")
	}
	if strings.EqualFold(fileName, "plugins") {
		return false, contract.NewValidationError("invalid filename: plugins is reserved")
	}

	lower := strings.ToLower(fileName)
	if strings.HasSuffix(lower, ".json") {
		switch fileName {
		case "releases.win.json", "releases.linux.json", "releases.osx.json":
			return true, nil
		default:
			return false, contract.NewValidationError("json uploads are limited to releases.win.json, releases.linux.json, and releases.osx.json")
		}
	}

	for _, ext := range allowedReleaseExtensions {
		if strings.HasSuffix(lower, ext) && len(lower) > len(ext) {
			return false, nil
		}
	}
	return false, contract.NewValidationError("unsupported desktop release file extension")
}

func validateManifestBytes(data []byte) error {
	var manifest struct {
		Assets []Asset `json:"Assets"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return contract.NewValidationError("invalid Velopack manifest JSON: " + err.Error())
	}
	if len(manifest.Assets) == 0 {
		return contract.NewValidationError("Velopack manifest must contain at least one asset")
	}
	for _, asset := range manifest.Assets {
		fn := strings.TrimSpace(asset.FileName)
		if !isPlainSafeFileName(fn) {
			return contract.NewValidationError("invalid asset fileName in Velopack manifest")
		}
		if strings.TrimSpace(asset.Version) == "" {
			return contract.NewValidationError("asset version is required in Velopack manifest")
		}
	}
	return nil
}

func validatePluginVersion(version string) (string, error) {
	v := strings.TrimSpace(version)
	if v == "" {
		return "", contract.NewValidationError("plugin version is required")
	}
	if len(v) > 64 {
		return "", contract.NewValidationError("plugin version exceeds 64 characters")
	}
	if strings.ContainsAny(v, "|\r\n\t\x00") {
		return "", contract.NewValidationError("plugin version contains invalid characters")
	}
	return v, nil
}

func validatePluginZipAndVersion(zipPath string, size int64, explicitVersion string) (string, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return "", fmt.Errorf("open uploaded zip: %w", err)
	}
	defer func() { _ = f.Close() }()

	zr, err := zip.NewReader(f, size)
	if err != nil {
		return "", contract.NewValidationError("invalid zip archive")
	}
	if explicitVersion != "" {
		return explicitVersion, nil
	}

	const maxVersionFileBytes = 4096
	for _, entry := range zr.File {
		cleanEntry := strings.TrimPrefix(entry.Name, "./")
		if cleanEntry != "VERSION.txt" || entry.FileInfo().IsDir() {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return "", contract.NewValidationError("failed to read VERSION.txt in zip archive")
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, maxVersionFileBytes+1))
		_ = rc.Close()
		if readErr != nil {
			return "", contract.NewValidationError("failed to read VERSION.txt in zip archive")
		}
		if len(data) > maxVersionFileBytes {
			return "", contract.NewValidationError("VERSION.txt in zip archive is too large")
		}
		for line := range strings.Lines(string(data)) {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			return validatePluginVersion(line)
		}
		return "", contract.NewValidationError("VERSION.txt in zip archive is empty")
	}
	return "", contract.NewValidationError("version query parameter or VERSION.txt in zip archive is required")
}

func writeStreamToTempFile(dir string, r io.Reader, contentLength, maxBytes int64) (tmpPath, sha256Hex string, size int64, err error) {
	pattern := uploadTempPrefix + "*"
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", "", 0, fmt.Errorf("create temp upload file: %w", err)
	}
	tmpPath = tmp.Name()

	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(r, maxBytes+1)
	written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if copyErr != nil {
		return "", "", 0, fmt.Errorf("write upload stream: %w", copyErr)
	}
	if written > maxBytes {
		return "", "", 0, ErrUploadTooLarge
	}
	if written == 0 {
		return "", "", 0, contract.NewValidationError("uploaded file is empty")
	}
	if contentLength >= 0 && written != contentLength {
		return "", "", 0, contract.NewValidationError(fmt.Sprintf("upload size mismatch: declared %d bytes, received %d bytes", contentLength, written))
	}
	if err := tmp.Sync(); err != nil {
		return "", "", 0, fmt.Errorf("sync temp upload file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", 0, fmt.Errorf("close temp upload file: %w", err)
	}
	cleanup = false
	return tmpPath, hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func writeFileAtomic(dir, targetPath string, content []byte) error {
	pattern := uploadTempPrefix + "*"
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}
