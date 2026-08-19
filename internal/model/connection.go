// noinspection GoMixedReceiver
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
)

type ConnectionStatus int
type ConnectionSource int

const (
	ConnectionRunning ConnectionStatus = 1
	ConnectionStopped ConnectionStatus = 2
	ConnectionUnknown ConnectionStatus = 3

	ConnectionSourceManual     ConnectionSource = 2
	ConnectionSourceDiscovered ConnectionSource = 3
	ConnectionSourceFixed      ConnectionSource = 4
)

func (s ConnectionStatus) String() string {
	switch s {
	case ConnectionRunning:
		return "running"
	case ConnectionStopped:
		return "stopped"
	case ConnectionUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("connectionStatus(%d)", s)
	}
}

func ParseConnectionStatus(s string) (ConnectionStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return ConnectionRunning, nil
	case "stopped":
		return ConnectionStopped, nil
	case "unknown":
		return ConnectionUnknown, nil
	default:
		return 0, fmt.Errorf("unknown connection status: %q", s)
	}
}

func (s ConnectionStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *ConnectionStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid ConnectionStatus: %s", string(data))
	}
	parsed, err := ParseConnectionStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

func (src ConnectionSource) String() string {
	switch src {
	case ConnectionSourceManual:
		return "manual"
	case ConnectionSourceDiscovered:
		return "discovered"
	case ConnectionSourceFixed:
		return "fixed"
	default:
		return fmt.Sprintf("connectionSource(%d)", src)
	}
}

func ParseConnectionSource(s string) (ConnectionSource, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "manual":
		return ConnectionSourceManual, nil
	case "discovered":
		return ConnectionSourceDiscovered, nil
	case "fixed":
		return ConnectionSourceFixed, nil
	default:
		return 0, fmt.Errorf("unknown connection source: %q", s)
	}
}

func (src ConnectionSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(src.String())
}

func (src *ConnectionSource) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid ConnectionSource: %s", string(data))
	}
	parsed, err := ParseConnectionSource(strVal)
	if err != nil {
		return err
	}
	*src = parsed
	return nil
}

type ConnectionExtra struct {
	StartOptions string `json:"startOptions,omitempty"` // 启动参数（如 "-wm 2d -geometry 1920x1080"）
	OTPEnabled   *bool  `json:"otpEnabled,omitempty"`   // nil/true = 允许签发 OTP, false = 禁用
	VNCVariant   string `json:"vncVariant,omitempty"`   // VNC 变体（如 "turbovnc"）
	EnableCgroup *bool  `json:"enableCgroup,omitempty"` // nil/false = 降权执行, true = 启用 Cgroup/Slice 隔离 (底层将申请提权)
}

type Connection struct {
	ID             uint              `json:"id" gorm:"primarykey"`
	Name           string            `json:"name" gorm:"type:varchar(128)"`
	Alias          string            `json:"alias" gorm:"type:varchar(100)"`
	Username       string            `json:"username" gorm:"index;uniqueIndex:idx_conn_node_user_port,priority:2,where:port > 0 AND target_node_id <> ''"`
	Shared         bool              `json:"shared" gorm:"index"` // true = 所有用户可见可连（如共享 Windows RDP）
	TargetNodeID   string            `json:"targetNodeId" gorm:"index;uniqueIndex:idx_conn_node_user_port,priority:1,where:port > 0 AND target_node_id <> '';uniqueIndex:idx_conn_shared_node_protocol,priority:1,where:shared = true AND target_node_id <> '' AND protocol <> ''"`
	Host           string            `json:"host" gorm:"type:varchar(45)"`
	Port           uint              `json:"port" gorm:"uniqueIndex:idx_conn_node_user_port,priority:3,where:port > 0 AND target_node_id <> ''"`
	Display        string            `json:"display" gorm:"type:varchar(32)"`
	PID            uint              `json:"pid,omitempty" gorm:"column:pid"`
	Status         ConnectionStatus  `json:"status" gorm:"index"`
	Source         ConnectionSource  `json:"source" gorm:"index"`
	Protocol       contract.Protocol `json:"protocol" gorm:"type:varchar(20);default:'';uniqueIndex:idx_conn_shared_node_protocol,priority:2,where:shared = true AND target_node_id <> '' AND protocol <> ''"`
	Extra          ConnectionExtra   `json:"extra" gorm:"type:text;serializer:json"`
	NodeClientHost string            `json:"nodeClientHost,omitempty" gorm:"->;-:migration"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	LastAccessedAt time.Time         `json:"lastAccessedAt"`
}

func (c Connection) SourceType() ConnectionSource {
	return c.Source
}

func (c Connection) IsOTPEnabled() bool {
	if c.Source == ConnectionSourceManual {
		return false
	}
	if c.Extra.OTPEnabled != nil {
		return *c.Extra.OTPEnabled
	}
	return true
}

func (c Connection) GetStartOptions() string {
	return c.Extra.StartOptions
}

func (c Connection) NeedsElevation() bool {
	if c.Extra.EnableCgroup != nil {
		return *c.Extra.EnableCgroup
	}
	return false
}
