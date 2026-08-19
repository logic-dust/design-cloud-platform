// noinspection GoMixedReceiver
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type UserStatusCode int

const (
	UserActive          UserStatusCode = 1
	UserDisabled        UserStatusCode = 2
	UserPasswordExpired UserStatusCode = 3
	UserPasswordNotSet  UserStatusCode = 4
	UserLocked          UserStatusCode = 5
)

func (c UserStatusCode) String() string {
	switch c {
	case UserActive:
		return "active"
	case UserDisabled:
		return "disabled"
	case UserPasswordExpired:
		return "passwordExpired"
	case UserPasswordNotSet:
		return "passwordNotSet"
	case UserLocked:
		return "locked"
	default:
		return fmt.Sprintf("statusCode(%d)", c)
	}
}

// UserStatusString returns the human-readable name for a user status code.
func UserStatusString(code UserStatusCode) string {
	return code.String()
}

func ParseUserStatusCode(s string) (UserStatusCode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return UserActive, nil
	case "disabled":
		return UserDisabled, nil
	case "passwordexpired":
		return UserPasswordExpired, nil
	case "passwordnotset":
		return UserPasswordNotSet, nil
	case "locked":
		return UserLocked, nil
	default:
		return 0, fmt.Errorf("unknown user status code: %q", s)
	}
}

func (c UserStatusCode) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

func (c *UserStatusCode) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid UserStatusCode: %s", string(data))
	}
	parsed, err := ParseUserStatusCode(strVal)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

type UserType int

const (
	UserTypeSystem UserType = 1
	UserTypeApp    UserType = 2
)

func (t UserType) String() string {
	switch t {
	case UserTypeSystem:
		return "system"
	case UserTypeApp:
		return "app"
	default:
		return fmt.Sprintf("userType(%d)", t)
	}
}

func ParseUserType(s string) (UserType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "system":
		return UserTypeSystem, nil
	case "app":
		return UserTypeApp, nil
	default:
		return 0, fmt.Errorf("unknown user type: %q", s)
	}
}

func (t UserType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *UserType) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid UserType: %s", string(data))
	}
	parsed, err := ParseUserType(strVal)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

type UserPreference struct {
	DisabledModules []string `json:"disabledModules,omitempty"`
}

type User struct {
	ID             uint           `json:"id" gorm:"primarykey"`
	Username       string         `json:"username" gorm:"unique;not null;type:varchar(32)"`
	Password       string         `json:"-" gorm:"type:varchar(128)"`
	Status         UserStatusCode `json:"status" gorm:"default:1;index"`
	Type           UserType       `json:"type" gorm:"default:1;index"`
	LastOnlineIP   string         `json:"lastOnlineIP" gorm:"type:varchar(45)"`
	LastOnlineTime time.Time      `json:"lastOnlineTime"`
	DownloadAccess bool           `json:"downloadAccess" gorm:"default:true"`
	UploadAccess   bool           `json:"uploadAccess" gorm:"default:true"`
	TerminalAccess bool           `json:"terminalAccess" gorm:"default:true"`
	Role           Role           `json:"role" gorm:"default:0;index"`
	AdminScopes    AdminScopes    `json:"-" gorm:"default:0"`
	Preference     UserPreference `json:"preference" gorm:"type:text;serializer:json"`
	MaxConnections uint           `json:"maxConnections"`
	Apps           []App          `json:"-" gorm:"many2many:user_apps;"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}
