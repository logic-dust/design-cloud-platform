package payload

// --- License Actions ---

// LicenseStatus for LICENSE_GET_STATUS
type LicenseStatus struct {
	LicensePath string `json:"licensePath"`
}

// LicenseFeatures for LICENSE_LIST_FEATURES
type LicenseFeatures struct {
	LicensePath string `json:"licensePath"`
}
