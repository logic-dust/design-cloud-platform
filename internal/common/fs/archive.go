package fs

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ArchiveEntry represents an entry to be written to a tar.gz archive.
type ArchiveEntry struct {
	Name    string // Relative path in the archive
	Mode    int64  // File permission mode (e.g. 0600 or 0644)
	Data    []byte // File content
	ModTime time.Time
}

// CreateTarGz writes the provided entries to a .tar.gz archive at outPath.
func CreateTarGz(outPath string, entries []ArchiveEntry) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	gw := gzip.NewWriter(f)
	defer func() {
		_ = gw.Close()
	}()

	tw := tar.NewWriter(gw)
	defer func() {
		_ = tw.Close()
	}()

	now := time.Now()
	for _, entry := range entries {
		modTime := entry.ModTime
		if modTime.IsZero() {
			modTime = now
		}
		hdr := &tar.Header{
			Name:    entry.Name,
			Mode:    entry.Mode,
			Size:    int64(len(entry.Data)),
			ModTime: modTime,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write tar header for %s: %w", entry.Name, err)
		}
		if _, err := tw.Write(entry.Data); err != nil {
			return fmt.Errorf("write tar content for %s: %w", entry.Name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close archive file: %w", err)
	}
	return nil
}
