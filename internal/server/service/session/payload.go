package session

import (
	"time"

	"design-cloud-engine/internal/server/service/file"
)

// Payload is the session context payload returned on login.
type Payload struct {
	Token              string     `json:"token"`
	Username           string     `json:"username"`
	Role               string     `json:"role"`
	IP                 string     `json:"ip"`
	ExpiresAt          time.Time  `json:"expiresAt"`
	PasswordMustChange bool       `json:"passwordMustChange"`
	PasswordExpiresAt  *time.Time `json:"passwordExpiresAt,omitempty"`
	// MultiSessionEnabled 表示同一用户允许多个会话并存（auth.maxSessionsPerUser > 1）。
	MultiSessionEnabled bool        `json:"multiSessionEnabled"`
	File                FilePayload `json:"file"`
	UI                  UIPayload   `json:"ui"`
}

type FilePayload struct {
	Enabled            bool                `json:"enabled"`
	Locations          []file.LocationInfo `json:"locations"`
	DownloadEnabled    bool                `json:"downloadEnabled"`
	UploadEnabled      bool                `json:"uploadEnabled"`
	UploadMaxSizeBytes int64               `json:"uploadMaxSizeBytes"`
}

type UIPayload struct {
	AdminConsole AdminConsolePayload `json:"adminConsole"`
	UserPortal   UserPortalPayload   `json:"userPortal"`
}

// UIModuleListMode 表示 modules 列表语义：allow=仅列出的可见，deny=列出的隐藏。
type UIModuleListMode string

const (
	UIModuleListAllow UIModuleListMode = "allow"
	UIModuleListDeny  UIModuleListMode = "deny"
)

type AdminConsolePayload struct {
	Enabled bool             `json:"enabled"`
	Mode    UIModuleListMode `json:"mode"` // allow | deny
	Modules []string         `json:"modules"`
}

type UserPortalPayload struct {
	Enabled bool             `json:"enabled"`
	Mode    UIModuleListMode `json:"mode"` // allow | deny
	Modules []string         `json:"modules"`
}

// CurrentSession is the lightweight session info returned by getCurrentSession.
type CurrentSession struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	Token     string    `json:"token"`
	Role      string    `json:"role"`
	IP        string    `json:"ip"`
	ExpiresAt time.Time `json:"expiresAt"`
	// MultiSessionEnabled 同 Payload.MultiSessionEnabled。
	MultiSessionEnabled bool `json:"multiSessionEnabled"`
}

// LoginClient 是登录请求的客户端信息：IP 由服务端取，其余字段为客户端自报、可为空。
type LoginClient struct {
	IP         string
	Hostname   string
	Platform   string
	OSVersion  string
	AppVersion string
	DeviceID   string
}
