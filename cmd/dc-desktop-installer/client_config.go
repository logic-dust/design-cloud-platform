package main

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"os"
	"path/filepath"
)

// ClientAppConfig defines bootstrap settings written to the desktop client's data folder.
type ClientAppConfig struct {
	ServerURL  string `json:"serverUrl,omitempty"`
	CACertFile string `json:"caCertFile,omitempty"`
}

// GetClientDataDir returns the base directory for desktop client configurations.
// On Windows, this resolves to %APPDATA%\DesignCloud (e.g. C:\Users\<user>\AppData\Roaming\DesignCloud).
func GetClientDataDir() string {
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "DesignCloud")
	}
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "DesignCloud")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".design-cloud")
}

// SaveClientConfigAndCert persists the CA certificate to %APPDATA%\DesignCloud\certs\portal.ca.crt
// and writes the client bootstrap configuration to %APPDATA%\DesignCloud\config.json.
// This guarantees the desktop client can always establish secure TLS connections even if
// system-level certificate stores encounter caching or refresh delays.
func SaveClientConfigAndCert(caBytes []byte, serverURL string) (string, error) {
	dataDir := GetClientDataDir()
	certsDir := filepath.Join(dataDir, "certs")

	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return "", fmt.Errorf("create client certs dir: %w", err)
	}

	var caRelPath string
	if len(caBytes) > 0 {
		caPath := filepath.Join(certsDir, "portal.ca.crt")
		if err := os.WriteFile(caPath, caBytes, 0644); err != nil {
			return "", fmt.Errorf("write client ca certificate: %w", err)
		}
		caRelPath = filepath.Join("certs", "portal.ca.crt")
	}

	if serverURL != "" || caRelPath != "" {
		cfg := ClientAppConfig{
			ServerURL:  serverURL,
			CACertFile: caRelPath,
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal client config: %w", err)
		}
		cfgPath := filepath.Join(dataDir, "config.json")
		if err := os.WriteFile(cfgPath, cfgBytes, 0644); err != nil {
			return "", fmt.Errorf("write client config: %w", err)
		}
	}

	return dataDir, nil
}
