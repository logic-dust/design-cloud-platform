//go:build !windows

package verifier

import (
	"crypto/subtle"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"design-cloud-engine/internal/common/fs"
)

// launcherTokenPath returns the current launcher token file path.
// Replaced in tests to redirect to a temporary directory.
var launcherTokenPath = fs.LauncherTokenPath

// Token performs L2 token verification with security checks.
// This is the second layer of the 4-layer defense-in-depth security model.
//
// L2 validates that the request comes from a legitimate Agent process by:
// 1. Token directory has 0700 permissions (owner-only access)
// 2. Token file has 0600 permissions (read/write by owner)
// 3. Token content matches (using constant-time comparison)
//
// L2 cannot be replaced by L4 (seccomp) because:
// - seccomp cannot distinguish between "legitimate Agent" and "malicious same-UID process"
// - seccomp cannot verify request authenticity
// - seccomp cannot prevent token replay attacks
func Token(providedToken string) error {
	tokenPath := launcherTokenPath()
	tokenDir := filepath.Dir(tokenPath)

	// Security Check 1: Verify directory permissions.
	//
	// NOTE: There is a theoretical TOCTOU window between this os.Stat-based
	// directory check and the os.Open below — a same-UID attacker could attempt
	// to replace tokenDir with a symlink during the race. However this is not
	// exploitable in practice:
	//   - The attacker must delete the 0700 directory contents and the directory
	//     itself within the race window, which is extremely narrow.
	//   - Even if the race is won, Security Check 2 below uses fstat on the
	//     already-opened file descriptor, which is TOCTOU-safe and verifies the
	//     actual file that will be read.
	//   - The attacker could only redirect to another file they already own with
	//     0600 permissions — no privilege gain.
	// Therefore this window is intentionally left unfixed.
	if err := fs.VerifyPrivateDir(tokenDir); err != nil {
		return fmt.Errorf("token directory security check failed: %w", err)
	}

	// Open the token file first to prevent TOCTOU race conditions.
	f, err := os.Open(tokenPath)
	if err != nil {
		return fmt.Errorf("token verification failed: %w", err)
	}
	defer func(f *os.File) { _ = f.Close() }(f)

	// Security Check 2: Verify file permissions (token must be 0600) on the open file descriptor.
	if err := fs.VerifyPrivateFileDescriptor(f); err != nil {
		return fmt.Errorf("token file security check failed: %w", err)
	}

	// Read token file from the open descriptor
	tokenData, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("token verification failed: %w", err)
	}

	storedToken := strings.TrimSpace(string(tokenData))

	// Use constant time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(storedToken)) != 1 {
		return fmt.Errorf("invalid token")
	}

	return nil
}
