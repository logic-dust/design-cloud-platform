package model

import "time"

type LicenseServiceStatus string

const (
	LicenseServiceStatusUp      LicenseServiceStatus = "up"
	LicenseServiceStatusDown    LicenseServiceStatus = "down"
	LicenseServiceStatusUnknown LicenseServiceStatus = "unknown"
)

type LicenseType string

const (
	LicenseTypeFlexlm LicenseType = "flexlm"
)

// LicenseServiceBase holds the writable fields shared by the model and service input.
type LicenseServiceBase struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name" gorm:"type:varchar(64);not null"`
	Address string `json:"address" gorm:"type:varchar(64)"`
	Server  string `json:"server" gorm:"type:varchar(128)"`
	// LmutilPath optionally overrides DomainLicense / node-effective lmutilPath for this service.
	// Empty means use settings (global + node override). Absolute path required when set.
	LmutilPath string      `json:"lmutilPath,omitempty" gorm:"type:varchar(256)"`
	Comment    string      `json:"comment" gorm:"type:varchar(255)"`
	Type       LicenseType `json:"type" gorm:"type:varchar(32)"`
}

type LicenseService struct {
	ID                 uint `json:"id" gorm:"primarykey"`
	LicenseServiceBase `gorm:"embedded"`
	Status             LicenseServiceStatus `json:"status" gorm:"-"`
	CreatedAt          time.Time            `json:"createdAt"`
	UpdatedAt          time.Time            `json:"updatedAt"`
}
