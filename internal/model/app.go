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
	Name   string  `json:"name" gorm:"unique;not null;type:varchar(32)"`
	Alias  string  `json:"alias" gorm:"type:varchar(100)"`
	Type   AppType `json:"type" gorm:"not null"`
	Cmd    string  `json:"cmd" gorm:"type:text"`
	Icon   string  `json:"icon" gorm:"type:varchar(256)"`
	Public bool    `json:"public" gorm:"default:false"`
	Form   string  `json:"form" gorm:"type:text"`
	About  string  `json:"about" gorm:"type:varchar(512)"`
	Queue  string  `json:"queue" gorm:"type:varchar(128)"`
}

type App struct {
	ID      uint `json:"id" gorm:"primarykey"`
	AppBase `gorm:"embedded"`
	// WARNING: Do NOT use nested Preload like Preload("Users.Apps") to avoid circular JSON serialization. Keep Preload depth to 1 level.
	Users []User `json:"-" gorm:"many2many:user_apps;"`
	// App 采用**硬删除**（无 DeletedAt）：Name 上的唯一约束要求删除即物理移除，
	// 否则软删行会永久占用唯一索引，导致同名应用无法重建。
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
