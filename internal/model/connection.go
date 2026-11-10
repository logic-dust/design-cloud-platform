package model

import (
	"cmp"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"design-cloud-engine/internal/contract"
)

type ConnectionStatus int

const (
	// --- DB-persisted statuses (contiguous) ---
	ConnectionRunning     ConnectionStatus = 1
	ConnectionStopped     ConnectionStatus = 2
	ConnectionPending     ConnectionStatus = 3 // short-lived Launch/Start claim
	ConnectionTerminating ConnectionStatus = 4 // delete tombstone; hidden from user lists

	// --- API view overlays only (never written by lifecycle/probe writers) ---
	ConnectionUnknown     ConnectionStatus = 5 // node offline / unreachable
	ConnectionMaintenance ConnectionStatus = 6 // node in maintenance
)

func (s ConnectionStatus) String() string {
	switch s {
	case ConnectionRunning:
		return "running"
	case ConnectionStopped:
		return "stopped"
	case ConnectionPending:
		return "pending"
	case ConnectionTerminating:
		return "terminating"
	case ConnectionUnknown:
		return "unknown"
	case ConnectionMaintenance:
		return "maintenance"
	default:
		return fmt.Sprintf("connectionStatus(%d)", s)
	}
}

// IsPersisted reports whether s is a valid DB-written connection status.
func (s ConnectionStatus) IsPersisted() bool {
	switch s {
	case ConnectionRunning, ConnectionStopped, ConnectionPending, ConnectionTerminating:
		return true
	default:
		return false
	}
}

func ParseConnectionStatus(s string) (ConnectionStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return ConnectionRunning, nil
	case "stopped":
		return ConnectionStopped, nil
	case "pending":
		return ConnectionPending, nil
	case "terminating":
		return ConnectionTerminating, nil
	case "unknown":
		return ConnectionUnknown, nil
	case "maintenance":
		return ConnectionMaintenance, nil
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

// Connection 代表落点在宿主机上的具体物理端点实例（VNC/RDP/XRDP）。
// SSH 不写本表：走 GET /api/connections/ssh?resourceId= 的内存 proxy 隧道。
type Connection struct {
	ID             uint              `json:"id" gorm:"primaryKey"`
	ResourceID     *uint             `json:"resourceId,omitempty" gorm:"index"`
	Name           string            `json:"name" gorm:"size:64;not null"`
	Username       string            `json:"username" gorm:"size:64;not null;index;uniqueIndex:idx_conn_node_user_port,priority:2"`
	Protocol       contract.Protocol `json:"protocol" gorm:"not null"`
	NodeID         string            `json:"nodeId" gorm:"size:64;not null;index;uniqueIndex:idx_conn_node_user_port,priority:1;uniqueIndex:idx_conn_node_display,priority:1"`
	Host           string            `json:"host,omitempty" gorm:"-"` // API-only: resolved from nodes.dial_host at read/proxy time
	Port           uint              `json:"port" gorm:"not null;uniqueIndex:idx_conn_node_user_port,priority:3"`
	Display        *int              `json:"display,omitempty" gorm:"uniqueIndex:idx_conn_node_display,priority:2"`
	PID            *uint             `json:"pid,omitempty" gorm:"column:pid"`
	Status         ConnectionStatus  `json:"status" gorm:"index;not null;default:1"`
	LastAccessedAt *time.Time        `json:"lastAccessedAt,omitempty" gorm:"index"`
	// ControlledAt: VNC/XRDP seat — last Start/Stop or process finalize; probe grace window anchor.
	ControlledAt *time.Time `json:"controlledAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// MaxConnectionNameLength 是连接名称的最大长度（按字符计，对应 varchar(64)）。
const MaxConnectionNameLength = 64

// TruncateText 去除首尾空白后按字符（rune）截断到 maxRunes，
// 截断后再次去除尾部空白，保证结果为合法 UTF-8 且不超过列宽。
func TruncateText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimRightFunc(string(runes[:maxRunes]), unicode.IsSpace)
}

// TruncateConnectionName 按 TruncateText 规整连接名称到 MaxConnectionNameLength。
func TruncateConnectionName(s string) string {
	return TruncateText(s, MaxConnectionNameLength)
}

// ConnectionName 构造落库的连接名称 "<resource>-<node>"，并按 TruncateConnectionName 规整。
func ConnectionName(resourceName, nodeID string) string {
	return TruncateConnectionName(strings.TrimSpace(resourceName) + "-" + strings.TrimSpace(nodeID))
}

// DialHostFor resolves the TCP dial hostname for a node (dialHost override, else node ID).
func DialHostFor(nodeDialHost, nodeID string) string {
	if h := strings.TrimSpace(nodeDialHost); h != "" {
		return h
	}
	return strings.TrimSpace(nodeID)
}

// ProbedConnectionName 构造探针发现的 VNC 连接名称：优先使用桌面名（vncserver -name/-desktop），
// 为空时为 "<nodeId>:<port>"，并按 TruncateConnectionName 规整。
func ProbedConnectionName(desktopName, nodeID string, port uint) string {
	if name := TruncateConnectionName(desktopName); name != "" {
		return name
	}
	return TruncateConnectionName(fmt.Sprintf("%s:%d", cmp.Or(strings.TrimSpace(nodeID), "?"), port))
}

// ConnectionResource 表示连接资源规格定义（单机专机、多机直选、跨组动态池或公共跳板机）。
type ConnectionResource struct {
	ID         uint              `gorm:"primaryKey" json:"id"`
	Name       string            `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Protocol   contract.Protocol `gorm:"not null" json:"protocol"`
	Nodes      []string          `gorm:"type:jsonb;not null;default:'[]';serializer:json" json:"nodes"`
	NodeGroups []string          `gorm:"type:jsonb;not null;default:'[]';serializer:json" json:"nodeGroups"`
	Display    *int              `json:"display"` // nil=动态分配; 非 nil=固定桌面号（规格 API 校验 1..99）
	Port       *uint             `json:"port"`    // nil=由DcExec或协议默认兜底(3389/22)
	MaxPerUser int               `gorm:"not null;default:1" json:"maxPerUser"`
	MaxTotal   int               `gorm:"not null;default:0" json:"maxTotal"`            // 全局总实例上限（0=不限制）
	VNCConfig  json.RawMessage   `gorm:"type:jsonb;column:vnc_config" json:"vncConfig"` // VNC 启动参数(分辨率/WM等)
	Public     bool              `gorm:"not null;default:false" json:"public"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

// UserConnectionResource 表示门户用户与非公开连接资源规格的多对多授权映射。
type UserConnectionResource struct {
	UserID     uint      `gorm:"primaryKey" json:"userId"`
	ResourceID uint      `gorm:"primaryKey;index:idx_ucr_resource_id" json:"resourceId"`
	CreatedAt  time.Time `json:"createdAt"`
}
