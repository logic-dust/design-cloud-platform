package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirPrivateAndVerifyPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create test dir: %v", err)
	}
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	if err := VerifyPrivateDir(dir); err != nil {
		t.Fatalf("VerifyPrivateDir: %v", err)
	}
}

func TestWritePrivateFileAndVerifyPrivateFileDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := WritePrivateFile(path, []byte("first")); err != nil {
		t.Fatalf("WritePrivateFile first: %v", err)
	}
	if err := WritePrivateFile(path, []byte("second")); err != nil {
		t.Fatalf("WritePrivateFile second: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open private file: %v", err)
	}
	defer func() { _ = f.Close() }()

	if err := VerifyPrivateFileDescriptor(f); err != nil {
		t.Fatalf("VerifyPrivateFileDescriptor: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read private file: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("file content = %q, want second", string(data))
	}
}
