package node

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"time"
	"unicode/utf8"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/format"
)

// TokenExpiry is the lifetime of an enrollment token.
const TokenExpiry = 24 * time.Hour

// EnrollmentTokenResponse is returned once when a plaintext enrollment token is issued.
type EnrollmentTokenResponse struct {
	NodeID    string    `json:"nodeId"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// UpdateNodeRequest holds mutable admin fields for PUT /api/admin/system/nodes/:id.
type UpdateNodeRequest struct {
	IsServer         *bool
	Maintenance      *bool // true → maintenance; false → exit maintenance (runtime status derived)
	DialHost         *string
	Groups           []string // nil = omit; non-nil replaces entire list
	Description      *string  // nil = omit; pointer to "" clears
	AllowConnections *bool
	MaxConnections   *uint
}

// ReqCreateNodeForm defines writable fields for POST /api/admin/system/nodes.
type ReqCreateNodeForm struct {
	NodeID           string   `json:"nodeId"`
	IsServer         bool     `json:"isServer"`
	DialHost         string   `json:"dialHost"`
	Description      string   `json:"description"`
	Groups           []string `json:"groups"`
	AllowConnections *bool    `json:"allowConnections,omitempty"`
	MaxConnections   uint     `json:"maxConnections,omitempty"`
}

// AgentConfig holds the configuration required to start a cluster agent node.
type AgentConfig struct {
	ServerAddr string `json:"serverAddr"`
	NodeID     string `json:"nodeId"`
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

type OfflineIssueCertRequest struct {
	NodeID    string
	IsServer  bool
	Hosts     []string
	ValidDays int
	CSRPEM    []byte
	Force     bool
}

type OfflineIssueCertResult struct {
	CertPEM     []byte
	KeyPEM      []byte
	CAPEM       []byte
	Fingerprint string
	Subject     string
	ExpiresAt   time.Time
	IsNewNode   bool
}

type StatusCPU struct {
	Utilization float64 `json:"utilization"`
	Cores       int64   `json:"cores"`
}

type StatusMemory struct {
	Total     int64 `json:"total"`
	Used      int64 `json:"used"`
	SwapTotal int64 `json:"swapTotal"`
	SwapFree  int64 `json:"swapFree"`
}

type StatusLoad struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type StatusGPU struct {
	Count       int64   `json:"count"`
	Utilization float64 `json:"utilization"`
}

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

type StatusDisk struct {
	MountPoint  string `json:"mountPoint"`
	Total       int64  `json:"total"`
	Used        int64  `json:"used"`
	MonitorName string `json:"monitorName,omitempty"`
}

func BuildNodeStatusView(metrics model.NodeRuntime) ReqNodeStatusView {
	view := ReqNodeStatusView{
		NodeID:        metrics.NodeID,
		ReportedAt:    metrics.ReportedAt,
		UptimeSeconds: metrics.Uptime,
		ActiveTasks:   metrics.ActiveTasks,
		CPU: StatusCPU{
			Utilization: format.RoundTo(metrics.CPUUtil, 4),
			Cores:       metrics.CPUCores,
		},
		Memory: StatusMemory{
			Total:     metrics.MemTotal,
			Used:      metrics.MemUsed,
			SwapTotal: metrics.SwapTotal,
			SwapFree:  metrics.SwapFree,
		},
		Load: StatusLoad{
			Load1:  metrics.Load1,
			Load5:  metrics.Load5,
			Load15: metrics.Load15,
		},
		GPU: StatusGPU{
			Count:       metrics.GPUCount,
			Utilization: format.RoundTo(metrics.GPUUtil, 4),
		},
	}
	if len(metrics.DiskUsages) > 0 {
		view.Disk = make([]StatusDisk, 0, len(metrics.DiskUsages))
		for _, d := range metrics.DiskUsages {
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

// NodeAdminView is the admin JSON projection for node records.
type NodeAdminView struct {
	ID                 uint           `json:"id"`
	NodeID             string         `json:"nodeId"`
	Groups             []string       `json:"groups"`
	IsServer           bool           `json:"isServer"`
	DialHost           string         `json:"dialHost,omitempty"`
	OS                 string         `json:"os,omitempty"`
	Arch               string         `json:"arch,omitempty"`
	OSName             string         `json:"osName,omitempty"`
	OSVersion          string         `json:"osVersion,omitempty"`
	KernelVersion      string         `json:"kernelVersion,omitempty"`
	Description        string         `json:"description,omitempty"`
	Settings           map[string]any `json:"settings,omitempty"`
	CertFingerprint    string         `json:"certFingerprint,omitempty"`
	CertSubject        string         `json:"certSubject,omitempty"`
	CertExpiresAt      time.Time      `json:"certExpiresAt"`
	CertAutoRenew      bool           `json:"certAutoRenew"`
	AllowConnections   bool           `json:"allowConnections"`
	MaxConnections     uint           `json:"maxConnections"`
	LastHeartbeatAt    time.Time      `json:"lastHeartbeatAt"`
	LastConnectedAt    time.Time      `json:"lastConnectedAt"`
	LastDisconnectedAt time.Time      `json:"lastDisconnectedAt"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
	EnrollmentStage    string         `json:"enrollmentStage"`
	Status             *string        `json:"status,omitempty"`
}

func BuildNodeAdminView(node model.Node) NodeAdminView {
	groups := node.Groups
	if groups == nil {
		groups = []string{}
	}
	view := NodeAdminView{
		ID:                 node.ID,
		NodeID:             node.NodeID,
		Groups:             groups,
		IsServer:           node.IsServer,
		DialHost:           node.DialHost,
		OS:                 node.OS,
		Arch:               node.Arch,
		OSName:             node.OSName,
		OSVersion:          node.OSVersion,
		KernelVersion:      node.KernelVersion,
		Description:        node.Description,
		Settings:           node.Settings,
		CertFingerprint:    node.CertFingerprint,
		CertSubject:        node.CertSubject,
		CertExpiresAt:      node.CertExpiresAt,
		CertAutoRenew:      node.CertAutoRenew,
		AllowConnections:   node.AllowConnections,
		MaxConnections:     node.MaxConnections,
		LastHeartbeatAt:    node.LastHeartbeatAt,
		LastConnectedAt:    node.LastConnectedAt,
		LastDisconnectedAt: node.LastDisconnectedAt,
		CreatedAt:          node.CreatedAt,
		UpdatedAt:          node.UpdatedAt,
		EnrollmentStage:    node.EnrollmentStage(),
	}
	if node.HasEnrolled() && node.Status.IsRuntimeStatus() {
		st := node.Status.String()
		view.Status = &st
	}
	return view
}

func validateDescription(desc string) error {
	if utf8.RuneCountInString(desc) > 512 {
		return contract.NewValidationError("description exceeds 512 characters")
	}
	return nil
}

// NodeSettingsBatchSelector defines search criteria for batch settings updates.
type NodeSettingsBatchSelector struct {
	NodeIDs []string `json:"nodeIds,omitempty"`
	Groups  []string `json:"groups,omitempty"`
	OS      []string `json:"os,omitempty"`
	Arch    []string `json:"arch,omitempty"`
	Status  []string `json:"status,omitempty"`
}

func (s *NodeSettingsBatchSelector) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.NodeIDs) == 0 && len(s.Groups) == 0 && len(s.OS) == 0 && len(s.Arch) == 0 && len(s.Status) == 0
}

type BatchUpdateNodeSettingsRequest struct {
	Selector *NodeSettingsBatchSelector `json:"selector"`
	Patch    json.RawMessage            `json:"patch"`
	DryRun   bool                       `json:"dryRun"`
}

type BatchNodeFailure struct {
	NodeID  string `json:"nodeId"`
	Message string `json:"message"`
}

type BatchNodeSettingsResponse struct {
	Matched   int                `json:"matched"`
	Updated   int                `json:"updated"`
	Unchanged int                `json:"unchanged"`
	DryRun    bool               `json:"dryRun"`
	Failures  []BatchNodeFailure `json:"failures,omitempty"`
}

const maxNodeBatchItems = 100

// BatchNodeUpdateItem is one sparse update in POST /api/admin/system/nodes/batch-update.
// Omitted pointer/nil slice fields are left unchanged (same as PUT /nodes/:id).
type BatchNodeUpdateItem struct {
	NodeID           string   `json:"nodeId"`
	IsServer         *bool    `json:"isServer"`
	Maintenance      *bool    `json:"maintenance"`
	DialHost         *string  `json:"dialHost"`
	Groups           []string `json:"groups"`
	Description      *string  `json:"description"`
	AllowConnections *bool    `json:"allowConnections"`
	MaxConnections   *uint    `json:"maxConnections"`
}

func (item BatchNodeUpdateItem) hasFields() bool {
	return item.IsServer != nil ||
		item.Maintenance != nil ||
		item.DialHost != nil ||
		item.Groups != nil ||
		item.Description != nil ||
		item.AllowConnections != nil ||
		item.MaxConnections != nil
}

func (item BatchNodeUpdateItem) toUpdateRequest() UpdateNodeRequest {
	req := UpdateNodeRequest{
		IsServer:         item.IsServer,
		Maintenance:      item.Maintenance,
		DialHost:         item.DialHost,
		AllowConnections: item.AllowConnections,
		MaxConnections:   item.MaxConnections,
	}
	if item.Groups != nil {
		req.Groups = item.Groups
	}
	if item.Description != nil {
		req.Description = item.Description
	}
	return req
}

// BatchNodeUpdateFailure records one item that did not update.
type BatchNodeUpdateFailure struct {
	Index   int    `json:"index"`
	NodeID  string `json:"nodeId"`
	Message string `json:"message"`
}

// BatchNodeUpdateResponse is the partial-success result of AdminBatchUpdate.
type BatchNodeUpdateResponse struct {
	Updated  int                      `json:"updated"`
	Failures []BatchNodeUpdateFailure `json:"failures,omitempty"`
}
