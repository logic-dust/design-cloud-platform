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
	errLicenseAsRoot           = errors.New("license query must not run as root")
	errLicenseInvalidAction    = errors.New("invalid license action or missing usage")
	errLmutilPathNotConfigured = errors.New("lmutilPath not configured: set license.lmutilPath in settings or per license service")
)

// resolveLmutilPath resolves the lmutil executable:
// 1. Per-request override (license service lmutilPath) when non-empty.
// 2. Else settings / node-effective path from GetLmutilPath.
// 3. Never search PATH (CWE-426); never sniff predefined install locations.
func (e *Executor) resolveLmutilPath(override string) (string, error) {
	configured := strings.TrimSpace(override)
	if configured == "" && e.cfg.GetLmutilPath != nil {
		configured = strings.TrimSpace(e.cfg.GetLmutilPath())
	}
	if configured == "" {
		return "", errLmutilPathNotConfigured
	}
	if !filepath.IsAbs(configured) {
		return "", fmt.Errorf("lmutilPath must be an absolute path: %s", configured)
	}
	fi, err := os.Stat(configured)
	if err != nil || fi.IsDir() {
		return "", fmt.Errorf("configured lmutil not found: %s", configured)
	}
	return configured, nil
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

	var (
		args       []string
		lmutilPath string
		err        error
	)
	switch req.ActionName {
	case execution.ActionLicenseStatus:
		p, errPayload := execution.GetPayload[payload.LicenseStatus](req)
		if errPayload != nil {
			return nil, errPayload
		}
		lmutilPath, err = e.resolveLmutilPath(p.LmutilPath)
		if err != nil {
			return nil, err
		}
		if p.LicensePath != "" {
			args = []string{"lmstat", "-c", p.LicensePath}
		}
	case execution.ActionLicenseFeatures:
		p, errPayload := execution.GetPayload[payload.LicenseFeatures](req)
		if errPayload != nil {
			return nil, errPayload
		}
		lmutilPath, err = e.resolveLmutilPath(p.LmutilPath)
		if err != nil {
			return nil, err
		}
		if p.LicensePath != "" {
			args = []string{"lmstat", "-t", "5", "-i", "-a", "-c", p.LicensePath}
		}
	default:
		return nil, errLicenseInvalidAction
	}

	if len(args) == 0 {
		return nil, errLicenseInvalidAction
	}

	return executeCommand(ctx, req, lmutilPath, args...)
}
