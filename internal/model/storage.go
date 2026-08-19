package model

import "time"

// StorageReport records historical capacity samples for shared storage arrays.
// This is a time-series table; it is not cleaned up to allow for long-term trend analysis.
type StorageReport struct {
	ID         uint      `gorm:"primarykey"`
	StorageID  string    `gorm:"not null;type:varchar(64);index:idx_storage_reported,priority:1"`
	Total      int64     // 容量，单位: KB (KiB)
	Used       int64     // 已用容量，单位: KB (KiB)
	ReportedAt time.Time `gorm:"not null;index:idx_storage_reported,priority:2"` // agent 采集端打点时间
	SampledBy  string    `gorm:"type:varchar(64)"`                               // node_id of the agent that performed the statfs probe
}
