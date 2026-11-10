//go:build !linux

package command

// adjustCertOwnershipIfRoot is a no-op on non-Linux platforms (e.g. Windows).
func adjustCertOwnershipIfRoot(_ *PortalCertResult, _ string) {
}
