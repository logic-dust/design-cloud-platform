// noinspection GoMixedReceiver
package model

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"sort"
	"strings"
	"time"
)

type NodeStatus int

const (
	// NodeStatusUnset — no runtime status (node has not completed first connect).
	NodeStatusUnset NodeStatus = 0
	// NodeStatusOnline — connected; eligible for dispatch when registry agrees.
	NodeStatusOnline NodeStatus = 1
	// NodeStatusOffline — enrolled but not currently connected / heartbeat stale.
	NodeStatusOffline NodeStatus = 2
	// NodeStatusMaintenance — admin suspended; heartbeat/disconnect must not change status.
	NodeStatusMaintenance NodeStatus = 3
)

func (s NodeStatus) String() string {
	switch s {
	case NodeStatusOnline:
		return "online"
	case NodeStatusOffline:
		return "offline"
	case NodeStatusMaintenance:
		return "maintenance"
	case NodeStatusUnset:
		return ""
	default:
		return fmt.Sprintf("nodeStatus(%d)", s)
	}
}

func (s NodeStatus) IsRuntimeStatus() bool {
	return s == NodeStatusOnline || s == NodeStatusOffline || s == NodeStatusMaintenance
}

func ParseNodeStatus(s string) (NodeStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unset":
		return NodeStatusUnset, nil
	case "online":
		return NodeStatusOnline, nil
	case "offline":
		return NodeStatusOffline, nil
	case "maintenance":
		return NodeStatusMaintenance, nil
	default:
		return 0, fmt.Errorf("unknown node status: %q", s)
	}
}

func (s NodeStatus) MarshalJSON() ([]byte, error) {
	if !s.IsRuntimeStatus() {
		return json.Marshal(nil)
	}
	return json.Marshal(s.String())
}

func (s *NodeStatus) UnmarshalJSON(data []byte) error {
	var strVal *string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid NodeStatus: %s", string(data))
	}
	if strVal == nil || *strVal == "" {
		*s = NodeStatusUnset
		return nil
	}
	parsed, err := ParseNodeStatus(*strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

type Node struct {
	ID                       uint           `json:"id" gorm:"primarykey"`
	NodeID                   string         `json:"nodeId" gorm:"unique;not null;type:varchar(64)"`
	Groups                   []string       `json:"groups" gorm:"serializer:json"`
	IsServer                 bool           `json:"isServer" gorm:"not null;default:false"`
	Status                   NodeStatus     `json:"status" gorm:"index"`
	DialHost                 string         `json:"dialHost" gorm:"type:varchar(255)"`
	OS                       string         `json:"os" gorm:"type:varchar(32)"`
	Arch                     string         `json:"arch" gorm:"type:varchar(32)"`
	OSName                   string         `json:"osName" gorm:"type:varchar(128)"`
	OSVersion                string         `json:"osVersion" gorm:"type:varchar(64)"`
	KernelVersion            string         `json:"kernelVersion" gorm:"type:varchar(128)"`
	Description              string         `json:"description,omitempty" gorm:"type:varchar(512);not null;default:''"`
	Settings                 map[string]any `json:"settings" gorm:"serializer:json"`
	EnrollmentTokenHash      string         `json:"-" gorm:"type:varchar(64)"`
	EnrollmentTokenExpiresAt time.Time      `json:"-"`
	CertFingerprint          string         `json:"certFingerprint" gorm:"type:varchar(128)"`
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
	CertAlertStage     int16     `json:"certAlertStage" gorm:"not null;default:0;type:smallint"`
	LastHeartbeatAt    time.Time `json:"lastHeartbeatAt"`
	LastConnectedAt    time.Time `json:"lastConnectedAt"`
	LastDisconnectedAt time.Time `json:"lastDisconnectedAt"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// EnrollmentStage derives bootstrap phase from persisted facts (not stored as status).
func (n Node) EnrollmentStage() string {
	if n.CertFingerprint == "" {
		return "awaiting_certificate"
	}
	if n.LastConnectedAt.IsZero() {
		return "awaiting_connect"
	}
	return "enrolled"
}

// HasEnrolled reports whether the node completed at least one successful connect.
func (n Node) HasEnrolled() bool {
	return !n.LastConnectedAt.IsZero()
}

// NormalizeNodeGroups deduplicates and lexicographically sorts group names.
func NormalizeNodeGroups(groups []string) []string {
	if len(groups) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(groups))
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// NodeHasGroups reports whether nodeGroups contains every name in required (AND semantics).
func NodeHasGroups(nodeGroups, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(nodeGroups))
	for _, g := range nodeGroups {
		set[g] = struct{}{}
	}
	for _, need := range required {
		if _, ok := set[need]; !ok {
			return false
		}
	}
	return true
}

// NodeRuntime 节点最新运行时资源快照（按 node_id upsert）。
//
// 术语分层（数据本体按链路阶段命名，避免 metrics 一词贯穿所有层）：
//   - 上报帧：pb.Metrics（wire 契约）、Agent 采集任务 "metrics"；
//   - 控制面内存最新帧：registry.Connection.latestFrame / Snapshot.LatestFrame；
//   - 落库快照：本结构与 node_runtime 表；
//   - 读侧聚合缓存：overview.runtimeCache；
//   - 对外展示：ReqNodeStatusView 与 overview.Response 的 cpu/gpu/memory/load 资源块。
//
// 配置层的 metricsInterval / metricsRefreshInterval 只描述上报与读侧刷新节律，
// DefaultMetricsFlushInterval 与 *Stale* 只描述落盘与新鲜度窗口，均不指代本表。
type NodeRuntime struct {
	NodeID      string         `gorm:"primaryKey;type:varchar(64)"`
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

// TableName 固定单数表名，避免 GORM 复数化为 node_runtimes。
func (NodeRuntime) TableName() string { return "node_runtime" }

// NodeDiskInfo 单个挂载点的磁盘快照（序列化为 JSON 存入 node_runtime.disk_usages）。
type NodeDiskInfo struct {
	MountPoint  string `json:"mountPoint"`
	Filesystem  string `json:"filesystem"`
	MonitorName string `json:"monitorName"`
	Total       int64  `json:"total"` // 磁盘总容量，单位 KB (KiB)
	Used        int64  `json:"used"`  // 磁盘已用容量，单位 KB (KiB)
}
