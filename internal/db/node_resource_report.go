package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---- Manager methods ----

func (r *NodeRepo) UpsertResourceReport(ctx context.Context, report *model.NodeResourceReport) error {
	if report == nil {
		return nil
	}
	if report.NodeID == "" {
		return nil
	}

	// Serialize DiskUsages manually: clause.Assignments bypasses GORM's
	// serializer:json tag, causing pgx to reject the raw struct.
	diskUsagesJSON, err := json.Marshal(report.DiskUsages)
	if err != nil {
		return fmt.Errorf("marshal disk_usages: %w", err)
	}

	return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"reported_at":   report.ReportedAt,
			"uptime":        report.Uptime,
			"active_tasks":  report.ActiveTasks,
			"cpu_cores":     report.CPUCores,
			"cpu_util":      report.CPUUtil,
			"mem_total":     report.MemTotal,
			"mem_used":      report.MemUsed,
			"swap_total":    report.SwapTotal,
			"swap_free":     report.SwapFree,
			"load1":         report.Load1,
			"load5":         report.Load5,
			"load15":        report.Load15,
			"gpu_count":     report.GPUCount,
			"gpu_mem_total": report.GPUMemTotal,
			"gpu_mem_used":  report.GPUMemUsed,
			"gpu_util":      report.GPUUtil,
			"disk_usages":   string(diskUsagesJSON),
		}),
	}).Create(report).Error
}

func (r *NodeRepo) GetResourceReport(ctx context.Context, nodeID string) (model.NodeResourceReport, error) {
	var report model.NodeResourceReport
	if err := r.store.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&report).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.NodeResourceReport{}, fmt.Errorf("resource report for node %q: %w", nodeID, common.ErrNotFound)
		}
		return model.NodeResourceReport{}, err
	}
	return report, nil
}

func (r *NodeRepo) ListResourceReports(ctx context.Context) ([]model.NodeResourceReport, error) {
	var reports []model.NodeResourceReport
	if err := r.store.db.WithContext(ctx).Find(&reports).Error; err != nil {
		return nil, err
	}
	return reports, nil
}

type AggregatedResourceStats struct {
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

func (r *NodeRepo) DeleteResourceReportByNodeID(ctx context.Context, nodeID string) error {
	if nodeID == "" {
		return nil
	}
	return r.store.db.WithContext(ctx).Where("node_id = ?", nodeID).Delete(&model.NodeResourceReport{}).Error
}

func (r *NodeRepo) AggregateResourceStats(ctx context.Context, staleBefore time.Time) (AggregatedResourceStats, error) {
	var stats AggregatedResourceStats

	// 资源聚合统计算法：
	//   - JOIN 所有存在于 nodes 注册表中的未删除节点；
	//   - configured_* 无条件 SUM（只要在 nodes 表即计入全网物理设计总容量）；
	//   - online_* & total_*_used 校验 n.status = online 且 reported_at > staleBefore（精确计算实时活跃能力与已用资源）。
	onlineStatus := model.NodeStatusOnline
	err := r.store.db.WithContext(ctx).
		Table("node_resource_reports r").
		Joins("JOIN nodes n ON n.node_id = r.node_id").
		Select(`
			COALESCE(SUM(r.cpu_cores), 0) as total_cores,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.cpu_cores ELSE 0 END), 0) as online_cores,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.cpu_util * r.cpu_cores ELSE 0 END), 0) as sum_cpu_util,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.load1 ELSE 0 END), 0) as sum_load1,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.load5 ELSE 0 END), 0) as sum_load5,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.load15 ELSE 0 END), 0) as sum_load15,
			COALESCE(SUM(CASE WHEN r.gpu_count > 0 THEN 1 ELSE 0 END), 0) as total_gpu_nodes,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.gpu_count > 0 AND r.reported_at > ? THEN 1 ELSE 0 END), 0) as online_gpu_nodes,
			COALESCE(SUM(r.gpu_count), 0) as total_gpus,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.gpu_count ELSE 0 END), 0) as online_gpus,
			COALESCE(SUM(r.gpu_mem_total), 0) as configured_gpu_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.gpu_mem_total ELSE 0 END), 0) as online_gpu_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.gpu_mem_used ELSE 0 END), 0) as total_gpu_used,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.gpu_util * r.gpu_count ELSE 0 END), 0) as sum_gpu_util,
			COALESCE(SUM(r.mem_total), 0) as configured_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.mem_total ELSE 0 END), 0) as online_mem,
			COALESCE(SUM(CASE WHEN n.status = ? AND r.reported_at > ? THEN r.mem_used ELSE 0 END), 0) as total_mem_used
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
		return AggregatedResourceStats{}, err
	}
	return stats, nil
}
