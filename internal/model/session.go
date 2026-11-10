package model

import (
	"time"

	"golang.org/x/time/rate"
)

// 客户端自报信息的长度上限（按字符计），与列宽一致。
const (
	MaxSessionHostnameLength   = 255
	MaxSessionPlatformLength   = 32
	MaxSessionOSVersionLength  = 64
	MaxSessionAppVersionLength = 64
	MaxSessionDeviceIDLength   = 64
)

type Session struct {
	ID                 uint        `json:"id" gorm:"primaryKey"`
	UserID             uint        `json:"-" gorm:"index;not null"`
	Username           string      `json:"username" gorm:"index"`
	Token              string      `json:"-" gorm:"uniqueIndex;not null"`
	IP                 string      `json:"ip" gorm:"type:varchar(45)"`
	Hostname           string      `json:"hostname" gorm:"type:varchar(255)"`  // 客户端登录时自报的主机名
	Platform           string      `json:"platform" gorm:"type:varchar(32)"`   // 客户端登录时自报的平台，如 "windows"
	OSVersion          string      `json:"osVersion" gorm:"type:varchar(64)"`  // 客户端登录时自报的系统版本号
	AppVersion         string      `json:"appVersion" gorm:"type:varchar(64)"` // 客户端登录时自报的应用版本号
	DeviceID           string      `json:"deviceId" gorm:"type:varchar(64)"`   // 客户端登录时自报的稳定设备标识
	Role               Role        `json:"role" gorm:"default:0"`
	UserType           UserType    `json:"type" gorm:"default:1"`
	AdminScopes        AdminScopes `json:"adminScopes,omitempty" gorm:"default:0"`
	PasswordMustChange bool        `json:"passwordMustChange" gorm:"default:false"`
	CreatedAt          time.Time   `json:"createdAt"`
	UpdatedAt          time.Time   `json:"updatedAt"`
	ExpiresAt          time.Time   `json:"expiresAt" gorm:"index"`

	// RateLimiter is a per-session token bucket for request rate limiting.
	// Not persisted; initialized when the session is loaded into the memory cache.
	RateLimiter *rate.Limiter `json:"-" gorm:"-"`
}
