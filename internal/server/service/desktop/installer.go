package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
)

// BootstrapInstallerFileName is the cert-injected Windows bootstrap installer
// (cmd/dc-desktop-installer + `cert inject-installer`) published beside the
// Velopack release tree. It installs the Portal CA and client config, then
// downloads the Setup.exe reported by GetInstallerAsset and verifies its SHA256.
const BootstrapInstallerFileName = "DesignCloudDesktop-Installer.exe"

// InstallerAsset is a first-run Setup package (not a Velopack nupkg).
type InstallerAsset struct {
	FileName string `json:"fileName"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// GetInstallerAsset locates the first-run Windows Setup.exe beside the Velopack
// release tree and returns its name + SHA256. Velopack releases.*.json only lists
// nupkg update packages; the first-run installer is a sibling single artifact.
// Its name is configured by desktop.installerFileName (default:
// DesignCloudDesktop-win-Setup.exe); there is no versioned-name fallback.
func (s *Service) GetInstallerAsset(ctx context.Context) (InstallerAsset, error) {
	if _, err := s.checkAvailableAndGetMaxAssets(ctx, "win"); err != nil {
		return InstallerAsset{}, err
	}

	name := schema.DefaultInstallerFileName
	if s.settingsSvc != nil {
		dSettings, err := s.settingsSvc.GetDesktopSettings(ctx)
		if err != nil {
			return InstallerAsset{}, err
		}
		name = dSettings.EffectiveInstallerFileName()
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name ||
		!strings.HasSuffix(strings.ToLower(name), ".exe") {
		return InstallerAsset{}, contract.NewValidationError("invalid desktop.installerFileName")
	}
	return s.loadInstallerAsset(name)
}

// GetBootstrapInstallerAsset returns metadata for the optional cert-injected
// bootstrap installer. Unlike GetInstallerAsset, its name is fixed because the
// Engine owns the build/injection convention (`make inject-installer`).
func (s *Service) GetBootstrapInstallerAsset(ctx context.Context) (InstallerAsset, error) {
	if _, err := s.checkAvailableAndGetMaxAssets(ctx, "win"); err != nil {
		return InstallerAsset{}, err
	}
	return s.loadInstallerAsset(BootstrapInstallerFileName)
}

// loadInstallerAsset returns the file metadata, re-hashing only when the file's
// size or mtime changes so portal page loads do not SHA256 large installers.
func (s *Service) loadInstallerAsset(name string) (InstallerAsset, error) {
	full := filepath.Join(s.baseDir, name)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return InstallerAsset{}, contract.NewNotFoundError("installer package not found: " + name)
	}

	s.installerMu.Lock()
	cached, ok := s.installerCache[name]
	s.installerMu.Unlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.asset, nil
	}

	sum, size, err := hashFileSHA256(full)
	if err != nil {
		return InstallerAsset{}, err
	}
	asset := InstallerAsset{FileName: name, SHA256: sum, Size: size}

	s.installerMu.Lock()
	if s.installerCache == nil {
		s.installerCache = make(map[string]cachedInstallerAsset)
	}
	s.installerCache[name] = cachedInstallerAsset{
		modTime: info.ModTime(),
		size:    info.Size(),
		asset:   asset,
	}
	s.installerMu.Unlock()
	return asset, nil
}

func hashFileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, contract.NewNotFoundError("installer package not found")
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
