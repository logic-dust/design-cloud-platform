package desktop

import (
	"fmt"
	"strconv"
	"strings"
)

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
