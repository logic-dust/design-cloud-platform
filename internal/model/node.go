// noinspection GoMixedReceiver
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type NodeStatus int

const (
	// NodeStatusOnline --- 运行时实时状态 ---
	NodeStatusOnline  NodeStatus = 1 // online: 在线（正常运行中，gRPC 长连接保持）
	NodeStatusOffline NodeStatus = 2 // offline: 离线（断连或心跳超时）

	// NodeStatusPending --- 部署引导阶段状态 ---
	NodeStatusPending     NodeStatus = 3 // pending: 待引导（仅添加白名单，未领证书）
	NodeStatusProvisioned NodeStatus = 4 // provisioned: 已配置/已领证（证书下发完成，等待 Agent 首次上线）

	// NodeStatusMaintenance --- 管理员控制态 ---
	NodeStatusMaintenance NodeStatus = 5 // maintenance: 维护中（管理员手动下线/挂起，不参与调度）
)

func (s NodeStatus) String() string {
	switch s {
	case NodeStatusOnline:
		return "online"
	case NodeStatusOffline:
		return "offline"
	case NodeStatusPending:
		return "pending"
	case NodeStatusProvisioned:
		return "provisioned"
	case NodeStatusMaintenance:
		return "maintenance"
	default:
		return fmt.Sprintf("nodeStatus(%d)", s)
	}
}

func ParseNodeStatus(s string) (NodeStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "online":
		return NodeStatusOnline, nil
	case "offline":
		return NodeStatusOffline, nil
	case "pending":
		return NodeStatusPending, nil
	case "provisioned":
		return NodeStatusProvisioned, nil
	case "maintenance":
		return NodeStatusMaintenance, nil
	default:
		return 0, fmt.Errorf("unknown node status: %q", s)
	}
}

func (s NodeStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *NodeStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid NodeStatus: %s", string(data))
	}
	parsed, err := ParseNodeStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

type Node struct {
	ID              uint              `json:"id" gorm:"primarykey"`
	NodeID          string            `json:"nodeId" gorm:"unique;not null;type:varchar(64)"`
	IsServer        bool              `json:"isServer" gorm:"not null;default:false"`
	Status          NodeStatus        `json:"status" gorm:"not null;index"`
	ClientHost      string            `json:"clientHost" gorm:"type:varchar(255)"`
	OS              string            `json:"os" gorm:"type:varchar(32)"` // 操作系统（由 Agent 注册时上报，如 windows/linux）
	Labels          map[string]string `json:"labels" gorm:"serializer:json"`
	Settings        map[string]any    `json:"settings" gorm:"column:node_settings;serializer:json"`
	Token           string            `json:"-" gorm:"type:varchar(64)"`
	TokenCreatedAt  time.Time         `json:"tokenCreatedAt"`
	CertFingerprint string            `json:"certFingerprint" gorm:"type:varchar(128)"`
	// CertFingerprintPrev keeps the previous fingerprint so the renewal switch
	// window and .prev rollback remain authenticatable (dual fingerprint).
	CertFingerprintPrev string    `json:"-" gorm:"type:varchar(128)"`
	CertSubject         string    `json:"certSubject" gorm:"type:varchar(256)"`
	CertExpiresAt       time.Time `json:"certExpiresAt" gorm:"index"`
	// CertAutoRenew enables automatic renewal for this node (default true).
	CertAutoRenew bool `json:"certAutoRenew" gorm:"not null;default:true"`
	// AllowConnections controls whether this node accepts new desktop connections (default true).
	AllowConnections bool `json:"allowConnections" gorm:"not null;default:true"`
	// MaxConnections limits the non-shared connections on this node (0 = unlimited).
	MaxConnections uint `json:"maxConnections" gorm:"not null;default:0"`
	// CertAlertStage records the highest emitted expiry alert stage (0=none,
	// 90/30/7 days) to suppress duplicate alerts; reset on renewal.
	CertAlertStage  int16     `json:"certAlertStage" gorm:"not null;default:0;type:smallint"`
	LastHeartbeatAt time.Time `json:"lastHeartbeatAt"`
	LastSeenServer  string    `json:"lastSeenServer" gorm:"type:varchar(64)"`
	ConnectedAt     time.Time `json:"connectedAt"`
	DisconnectedAt  time.Time `json:"disconnectedAt"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type NodeResourceReport struct {
	ID          uint           `gorm:"primarykey"`
	NodeID      string         `gorm:"uniqueIndex;not null;type:varchar(64)"`
	Uptime      int64          // 运行时长，单位秒 (Seconds)
	ActiveTasks int64          // 当前活跃任务数
	CPUCores    int64          // CPU 核心数
	CPUUtil     float64        // CPU 利用率 (0-1 比例数值)
	MemTotal    int64          // 内存总量，单位 KB (KiB)
	MemUsed     int64          // 内存已用，单位 KB (KiB)
	SwapTotal   int64          // Swap 总量，单位 KB (KiB)
	SwapFree    int64          // Swap 剩余，单位 KB (KiB)
	Load1       float64        // 1 分钟平均负载
	Load5       float64        // 5 分钟平均负载
	Load15      float64        // 15 分钟平均负载
	GPUCount    int64          // GPU 设备数
	GPUMemTotal int64          // 显存总量，单位 KB (KiB)
	GPUMemUsed  int64          // 显存已用，单位 KB (KiB)
	GPUUtil     float64        // GPU 利用率 (0-1 比例数值)
	DiskUsages  []NodeDiskInfo `gorm:"serializer:json"`
	ReportedAt  time.Time      `gorm:"not null"` // agent 采集端打点时间
}

// NodeDiskInfo 单个挂载点的磁盘快照（序列化为 JSON 存入 node_resource_reports.disk_usages）。
type NodeDiskInfo struct {
	MountPoint  string `json:"mountPoint"`
	Filesystem  string `json:"filesystem"`
	MonitorName string `json:"monitorName"`
	Total       int64  `json:"total"` // 磁盘总容量，单位 KB (KiB)
	Used        int64  `json:"used"`  // 磁盘已用容量，单位 KB (KiB)
}
