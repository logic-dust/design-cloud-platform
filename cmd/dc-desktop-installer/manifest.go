package main

import (
	"crypto/sha256"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type portalEnvelope struct {
	Data json.RawMessage `json:"data"`
}

type releaseAsset struct {
	FileName string
	SHA256   string
}

type installerAPIAsset struct {
	FileName string `json:"fileName"`
	SHA256   string `json:"sha256"`
}

// FetchLatestWindowsAsset resolves the first-run Setup package from the
// dedicated installer endpoint. Velopack's releases.*.json only lists nupkg
// update packages, so it is deliberately not parsed here: updates use that
// manifest, first-run installation uses the configured single Setup artifact.
func FetchLatestWindowsAsset(client *http.Client, serverURL string) (releaseAsset, error) {
	baseURL := strings.TrimRight(serverURL, "/")
	return fetchInstallerEndpoint(client, baseURL)
}

func fetchInstallerEndpoint(client *http.Client, baseURL string) (releaseAsset, error) {
	resp, err := client.Get(baseURL + "/api/desktop/releases/installer")
	if err != nil {
		return releaseAsset{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return releaseAsset{}, fmt.Errorf("installer endpoint HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return releaseAsset{}, err
	}
	var env portalEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return releaseAsset{}, err
	}
	var asset installerAPIAsset
	if err := json.Unmarshal(env.Data, &asset); err != nil {
		return releaseAsset{}, err
	}
	if asset.FileName == "" || asset.SHA256 == "" {
		return releaseAsset{}, fmt.Errorf("installer endpoint returned incomplete asset")
	}
	name := filepath.Base(asset.FileName)
	if name != asset.FileName || name == "." || name == ".." ||
		!strings.HasSuffix(strings.ToLower(name), ".exe") {
		return releaseAsset{}, fmt.Errorf("installer endpoint returned invalid asset name %q", asset.FileName)
	}
	return releaseAsset{FileName: name, SHA256: strings.ToLower(asset.SHA256)}, nil
}

func verifyFileSHA256(path, wantHex string) error {
	wantHex = strings.ToLower(strings.TrimSpace(wantHex))
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash file: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		return fmt.Errorf("SHA256 mismatch: got %s want %s", got, wantHex)
	}
	return nil
}
