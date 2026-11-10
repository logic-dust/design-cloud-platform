package model

import "time"

type TaskLease struct {
	ID       uint   `gorm:"primarykey"`
	TaskName string `gorm:"uniqueIndex;not null;type:varchar(128)"`
	OwnerID  string `gorm:"not null;type:varchar(128)"`
	// Epoch 是 fencing token：仅同 Owner 存活续约不变，其余任何成功获取
	// （新建行、新 Owner 抢占、同 Owner 跨过期重获）一律 +1。
	Epoch     uint64    `gorm:"not null;default:0"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}
