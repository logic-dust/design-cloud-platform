package payload

// --- License Actions ---

// LicenseStatus for LICENSE_GET_STATUS
type LicenseStatus struct {
	LicensePath string `json:"licensePath"`
	// LmutilPath optionally overrides AgentSettings.lmutilPath for this query.
	LmutilPath string `json:"lmutilPath,omitempty"`
}

// LicenseFeatures for LICENSE_LIST_FEATURES
type LicenseFeatures struct {
	LicensePath string `json:"licensePath"`
	// LmutilPath optionally overrides AgentSettings.lmutilPath for this query.
	LmutilPath string `json:"lmutilPath,omitempty"`
}
