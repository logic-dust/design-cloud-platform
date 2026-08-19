package model

import "time"

// AlertHistory 记录系统生成的告警历史（触发/重推/恢复）。
// 最多保留 1000 条最新记录，供后台及离线上线后的管理员追溯。
type AlertHistory struct {
	ID         uint      `json:"id" gorm:"primarykey"`
	RuleKey    string    `json:"ruleKey" gorm:"type:varchar(128);index;not null"`
	Metric     float64   `json:"metric"`
	Threshold  float64   `json:"threshold"`
	AlertState string    `json:"alertState" gorm:"type:varchar(32);index;not null"` // "fire" | "repeat" | "recover"
	Level      string    `json:"level" gorm:"type:varchar(32);not null"`            // "warning" | "error"
	Message    string    `json:"message" gorm:"type:text"`
	CreatedAt  time.Time `json:"createdAt" gorm:"index;not null"`
}
