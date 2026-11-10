package fs

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateTarGz(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "bundle.tar.gz")

	entries := []ArchiveEntry{
		{
			Name:    "ca.crt",
			Mode:    0644,
			Data:    []byte("ca-certificate-data"),
			ModTime: time.Now(),
		},
		{
			Name:    "node-01/node-01.key",
			Mode:    0600,
			Data:    []byte("private-key-data"),
			ModTime: time.Now(),
		},
	}

	if err := CreateTarGz(outPath, entries); err != nil {
		t.Fatalf("CreateTarGz failed: %v", err)
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	defer func() {
		_ = gzr.Close()
	}()

	tr := tar.NewReader(gzr)
	readMap := make(map[string][]byte)
	modeMap := make(map[string]int64)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next tar header: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %s: %v", hdr.Name, err)
		}
		readMap[hdr.Name] = content
		modeMap[hdr.Name] = hdr.Mode
	}

	if string(readMap["ca.crt"]) != "ca-certificate-data" {
		t.Errorf("unexpected ca.crt content: %s", string(readMap["ca.crt"]))
	}
	if modeMap["ca.crt"] != 0644 {
		t.Errorf("expected ca.crt mode 0644, got 0%o", modeMap["ca.crt"])
	}

	if string(readMap["node-01/node-01.key"]) != "private-key-data" {
		t.Errorf("unexpected key content: %s", string(readMap["node-01/node-01.key"]))
	}
	if modeMap["node-01/node-01.key"] != 0600 {
		t.Errorf("expected key mode 0600, got 0%o", modeMap["node-01/node-01.key"])
	}
}
