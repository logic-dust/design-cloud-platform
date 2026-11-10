package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"design-cloud-engine/internal/platform/overlay"
)

var (
	version = "1.0.0"
)

func main() {
	var (
		silent    bool
		skipCert  bool
		skipSetup bool
		caPath    string
		serverURL string
	)

	flag.BoolVar(&silent, "silent", false, "Run in silent mode without interactive console output")
	flag.BoolVar(&silent, "s", false, "Alias for --silent")
	flag.BoolVar(&skipCert, "skip-cert", false, "Skip CA certificate installation")
	flag.BoolVar(&skipSetup, "skip-setup", false, "Install certificate and configs only, do not launch Setup.exe")
	flag.StringVar(&caPath, "ca", "", "Path to custom CA certificate file (overrides embedded certificate)")
	flag.StringVar(&serverURL, "server-url", "", "Target Portal Server URL (overrides embedded server URL)")
	flag.Parse()

	logf := func(format string, args ...any) {
		if !silent {
			fmt.Printf(format, args...)
		}
	}

	logf("=== Design Cloud Desktop Installer (v%s) ===\n\n", version)

	// 1. Read embedded overlay payload
	embeddedPayload, err := overlay.ReadExecutable()
	if err != nil && !silent {
		logf("[Info] No embedded payload detected in installer executable.\n")
	}

	// 2. Resolve CA certificate
	var caBytes []byte
	if caPath != "" {
		data, err := os.ReadFile(caPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to read CA file %q: %v\n", caPath, err)
			os.Exit(1)
		}
		caBytes = data
		logf("[Source] Using CA certificate from command-line flag: %s\n", caPath)
	} else if embeddedPayload != nil && embeddedPayload.CACertPEM != "" {
		caBytes = []byte(embeddedPayload.CACertPEM)
		logf("[Source] Using embedded CA certificate from installer overlay.\n")
	} else {
		// Check adjacent ca.crt or portal.ca.crt
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		candidates := []string{
			filepath.Join(exeDir, "portal.ca.crt"),
			filepath.Join(exeDir, "ca.crt"),
			filepath.Join(exeDir, "certs", "portal.ca.crt"),
		}
		for _, c := range candidates {
			if data, err := os.ReadFile(c); err == nil {
				caBytes = data
				logf("[Source] Using adjacent CA certificate: %s\n", c)
				break
			}
		}
	}

	// 3. Resolve Target Server URL
	targetServer := serverURL
	if targetServer == "" && embeddedPayload != nil {
		targetServer = embeddedPayload.ServerURL
	}
	if targetServer != "" {
		logf("[Config] Configured Portal Server URL: %s\n", targetServer)
	}

	// 4. Double-write CA certificate and Client Config:
	//    (A) Windows System Root Certificate Store (for Edge/Chrome browser and system components)
	//    (B) %APPDATA%\DesignCloud\ (dedicated cache directory for the desktop client)
	if !skipCert && len(caBytes) > 0 {
		logf("[Step 1/3] Installing CA certificate into Windows Trusted Root Store...\n")
		if err := InstallCACertificate(caBytes); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to install CA certificate into system store: %v\n", err)
		} else {
			logf("Successfully registered CA certificate into system store.\n")
		}

		logf("[Step 2/3] Writing client configuration & certificate to user AppData...\n")
		clientDir, err := SaveClientConfigAndCert(caBytes, targetServer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to write client config directory: %v\n", err)
		} else {
			logf("Successfully saved client credentials to: %s\n\n", clientDir)
		}
	} else if skipCert {
		logf("[Step 1/3] Skipped CA certificate installation (--skip-cert).\n\n")
	} else {
		logf("[Step 1/3] Notice: No CA certificate available to install. Continuing...\n\n")
	}

	// 5. Setup execution
	if skipSetup {
		logf("[Step 3/3] Skipped launching setup program (--skip-setup).\n")
		logf("\nCertificate installation finished.\n")
		return
	}

	// Locate local Setup.exe or download via secure HTTPS
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	setupCandidates := []string{
		filepath.Join(exeDir, "DesignCloudDesktop-win-Setup.exe"),
		filepath.Join(exeDir, "DesignCloudDesktop-Setup.exe"),
		filepath.Join(exeDir, "Setup.exe"),
	}

	var targetSetup string
	var downloadedDir string // non-empty only in online-download mode; cleaned up on failure
	for _, cand := range setupCandidates {
		if _, err := os.Stat(cand); err == nil {
			targetSetup = cand
			break
		}
	}

	if targetSetup != "" {
		logf("[Step 3/3] Found local desktop setup package: %s\n", filepath.Base(targetSetup))
	} else if targetServer != "" {
		// Online download mode: use custom HTTPS client directly trusting embedded CA
		logf("[Step 3/3] Local setup package not found. Downloading latest package from server...\n")
		logf("           Target Server: %s\n", targetServer)

		httpClient, err := CreateSecureHTTPClient(caBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to initialize secure HTTPS client: %v\n", err)
			os.Exit(1)
		}

		asset, err := FetchLatestWindowsAsset(httpClient, targetServer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to resolve latest desktop release: %v\n", err)
			os.Exit(1)
		}

		downloadDir, err := os.MkdirTemp("", "dc-desktop-setup-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to create download directory: %v\n", err)
			os.Exit(1)
		}
		downloadedDir = downloadDir

		downloadedFile, err := DownloadPackage(httpClient, targetServer, asset.FileName, downloadDir, silent)
		if err != nil {
			_ = os.RemoveAll(downloadDir)
			fmt.Fprintf(os.Stderr, "Error: Failed to download setup package from server: %v\n", err)
			os.Exit(1)
		}
		if err := verifyFileSHA256(downloadedFile, asset.SHA256); err != nil {
			_ = os.RemoveAll(downloadDir)
			fmt.Fprintf(os.Stderr, "Error: Download integrity check failed: %v\n", err)
			os.Exit(1)
		}
		targetSetup = downloadedFile
		logf("Download complete: %s\n", targetSetup)
	} else {
		logf("[Step 3/3] Notice: Desktop Setup package not found locally and no server URL configured.\n")
		logf("           (Expected local: DesignCloudDesktop-win-Setup.exe or Setup.exe)\n")
		logf("\nCertificate installation complete.\n")
		return
	}

	logf("Launching Desktop setup installer: %s...\n", filepath.Base(targetSetup))

	var setupArgs []string
	if silent {
		setupArgs = append(setupArgs, "--silent")
	}

	cmd := exec.Command(targetSetup, setupArgs...)
	cmd.Dir = filepath.Dir(targetSetup)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		if downloadedDir != "" {
			_ = os.RemoveAll(downloadedDir)
		}
		fmt.Fprintf(os.Stderr, "Error: Failed to launch %s: %v\n", targetSetup, err)
		os.Exit(1)
	}

	logf("Setup launched successfully.\n\nDone.\n")
}
