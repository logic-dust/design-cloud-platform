package overview

import (
	"time"
)

type Response struct {
	Name      string         `json:"name"`
	Nodes     NodeStats      `json:"nodes"`
	CPU       CPUStats       `json:"cpu"`
	Load      LoadStats      `json:"load"`
	GPU       GPUStats       `json:"gpu"`
	Memory    MemoryStats    `json:"memory"`
	Scheduler SchedulerStats `json:"scheduler"`
	Storage   StorageStats   `json:"storage"`
	Alert     AlertView      `json:"alert"`
	SampledAt time.Time      `json:"sampledAt"`

	// 各数据块的采样时间：仅供聚合者做 SSE 指纹变更检测（overviewFingerprint），
	// 不对外暴露 JSON——聚合层时间戳前端不展示（顶栏统一显示 sampledAt，
	// 明细真实时间仅 storage.items[].sampledAt）。
	NodeSampledAt      time.Time `json:"-"`
	MetricsSampledAt   time.Time `json:"-"`
	SchedulerSampledAt time.Time `json:"-"`
	StorageSampledAt   time.Time `json:"-"`
}

type SchedulerStats struct {
	Hosts  SchedulerHostSummary `json:"hosts"`
	Jobs   JobStats             `json:"jobs"`
	Queues []map[string]any     `json:"queues"` // 调度器原生队列/分区记录（与 GET /api/scheduler/queues 同源）
}

type SchedulerHostSummary struct {
	Online int `json:"online"`
	Total  int `json:"total"`
}

type NodeStats struct {
	Configured  int64 `json:"configured"`
	Online      int64 `json:"online"`
	Offline     int64 `json:"offline"`
	Maintenance int64 `json:"maintenance"`
}

type CPUStats struct {
	Configured  int64   `json:"configured"`  //配置的总量（所有节点的CPU核数）
	Online      int64   `json:"online"`      //在线的CPU核数（即在线节点上的CPU核数）
	Utilization float64 `json:"utilization"` //在线CPU核的平均使用率（平均值，单位：0~1）
}

// LoadStats 在线节点 /proc/loadavg 聚合：各节点 load1/5/15 分别求和（原始 loadavg，非百分比）。
type LoadStats struct {
	Load1m  float64 `json:"1m"`
	Load5m  float64 `json:"5m"`
	Load15m float64 `json:"15m"`
}

type GPUNodesSubStats struct {
	Configured int64 `json:"configured"` //配置的总量（所有GPU节点）
	Online     int64 `json:"online"`
}

type GPUCardsSubStats struct {
	Configured  int64   `json:"configured"`  //配置的总量（所有GPU节点的GPU卡）
	Online      int64   `json:"online"`      //在线的GPU卡（即在线节点上的GPU卡）
	Utilization float64 `json:"utilization"` //在线GPU卡的平均使用率（平均值，单位：0~1）
}

type GPUVRAMSubStats struct {
	Configured float64 `json:"configured"` //配置的总量（所有GPU卡的显存）
	Online     float64 `json:"online"`     // 在线的显存（即在线GPU卡的显存）
	Used       float64 `json:"used"`       // 在线显存的使用量
	Unit       string  `json:"unit"`
}

type GPUStats struct {
	Nodes GPUNodesSubStats `json:"nodes"`
	Cards GPUCardsSubStats `json:"cards"`
	VRAM  GPUVRAMSubStats  `json:"vram"`
}

type MemoryStats struct {
	Configured float64 `json:"configured"`
	Online     float64 `json:"online"`
	Used       float64 `json:"used"`
	Unit       string  `json:"unit"`
}

type JobStats struct {
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Suspended int `json:"suspended"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

type StorageStats struct {
	Summary StorageSummary        `json:"summary"`
	Items   []SharedStorageStatus `json:"items"`
}

type StorageSummary struct {
	Total       float64 `json:"total"`
	Used        float64 `json:"used"`
	Unit        string  `json:"unit"`
	Utilization float64 `json:"utilization"`
}

type SharedStorageStatus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Total     float64   `json:"total"`
	Used      float64   `json:"used"`
	Unit      string    `json:"unit"`
	SampledAt time.Time `json:"sampledAt"`
}

// AlertView 携带前端视觉阈值（来自 DomainAlert 配置，避免硬编码）。
// 值为 0 表示该维度未启用，前端应回退到内置默认阈值。
type AlertView struct {
	// StorageUsedPercent 存储使用率红色阈值（>阈值显示红色）。
	StorageUsedPercent float64 `json:"storageUsedPercent"`
	// LoadRatio 负载相对核数倍率阈值（>阈值显示繁忙/超载）。
	LoadRatio float64 `json:"loadRatio"`
}
