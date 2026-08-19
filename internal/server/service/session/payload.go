package session

import (
	"time"

	"design-cloud-engine/internal/server/service/file"
)

// Payload is the session context payload returned on login.
type Payload struct {
	Token              string      `json:"token"`
	Username           string      `json:"username"`
	Role               string      `json:"role"`
	IP                 string      `json:"ip"`
	ExpiresAt          time.Time   `json:"expiresAt"`
	PasswordMustChange bool        `json:"passwordMustChange"`
	PasswordExpiresAt  *time.Time  `json:"passwordExpiresAt,omitempty"`
	File               FilePayload `json:"file"`
	UI                 UIPayload   `json:"ui"`
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

type AdminConsolePayload struct {
	Enabled     bool     `json:"enabled"`
	IsWhitelist bool     `json:"isWhitelist"`
	Modules     []string `json:"modules"`
}

type UserPortalPayload struct {
	Enabled     bool     `json:"enabled"`
	IsWhitelist bool     `json:"isWhitelist"`
	Modules     []string `json:"modules"`
}

// CurrentSession is the lightweight session info returned by getCurrentSession.
type CurrentSession struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	Token     string    `json:"token"`
	Role      string    `json:"role"`
	IP        string    `json:"ip"`
	ExpiresAt time.Time `json:"expiresAt"`
}
