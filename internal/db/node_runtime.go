package db

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---- Manager methods ----

func (r *NodeRepo) UpsertRuntime(ctx context.Context, metrics *model.NodeRuntime) error {
	if metrics == nil {
		return nil
	}
	if metrics.NodeID == "" {
		return nil
	}

	// Serialize DiskUsages manually: clause.Assignments bypasses GORM's
	// serializer:json tag, causing pgx to reject the raw struct.
	diskUsagesJSON, err := json.Marshal(metrics.DiskUsages)
	if err != nil {
		return fmt.Errorf("marshal disk_usages: %w", err)
	}

	return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"reported_at":   metrics.ReportedAt,
			"uptime":        metrics.Uptime,
			"active_tasks":  metrics.ActiveTasks,
			"cpu_cores":     metrics.CPUCores,
			"cpu_util":      metrics.CPUUtil,
			"mem_total":     metrics.MemTotal,
			"mem_used":      metrics.MemUsed,
			"swap_total":    metrics.SwapTotal,
			"swap_free":     metrics.SwapFree,
			"load1":         metrics.Load1,
			"load5":         metrics.Load5,
			"load15":        metrics.Load15,
			"gpu_count":     metrics.GPUCount,
			"gpu_mem_total": metrics.GPUMemTotal,
			"gpu_mem_used":  metrics.GPUMemUsed,
			"gpu_util":      metrics.GPUUtil,
			"disk_usages":   string(diskUsagesJSON),
		}),
	}).Create(metrics).Error
}

func (r *NodeRepo) GetRuntime(ctx context.Context, nodeID string) (model.NodeRuntime, error) {
	var metrics model.NodeRuntime
	if err := r.store.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&metrics).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.NodeRuntime{}, fmt.Errorf("metrics for node %q: %w", nodeID, contract.ErrNotFound)
		}
		return model.NodeRuntime{}, err
	}
	return metrics, nil
}

type AggregatedRuntime struct {
	TotalCores       int64   `gorm:"column:total_cores"`
	OnlineCores      int64   `gorm:"column:online_cores"`
	SumCPUUtil       float64 `gorm:"column:sum_cpu_util"`
	SumLoad1         float64 `gorm:"column:sum_load1"`
	SumLoad5         float64 `gorm:"column:sum_load5"`
	SumLoad15        float64 `gorm:"column:sum_load15"`
	TotalGPUNodes    int64   `gorm:"column:total_gpu_nodes"`
	OnlineGPUNodes   int64   `gorm:"column:online_gpu_nodes"`
	TotalGPUs        int64   `gorm:"column:total_gpus"`
	OnlineGPUs       int64   `gorm:"column:online_gpus"`
	ConfiguredGPUMem int64   `gorm:"column:configured_gpu_mem"`
	OnlineGPUMem     int64   `gorm:"column:online_gpu_mem"`
	TotalGPUUsed     int64   `gorm:"column:total_gpu_used"`
	SumGPUUtil       float64 `gorm:"column:sum_gpu_util"`
	ConfiguredMem    int64   `gorm:"column:configured_mem"`
	OnlineMem        int64   `gorm:"column:online_mem"`
	TotalMemUsed     int64   `gorm:"column:total_mem_used"`
}

func (r *NodeRepo) AggregateRuntime(ctx context.Context, staleBefore time.Time) (AggregatedRuntime, error) {
	var stats AggregatedRuntime

	// 资源聚合统计算法：
	//   - JOIN 所有存在于 nodes 注册表中的未删除节点；
	//   - configured_* 无条件 SUM（只要在 nodes 表即计入全网物理设计总容量）；
	//   - online_* & total_*_used 校验 n.status = online 且 reported_at > staleBefore（精确计算实时活跃能力与已用资源）。
	onlineStatus := model.NodeStatusOnline
	err := r.store.db.WithContext(ctx).
		Table("node_runtime m").
		Joins("JOIN nodes n ON n.node_id = m.node_id").
		Select(`
			COALESCE(SUM(m.cpu_cores), 0) as total_cores,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.cpu_cores ELSE 0 END), 0) as online_cores,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.cpu_util * m.cpu_cores ELSE 0 END), 0) as sum_cpu_util,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.load1 ELSE 0 END), 0) as sum_load1,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.load5 ELSE 0 END), 0) as sum_load5,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.load15 ELSE 0 END), 0) as sum_load15,
			COALESCE(SUM(CASE WHEN m.gpu_count > 0 THEN 1 ELSE 0 END), 0) as total_gpu_nodes,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.gpu_count > 0 AND m.reported_at > ? THEN 1 ELSE 0 END), 0) as online_gpu_nodes,
			COALESCE(SUM(m.gpu_count), 0) as total_gpus,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.gpu_count ELSE 0 END), 0) as online_gpus,
			COALESCE(SUM(m.gpu_mem_total), 0) as configured_gpu_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.gpu_mem_total ELSE 0 END), 0) as online_gpu_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.gpu_mem_used ELSE 0 END), 0) as total_gpu_used,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.gpu_util * m.gpu_count ELSE 0 END), 0) as sum_gpu_util,
			COALESCE(SUM(m.mem_total), 0) as configured_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.mem_total ELSE 0 END), 0) as online_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND m.reported_at > ? THEN m.mem_used ELSE 0 END), 0) as total_mem_used
		`,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
			onlineStatus, staleBefore,
		).
		Scan(&stats).Error

	if err != nil {
		return AggregatedRuntime{}, err
	}
	return stats, nil
}
