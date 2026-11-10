package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CreateSecureHTTPClient creates an HTTP client explicitly trusting the provided CA certificate,
// completely bypassing any system certificate store refresh delays or cache staleness.
func CreateSecureHTTPClient(caBytes []byte) (*http.Client, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if len(caBytes) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, errors.New("failed to parse CA certificate into cert pool")
		}
		tlsConfig.RootCAs = pool
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:       tlsConfig,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 30 * time.Minute,
	}, nil
}

func downloadURLFor(baseURL, filename string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	escaped := url.PathEscape(filename)
	return fmt.Sprintf("%s/api/desktop/releases/download/%s", baseURL, escaped)
}

// DownloadPackage downloads the desktop installer package from the portal server.
func DownloadPackage(client *http.Client, serverURL, filename, destDir string, silent bool) (string, error) {
	baseURL := strings.TrimRight(serverURL, "/")
	downloadURL := downloadURLFor(baseURL, filename)
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to portal server %s: %w", downloadURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download server returned HTTP %d (%s)", resp.StatusCode, resp.Status)
	}

	destPath := filepath.Join(destDir, filepath.Base(filename))
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("create destination file %q: %w", destPath, err)
	}

	totalBytes := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 64*1024)
	lastReport := time.Now()

	for {
		nr, rerr := resp.Body.Read(buf)
		if nr > 0 {
			nw, werr := out.Write(buf[:nr])
			if werr != nil {
				_ = out.Close()
				_ = os.Remove(destPath)
				return "", fmt.Errorf("write to %q: %w", destPath, werr)
			}
			downloaded += int64(nw)

			if !silent && (time.Since(lastReport) >= 250*time.Millisecond || rerr != nil) {
				lastReport = time.Now()
				if totalBytes > 0 {
					pct := float64(downloaded) * 100.0 / float64(totalBytes)
					fmt.Printf("\r[Download Progress] %6.1f%% (%.1f MB / %.1f MB)    ",
						pct, float64(downloaded)/(1024*1024), float64(totalBytes)/(1024*1024))
				} else {
					fmt.Printf("\r[Download Progress] %.1f MB transferred...        ",
						float64(downloaded)/(1024*1024))
				}
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			_ = out.Close()
			_ = os.Remove(destPath)
			return "", fmt.Errorf("read response body: %w", rerr)
		}
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("close %q: %w", destPath, err)
	}

	if !silent {
		fmt.Println()
	}
	return destPath, nil
}
