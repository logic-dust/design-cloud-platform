package model

import "time"

type TaskLease struct {
	ID        uint      `gorm:"primarykey"`
	TaskName  string    `gorm:"uniqueIndex;not null;type:varchar(128)"`
	OwnerID   string    `gorm:"not null;type:varchar(128)"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}
