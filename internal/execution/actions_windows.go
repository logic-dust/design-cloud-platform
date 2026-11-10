package execution

import (
	"design-cloud-engine/internal/execution/payload"
)

// init populates the platform-specific Action registry for Windows.
//
// Windows Agent Architecture Note:
// Windows nodes do not deploy or execute via `dc-exec` (PrivilegedExec is nil).
// All supported operations on Windows MUST be Direct actions executed in-process.
//
// Phase 1 Scope:
// Currently only in-process Direct license status/features queries are enabled.
// Local user/group management, Slurm/LSF job dispatch, and Linux-specific file
// operations are intentionally omitted and will return unsupported errors if requested.
//
// Extension Guide for Future Windows Actions:
// To add new Windows actions (e.g. Windows service query, file ops, process control):
// 1. Define the ActionName constant in actions.go (if protocol-wide)
// 2. Register here with PrivilegedExec: nil (Direct execution)
// 3. Implement the corresponding direct executor in internal/agent/executor/
func init() {
	registerActions(map[ActionName]ActionSpec{
		ActionLicenseStatus: {
			Description: "Query license status directly",
			NewPayload:  func() any { return &payload.LicenseStatus{} },
		},
		ActionLicenseFeatures: {
			Description: "Query license features directly",
			NewPayload:  func() any { return &payload.LicenseFeatures{} },
		},
		ActionSessionControl: {
			Description: "Stop a Windows RDP/interactive user session via logoff (never stops TermService)",
			NewPayload:  func() any { return &payload.SessionControl{} },
			// Windows has no dc-exec; Direct in-process execution.
		},
	})
}
