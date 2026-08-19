package model

import (
	"time"

	"gorm.io/gorm"
)

// TicketProject 工单项目
type TicketProject struct {
	ID          uint           `json:"id" gorm:"primarykey"`
	Name        string         `json:"name" gorm:"type:varchar(128);not null;unique"`
	Description string         `json:"description" gorm:"type:text"`
	IsArchived  bool           `json:"isArchived" gorm:"default:false;index"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `json:"deletedAt" gorm:"index"`
}
