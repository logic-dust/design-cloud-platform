// noinspection GoMixedReceiver
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type DownloadStatus int

const (
	DownloadStatusPending  DownloadStatus = 1
	DownloadStatusApproved DownloadStatus = 2
	DownloadStatusRejected DownloadStatus = 3
)

func (s DownloadStatus) String() string {
	switch s {
	case DownloadStatusPending:
		return "pending"
	case DownloadStatusApproved:
		return "approved"
	case DownloadStatusRejected:
		return "rejected"
	default:
		return fmt.Sprintf("downloadStatus(%d)", s)
	}
}

func ParseDownloadStatus(s string) (DownloadStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return DownloadStatusPending, nil
	case "approved":
		return DownloadStatusApproved, nil
	case "rejected":
		return DownloadStatusRejected, nil
	default:
		return 0, fmt.Errorf("unknown download status: %q", s)
	}
}

func (s DownloadStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *DownloadStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid DownloadStatus: %s", string(data))
	}
	parsed, err := ParseDownloadStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

type Download struct {
	ID              uint   `gorm:"primarykey"`
	Path            string `json:"path" gorm:"type:text"`
	FileFingerprint string `json:"-"`
	Size            int64
	Requester       string         `json:"requester" gorm:"index"`
	Status          DownloadStatus `gorm:"default:1;index"`
	Approver        string         `json:"approver" gorm:"type:varchar(32)"`
	ReviewedAt      *time.Time
	ExpiresAt       *time.Time
	Reason          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"index"`
}
