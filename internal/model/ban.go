package model

import "time"

type BanType string

const (
	BanTypeIP   BanType = "IP"
	BanTypeUser BanType = "User"
)

type BanRecord struct {
	Key      string `gorm:"primaryKey;type:varchar(128)"`
	Count    uint
	IP       string `gorm:"type:varchar(45)"`
	Username string `gorm:"type:varchar(45);index:idx_ban_username"`
	Type     BanType
	// 时间戳为业务语义（首次失败/最后失败），非 GORM 约定的"创建/修改时间"，
	// 因此由内存缓存 BanCache 显式维护并在 flush 时写入，不使用 GORM 自动特性。
	CreatedAt   time.Time // 首次失败/封禁时间（BanTime 展示语义）
	UpdatedAt   time.Time // 最后失败时间（无封禁失败记录的清理保留期判断）
	BannedUntil *time.Time
}
