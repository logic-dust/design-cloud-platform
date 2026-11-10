//go:build windows

package command

// ensureSetPasswordPrivilege is a no-op on Windows: config.Load already enforces
// SYSTEM/Administrators ACLs on config.toml, and Windows nodes are agent-only.
func ensureSetPasswordPrivilege(_ string) error {
	return nil
}
