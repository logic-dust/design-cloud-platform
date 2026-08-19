package direct

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
)

var (
	errLicenseAsRoot        = errors.New("license query must not run as root")
	errLicenseInvalidAction = errors.New("invalid license action or missing usage")
	errLmutilNotFound       = errors.New("lmutil executable not found in configured path or predefined candidates")
)

// predefinedLmutilCandidates returns absolute fallback paths for lmutil by OS.
// PATH is NEVER searched (CWE-426).
func predefinedLmutilCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{
			`C:\Program Files\FlexNet Publisher\lmutil.exe`,
			`C:\Program Files (x86)\FlexNet Publisher\lmutil.exe`,
			`C:\FlexLM\lmutil.exe`,
		}
	}
	return []string{
		"/opt/flexnet/bin/lmutil",
		"/opt/flexlm/bin/lmutil",
		"/usr/local/bin/lmutil",
		"/usr/bin/lmutil",
	}
}

// resolveLmutilPath resolves the lmutil executable to use:
// 1. If configured (via settings/node override), it must be an absolute path and must exist as a file.
// 2. If empty, test predefined absolute candidates.
// 3. Never search PATH.
func (e *Executor) resolveLmutilPath() (string, error) {
	var configured string
	if e.cfg.GetLmutilPath != nil {
		configured = strings.TrimSpace(e.cfg.GetLmutilPath())
	}

	if configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("lmutilPath must be an absolute path: %s", configured)
		}
		fi, err := os.Stat(configured)
		if err != nil || fi.IsDir() {
			return "", fmt.Errorf("configured lmutil not found: %s", configured)
		}
		return configured, nil
	}

	for _, candidate := range predefinedLmutilCandidates() {
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
	}

	return "", errLmutilNotFound
}

// ============================================================================
// FLEXLM License Query Actions (Read-Only, No Privileges Required)
// ============================================================================

// executeLicenseCommand executes license query commands directly using `{lmutilPath} lmstat <args>`.
// Only the lmutil command form is supported; no basename special casing for standalone lmstat.
func (e *Executor) executeLicenseCommand(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		return nil, errLicenseAsRoot
	}

	lmutilPath, err := e.resolveLmutilPath()
	if err != nil {
		return nil, err
	}

	var args []string
	switch req.ActionName {
	case execution.ActionLicenseStatus:
		p, err := execution.GetPayload[payload.LicenseStatus](req)
		if err != nil {
			return nil, err
		}
		if p.LicensePath != "" {
			args = []string{"lmstat", "-c", p.LicensePath}
		}
	case execution.ActionLicenseFeatures:
		p, err := execution.GetPayload[payload.LicenseFeatures](req)
		if err != nil {
			return nil, err
		}
		if p.LicensePath != "" {
			args = []string{"lmstat", "-t", "5", "-i", "-a", "-c", p.LicensePath}
		}
	}

	if len(args) == 0 {
		return nil, errLicenseInvalidAction
	}

	return executeCommand(ctx, req, lmutilPath, args...)
}
