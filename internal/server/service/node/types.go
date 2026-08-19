package node

import (
	"encoding/json"
	"time"

	"design-cloud-engine/internal/common/format"
	"design-cloud-engine/internal/model"
)

// TokenExpiry is the lifetime of a bootstrap token.
const TokenExpiry = 24 * time.Hour

// UpdateNodeRequest holds the mutable fields for a node update.
// All fields are optional; zero values mean "not provided".
// IsServer uses a pointer to distinguish zero from unset.
type UpdateNodeRequest struct {
	IsServer         *bool             // optional; true = server-plane role
	Status           model.NodeStatus  // optional; use NodeStatusMaintenance to set/clear maintenance mode
	ClientHost       *string           // optional; host/IP/FQDN used by direct clients
	Labels           map[string]string // optional; if provided (not nil), overwrites current labels
	AllowConnections *bool             // optional; true = allow new connections on this node
	MaxConnections   *uint             // optional; connection capacity limit (0 = unlimited)
}

// ReqCreateNodeForm defines the writable fields accepted when creating/registering a node.
type ReqCreateNodeForm struct {
	NodeID           string            `json:"nodeId"`
	IsServer         bool              `json:"isServer"`
	Status           model.NodeStatus  `json:"status"`
	ClientHost       string            `json:"clientHost"`
	Labels           map[string]string `json:"labels"`
	AllowConnections *bool             `json:"allowConnections,omitempty"`
	MaxConnections   uint              `json:"maxConnections,omitempty"`
}

// AgentConfig holds the configuration required to start a cluster agent node.
type AgentConfig struct {
	ServerAddr string `json:"serverAddr"`
	NodeID     string `json:"nodeId"`
	Token      string `json:"token"`
	CertPath   string `json:"certPath"`
	KeyPath    string `json:"keyPath"`
	CAPath     string `json:"caPath"`
}

type CreateClusterCertRequest struct {
	NodeID    string
	Hosts     []string
	ValidDays int
	SyncNode  bool
}

// OfflineIssueCertRequest defines the parameters for offline certificate issuance.
type OfflineIssueCertRequest struct {
	NodeID    string
	IsServer  bool
	Hosts     []string
	ValidDays int
	CSRPEM    []byte // Optional: if provided, signs this CSR; if empty, generates a new private key
	Force     bool   // Force overwrite if node already has an active certificate
}

// OfflineIssueCertResult holds the generated materials and metadata.
type OfflineIssueCertResult struct {
	CertPEM     []byte
	KeyPEM      []byte // empty when CSRPEM was provided
	CAPEM       []byte
	Fingerprint string
	Subject     string
	ExpiresAt   time.Time
	IsNewNode   bool
}

// StatusCPU describes current CPU utilization.
type StatusCPU struct {
	Utilization float64 `json:"utilization"` // CPU 使用率比例 (0.0000 - 1.0000)
	Cores       int64   `json:"cores"`
}

// StatusMemory describes current memory usage (values in KB / KiB from /proc/meminfo).
type StatusMemory struct {
	Total     int64 `json:"total"`     // 总内存，单位 KB (KiB)
	Used      int64 `json:"used"`      // 已用内存，单位 KB (KiB)
	SwapTotal int64 `json:"swapTotal"` // Swap 总量，单位 KB (KiB)
	SwapFree  int64 `json:"swapFree"`  // Swap 剩余，单位 KB (KiB)
}

// StatusLoad describes current load averages.
type StatusLoad struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// StatusGPU describes current GPU summary metrics.
type StatusGPU struct {
	Count       int64   `json:"count"`
	Utilization float64 `json:"utilization"` // GPU 使用率比例 (0.0000 - 1.0000)
}

// ReqNodeStatusView is the runtime status payload exposed by /api/nodes/{id}/status.
type ReqNodeStatusView struct {
	NodeID        string       `json:"nodeId"`
	ReportedAt    time.Time    `json:"reportedAt"`
	UptimeSeconds int64        `json:"uptimeSeconds"`
	ActiveTasks   int64        `json:"activeTasks"`
	CPU           StatusCPU    `json:"cpu"`
	Memory        StatusMemory `json:"memory"`
	Load          StatusLoad   `json:"load"`
	GPU           StatusGPU    `json:"gpu"`
	Disk          []StatusDisk `json:"disk,omitempty"`
}

// StatusDisk describes disk usage for a monitored mount point (values in KB / KiB from statfs).
type StatusDisk struct {
	MountPoint  string `json:"mountPoint"`
	Total       int64  `json:"total"` // 磁盘总容量，单位 KB (KiB)
	Used        int64  `json:"used"`  // 磁盘已用容量，单位 KB (KiB)
	MonitorName string `json:"monitorName,omitempty"`
}

// BuildNodeStatusView maps a resource snapshot into the public runtime status payload.
// Disk data is read from the embedded report.DiskUsages JSON column.
func BuildNodeStatusView(report model.NodeResourceReport) ReqNodeStatusView {
	view := ReqNodeStatusView{
		NodeID:        report.NodeID,
		ReportedAt:    report.ReportedAt,
		UptimeSeconds: report.Uptime,
		ActiveTasks:   report.ActiveTasks,
		CPU: StatusCPU{
			Utilization: format.RoundTo(report.CPUUtil, 4),
			Cores:       report.CPUCores,
		},
		Memory: StatusMemory{
			Total:     report.MemTotal,
			Used:      report.MemUsed,
			SwapTotal: report.SwapTotal,
			SwapFree:  report.SwapFree,
		},
		Load: StatusLoad{
			Load1:  report.Load1,
			Load5:  report.Load5,
			Load15: report.Load15,
		},
		GPU: StatusGPU{
			Count:       report.GPUCount,
			Utilization: format.RoundTo(report.GPUUtil, 4),
		},
	}

	if len(report.DiskUsages) > 0 {
		view.Disk = make([]StatusDisk, 0, len(report.DiskUsages))
		for _, d := range report.DiskUsages {
			view.Disk = append(view.Disk, StatusDisk{
				MountPoint:  d.MountPoint,
				Total:       d.Total,
				Used:        d.Used,
				MonitorName: d.MonitorName,
			})
		}
	}
	return view
}

// NodeSettingsBatchSelector defines search criteria for selecting nodes to apply batch settings updates.
type NodeSettingsBatchSelector struct {
	NodeIDs []string          `json:"nodeIds,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	OS      []string          `json:"os,omitempty"`
	Status  []string          `json:"status,omitempty"`
}

// IsEmpty returns true if no filtering criteria are specified.
func (s *NodeSettingsBatchSelector) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.NodeIDs) == 0 && len(s.Labels) == 0 && len(s.OS) == 0 && len(s.Status) == 0
}

// BatchUpdateNodeSettingsRequest is the payload for POST /api/admin/system/nodes/settings/batch.
type BatchUpdateNodeSettingsRequest struct {
	Selector *NodeSettingsBatchSelector `json:"selector"`
	Patch    json.RawMessage            `json:"patch"`
	DryRun   bool                       `json:"dryRun"`
}

// BatchNodeFailure records a failure for a specific node during batch update.
type BatchNodeFailure struct {
	NodeID  string `json:"nodeId"`
	Message string `json:"message"`
}

// BatchNodeSettingsResponse is the result returned by POST /api/admin/system/nodes/settings/batch.
type BatchNodeSettingsResponse struct {
	Matched   int                `json:"matched"`
	Updated   int                `json:"updated"`
	Unchanged int                `json:"unchanged"`
	DryRun    bool               `json:"dryRun"`
	Failures  []BatchNodeFailure `json:"failures,omitempty"`
}
