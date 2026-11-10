package model

import (
	"time"
)

type AppType string

const (
	GUIApp   AppType = "GUI"   // graphical app (EDA tool GUI / simulation window)
	BatchApp AppType = "batch" // command app, a command line app, run a command (or script)
	FormApp  AppType = "form"  // provide a form to user, it could be only run as a batch app, or run as a graphical app
)

// AppBase holds the writable application fields shared by the model and service input.
type AppBase struct {
	Slug        string  `json:"slug" gorm:"column:slug;unique;not null;type:varchar(32)"`
	Name        string  `json:"name" gorm:"column:name;type:varchar(100)"`
	Type        AppType `json:"type" gorm:"not null"`
	Cmd         string  `json:"cmd" gorm:"type:text"`
	Icon        string  `json:"icon" gorm:"type:varchar(256)"`
	Public      bool    `json:"public" gorm:"default:false"`
	Form        string  `json:"form" gorm:"type:text"`
	Description string  `json:"description" gorm:"column:description;type:varchar(512)"`
	Queue       string  `json:"queue" gorm:"type:varchar(128)"`
}

type App struct {
	ID      uint `json:"id" gorm:"primarykey"`
	AppBase `gorm:"embedded"`
	// App 采用**硬删除**（无 DeletedAt）：Slug 上的唯一约束要求删除即物理移除，
	// 否则软删行会永久占用唯一索引，导致同 slug 应用无法重建。
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// UserApp maps a portal username to a private app (survives user-row recreate).
type UserApp struct {
	Username string `gorm:"primaryKey;size:32;not null" json:"username"`
	AppID    uint   `gorm:"primaryKey;index:idx_user_apps_app_id" json:"appId"`
}
