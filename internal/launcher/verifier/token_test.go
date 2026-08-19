//go:build !windows

package verifier

import (
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/common/fs"
)

// withTempTokenPath redirects launcherTokenPath to a temp directory for the
// duration of the test. Returns the token directory path and the token file path.
func withTempTokenPath(t *testing.T) (tokenDir, tokenFile string) {
	t.Helper()
	tokenDir = filepath.Join(t.TempDir(), "runtime")
	tokenFile = filepath.Join(tokenDir, "dc-launcher.token")
	orig := launcherTokenPath
	launcherTokenPath = func() string { return tokenFile }
	t.Cleanup(func() { launcherTokenPath = orig })
	return tokenDir, tokenFile
}

func TestToken_CorrectToken(t *testing.T) {
	tokenDir, tokenFile := withTempTokenPath(t)

	if err := fs.MkdirPrivate(tokenDir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	const secret = "test-secret-token"
	if err := fs.WritePrivateFile(tokenFile, []byte(secret)); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}

	if err := Token(secret); err != nil {
		t.Fatalf("Token correct secret: %v", err)
	}
}

func TestToken_WrongToken(t *testing.T) {
	tokenDir, tokenFile := withTempTokenPath(t)

	if err := fs.MkdirPrivate(tokenDir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	if err := fs.WritePrivateFile(tokenFile, []byte("real-secret")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}

	if err := Token("wrong-secret"); err == nil {
		t.Fatal("expected error for wrong token, got nil")
	}
}

func TestToken_MissingFile(t *testing.T) {
	tokenDir, _ := withTempTokenPath(t)
	// Create the directory but NOT the token file.
	if err := fs.MkdirPrivate(tokenDir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}

	if err := Token("any"); err == nil {
		t.Fatal("expected error when token file is missing, got nil")
	}
}

func TestToken_BadDirPermissions(t *testing.T) {
	tokenDir, _ := withTempTokenPath(t)

	// Create dir with wrong permissions (0755 instead of 0700).
	if err := os.MkdirAll(tokenDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := Token("secret"); err == nil {
		t.Fatal("expected error for dir with wrong permissions, got nil")
	}
}

func TestToken_BadFilePermissions(t *testing.T) {
	tokenDir, tokenFile := withTempTokenPath(t)

	if err := fs.MkdirPrivate(tokenDir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	// Write with 0644 instead of 0600.
	if err := os.WriteFile(tokenFile, []byte("secret"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Token("secret"); err == nil {
		t.Fatal("expected error for file with wrong permissions, got nil")
	}
}
