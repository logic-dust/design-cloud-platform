package probe

// SysInfo holds OS product/version strings probed once when the agent connects.
type SysInfo struct {
	OSName        string
	OSVersion     string
	KernelVersion string
}

// DetectSysInfo probes the local host OS product name, version, and optional kernel.
// Call only at connect (RegisterRequest), not on heartbeat.
func DetectSysInfo() SysInfo {
	return detectSysInfo()
}
