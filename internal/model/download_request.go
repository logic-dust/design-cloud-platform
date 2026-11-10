// noinspection GoMixedReceiver
package model

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type DownloadRequestStatus int

const (
	DownloadRequestStatusPending  DownloadRequestStatus = 1
	DownloadRequestStatusApproved DownloadRequestStatus = 2
	DownloadRequestStatusRejected DownloadRequestStatus = 3
)

func (s DownloadRequestStatus) String() string {
	switch s {
	case DownloadRequestStatusPending:
		return "pending"
	case DownloadRequestStatusApproved:
		return "approved"
	case DownloadRequestStatusRejected:
		return "rejected"
	default:
		return fmt.Sprintf("downloadRequestStatus(%d)", s)
	}
}

func ParseDownloadRequestStatus(s string) (DownloadRequestStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return DownloadRequestStatusPending, nil
	case "approved":
		return DownloadRequestStatusApproved, nil
	case "rejected":
		return DownloadRequestStatusRejected, nil
	default:
		return 0, fmt.Errorf("unknown download request status: %q", s)
	}
}

func (s DownloadRequestStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *DownloadRequestStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid DownloadRequestStatus: %s", string(data))
	}
	parsed, err := ParseDownloadRequestStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// DownloadRequest 是 Portal 文件下载审批申请（GORM 默认表名 download_requests）。
type DownloadRequest struct {
	ID              uint                  `json:"id" gorm:"primarykey"`
	Path            string                `json:"path" gorm:"type:text"`
	FileFingerprint string                `json:"-"`
	Size            int64                 `json:"size"` // 文件物理体积，单位字节 (Bytes)
	Requester       string                `json:"requester" gorm:"index"`
	Status          DownloadRequestStatus `json:"status" gorm:"default:1;index"`
	Approver        string                `json:"approver,omitempty" gorm:"type:varchar(32)"`
	ReviewedAt      *time.Time            `json:"reviewedAt,omitempty"`
	ExpiresAt       *time.Time            `json:"expiresAt,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	CreatedAt       time.Time             `json:"createdAt"`
	UpdatedAt       time.Time             `json:"updatedAt"`
	DeletedAt       gorm.DeletedAt        `json:"-" gorm:"index"`
}
