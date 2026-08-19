package model

import (
	"time"

	"golang.org/x/time/rate"
)

type Session struct {
	ID                 uint        `json:"id" gorm:"primaryKey"`
	UserID             uint        `json:"-" gorm:"index;not null"`
	Username           string      `json:"username" gorm:"index"`
	Token              string      `json:"-" gorm:"uniqueIndex;not null"`
	IP                 string      `json:"ip" gorm:"type:varchar(45)"`
	Role               Role        `json:"role" gorm:"default:0"`
	Type               UserType    `json:"type" gorm:"column:user_type;default:1"`
	AdminScopes        AdminScopes `json:"adminScopes,omitempty" gorm:"default:0"`
	PasswordMustChange bool        `json:"passwordMustChange" gorm:"default:false"`
	CreatedAt          time.Time   `json:"createdAt"`
	UpdatedAt          time.Time   `json:"updatedAt"`
	ExpiresAt          time.Time   `json:"expiresAt" gorm:"index"`

	// RateLimiter is a per-session token bucket for request rate limiting.
	// Not persisted; initialized when the session is loaded into the memory cache.
	RateLimiter *rate.Limiter `json:"-" gorm:"-"`
}
