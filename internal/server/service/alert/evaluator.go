// Package alert 提供集中式告警状态机。
// 每个「指标 × 目标」维护独立告警状态，仅在状态翻转（触发/恢复）或
// 冷却期到期（持续告警）时推送，防止消息洪泛。
package alert

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/settings"
)

// Evaluator 集中式告警状态机。
// 阈值读取自 DomainAlert 配置（settings），推送走 push.Hub（SSE），
// 日志统一在引擎内输出（alert_state=fire/repeat/recover），保证单一出口。
type Evaluator struct {
	hub      *push.Hub
	settings *settings.Service
	history  *db.AlertHistoryRepo
	logger   *slog.Logger

	mu    sync.Mutex
	state map[string]*alertState // key: ruleKey (如 "node.node-01.memory")
}

// NewEvaluator 创建告警引擎实例。
func NewEvaluator(hub *push.Hub, svc *settings.Service, logger *slog.Logger) *Evaluator {
	return &Evaluator{
		hub:      hub,
		settings: svc,
		logger:   logger,
		state:    make(map[string]*alertState),
	}
}

// isKeyActive 检查指定 key 当前是否处于告警激活态。
func (e *Evaluator) isKeyActive(key string) bool {
	e.mu.Lock()
	st, ok := e.state[key]
	e.mu.Unlock()
	if !ok {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.active
}

// isKeyTriggerFree 检查指定 key 当前激活的告警是否由容量空间不足（MinFreeSpace）触发。
func (e *Evaluator) isKeyTriggerFree(key string) bool {
	e.mu.Lock()
	st, ok := e.state[key]
	e.mu.Unlock()
	if !ok {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.active && st.triggerFree
}

// Eval 对单个指标进行阈值评估，并根据状态机决定是否推送告警/恢复通知。
// metric 为当前指标值（如内存使用率百分比），key 为状态机键（如 "node.n1.memory"）。
// 方法是线程安全的，可从多个 goroutine 并发调用。
func (e *Evaluator) Eval(ctx context.Context, key string, metric float64, rule Rule) {
	now := time.Now()
	e.mu.Lock()
	st, ok := e.state[key]
	if !ok {
		st = &alertState{}
		e.state[key] = st
	}
	e.mu.Unlock()

	st.mu.Lock()
	defer st.mu.Unlock()

	var exceeding bool
	if rule.Exceeding != nil {
		exceeding = *rule.Exceeding
	} else {
		margin := cmp.Or(rule.HysteresisMargin, DefaultHysteresisMargin)
		if rule.Below {
			if st.active {
				// 告警恢复需超过迟滞裕度（如空闲空间需回升至 threshold * (1 + margin)）
				exceeding = rule.Threshold > 0 && metric < rule.Threshold*(1+margin)
			} else {
				exceeding = rule.Threshold > 0 && metric < rule.Threshold
			}
		} else {
			if st.active {
				// 告警恢复需回落至 threshold * (1 - margin)
				exceeding = rule.Threshold > 0 && metric >= rule.Threshold*(1-margin)
			} else {
				exceeding = rule.Threshold > 0 && metric >= rule.Threshold
			}
		}
	}

	if rule.Threshold <= 0 && !exceeding && !st.active {
		return
	}

	switch {
	case exceeding && !st.active:
		// 新触发
		st.active = true
		st.triggerFree = rule.IsFreeSpace
		st.lastFire = now
		e.publishFire(ctx, key, metric, rule)
		e.logger.Warn("alert fired",
			"rule", key, "metric", metric, "threshold", rule.Threshold,
			"alert_state", "fire", "notify", "system-admin")

	case exceeding && st.active && now.Sub(st.lastFire) >= rule.Cooldown:
		// 持续超阈值，按冷却节律重推
		st.triggerFree = rule.IsFreeSpace
		st.lastFire = now
		e.publishFire(ctx, key, metric, rule)
		e.logger.Warn("alert recurring",
			"rule", key, "metric", metric, "threshold", rule.Threshold,
			"alert_state", "repeat", "notify", "system-admin")

	case !exceeding && st.active:
		// 恢复
		st.active = false
		st.triggerFree = false
		e.publishRecover(ctx, key, metric, rule)
		e.logger.Info("alert recovered",
			"rule", key, "metric", metric, "threshold", rule.Threshold,
			"alert_state", "recover", "notify", "system-admin")
	}
}

// EvalResource 对节点资源上报执行告警评估（内存/磁盘/CPU/负载）。
// 在 control 层入库后调用；仅在对应规则启用时评估，毫秒级内存操作，不阻塞采集。
func (e *Evaluator) EvalResource(ctx context.Context, nodeID string, report *pb.ResourceReport) error {
	if report == nil || e.settings == nil {
		return nil
	}
	rule, err := e.settings.GetEffectiveAlertSettingsForNode(ctx, nodeID)
	if err != nil {
		return err
	}
	if !rule.Enabled {
		return nil
	}
	nodeKey := "node." + nodeID

	// 内存使用率与最小剩余空间（方案 A：统一 Key node.{id}.memory + 逻辑 OR）
	if report.GetMemTotal() > 0 && rule.Memory.Enabled {
		totalMem := report.GetMemTotal()
		usedPct := float64(report.GetMemUsed()) / float64(report.GetMemTotal()) * 100
		freeKB := report.GetMemTotal() - report.GetMemUsed()
		memKey := nodeKey + ".memory"
		isActive := e.isKeyActive(memKey)

		pctExceeded := rule.Memory.MaxUsedPercent > 0 && usedPct >= rule.Memory.MaxUsedPercent
		// 只有当总内存大于 MinFreeSpace 时，MinFreeSpace 才生效，避免小规格内存节点误报
		freeExceeded := rule.Memory.MinFreeSpace > 0 && totalMem > rule.Memory.MinFreeSpace && freeKB < rule.Memory.MinFreeSpace

		if isActive {
			// 迟滞防抖：已处于告警态时，需回落超过裕度才算恢复
			if rule.Memory.MaxUsedPercent > 0 {
				pctExceeded = usedPct >= rule.Memory.MaxUsedPercent*(1-DefaultHysteresisMargin)
			}
			if rule.Memory.MinFreeSpace > 0 && totalMem > rule.Memory.MinFreeSpace {
				freeExceeded = float64(freeKB) < float64(rule.Memory.MinFreeSpace)*(1+DefaultHysteresisMargin)
			}
		}
		exceeding := pctExceeded || freeExceeded

		if exceeding || isActive {
			metric := usedPct
			threshold := rule.Memory.MaxUsedPercent
			isFreeSpace := false
			if freeExceeded && !pctExceeded {
				metric = float64(freeKB)
				threshold = float64(rule.Memory.MinFreeSpace)
				isFreeSpace = true
			} else if !exceeding && (e.isKeyTriggerFree(memKey) || (rule.Memory.MaxUsedPercent <= 0 && rule.Memory.MinFreeSpace > 0)) {
				metric = float64(freeKB)
				threshold = float64(rule.Memory.MinFreeSpace)
				isFreeSpace = true
			}
			e.Eval(ctx, memKey, metric, Rule{
				Threshold:   threshold,
				Cooldown:    time.Duration(rule.Memory.CooldownInterval) * time.Second,
				Level:       "warning",
				Exceeding:   &exceeding,
				IsFreeSpace: isFreeSpace,
			})
		}
	}

	// 磁盘使用率与最小剩余空间（每个挂载点统一 Key node.{id}.disk.{mount} + 逻辑 OR）
	if rule.Disk.Enabled {
		for _, du := range report.GetDiskUsages() {
			if du.GetTotal() > 0 {
				totalDisk := du.GetTotal()
				usedPct := float64(du.GetUsed()) / float64(du.GetTotal()) * 100
				freeKB := du.GetTotal() - du.GetUsed()
				mountKey := nodeKey + ".disk." + sanitizeKey(du.GetMountPoint())
				isActive := e.isKeyActive(mountKey)

				pctExceeded := rule.Disk.MaxUsedPercent > 0 && usedPct >= rule.Disk.MaxUsedPercent
				freeExceeded := rule.Disk.MinFreeSpace > 0 && totalDisk > rule.Disk.MinFreeSpace && freeKB < rule.Disk.MinFreeSpace

				if isActive {
					// 迟滞防抖：已处于告警态时，需回落超过裕度才算恢复
					if rule.Disk.MaxUsedPercent > 0 {
						pctExceeded = usedPct >= rule.Disk.MaxUsedPercent*(1-DefaultHysteresisMargin)
					}
					if rule.Disk.MinFreeSpace > 0 && totalDisk > rule.Disk.MinFreeSpace {
						freeExceeded = float64(freeKB) < float64(rule.Disk.MinFreeSpace)*(1+DefaultHysteresisMargin)
					}
				}
				exceeding := pctExceeded || freeExceeded

				if exceeding || isActive {
					metric := usedPct
					threshold := rule.Disk.MaxUsedPercent
					isFreeSpace := false
					if freeExceeded && !pctExceeded {
						metric = float64(freeKB)
						threshold = float64(rule.Disk.MinFreeSpace)
						isFreeSpace = true
					} else if !exceeding && (e.isKeyTriggerFree(mountKey) || (rule.Disk.MaxUsedPercent <= 0 && rule.Disk.MinFreeSpace > 0)) {
						metric = float64(freeKB)
						threshold = float64(rule.Disk.MinFreeSpace)
						isFreeSpace = true
					}
					e.Eval(ctx, mountKey, metric, Rule{
						Threshold:   threshold,
						Cooldown:    time.Duration(rule.Disk.CooldownInterval) * time.Second,
						Level:       "error",
						Exceeding:   &exceeding,
						IsFreeSpace: isFreeSpace,
					})
				}
			}
		}
	}

	// CPU / 负载（默认关闭，HPC 高利用率不算问题）。
	// 注意：cpu_util 是 0-1 比例（如 0.9=90%），需 *100 与 MaxUsedPercent(0-100) 对齐。
	if rule.CPU.Enabled && rule.CPU.MaxUsedPercent > 0 {
		e.Eval(ctx, nodeKey+".cpu", report.GetCpuUtil()*100, Rule{
			Threshold: rule.CPU.MaxUsedPercent,
			Cooldown:  time.Duration(rule.CPU.CooldownInterval) * time.Second,
			Level:     "warning",
		})
	}
	if rule.Load.Enabled && rule.Load.MaxLoadPercent > 0 && report.GetCpuCores() > 0 {
		loadPct := (report.GetLoad1() / float64(report.GetCpuCores())) * 100
		e.Eval(ctx, nodeKey+".load", loadPct, Rule{
			Threshold: rule.Load.MaxLoadPercent,
			Cooldown:  time.Duration(rule.Load.CooldownInterval) * time.Second,
			Level:     "warning",
		})
	}
	return nil
}

// EvalStorage 对共享存储 probe 上报执行告警评估（使用率 / 最小可用空间，统一 Key storage.{id} + 逻辑 OR）。
// 在 control 层入库后调用；任意维度命中即推送告警。
func (e *Evaluator) EvalStorage(ctx context.Context, report *pb.StorageProbeItem) error {
	if report == nil || e.settings == nil {
		return nil
	}
	alertRule, err := e.settings.GetAlertSettings(ctx)
	if err != nil {
		return err
	}
	st := alertRule.SharedStorage
	if !st.Enabled {
		return nil
	}

	total := report.GetTotal()
	used := report.GetUsed()
	freeKB := total - used
	stKey := "storage." + report.GetStorageId()

	if total > 0 {
		usedPct := float64(used) / float64(total) * 100
		isActive := e.isKeyActive(stKey)

		pctExceeded := st.MaxUsedPercent > 0 && usedPct >= st.MaxUsedPercent
		freeExceeded := st.MinFreeSpace > 0 && total > st.MinFreeSpace && freeKB < st.MinFreeSpace

		if isActive {
			// 迟滞防抖：已处于告警态时，需回落超过裕度才算恢复
			if st.MaxUsedPercent > 0 {
				pctExceeded = usedPct >= st.MaxUsedPercent*(1-DefaultHysteresisMargin)
			}
			if st.MinFreeSpace > 0 && total > st.MinFreeSpace {
				freeExceeded = float64(freeKB) < float64(st.MinFreeSpace)*(1+DefaultHysteresisMargin)
			}
		}
		exceeding := pctExceeded || freeExceeded

		if exceeding || isActive {
			metric := usedPct
			threshold := st.MaxUsedPercent
			isFreeSpace := false
			if freeExceeded && !pctExceeded {
				metric = float64(freeKB)
				threshold = float64(st.MinFreeSpace)
				isFreeSpace = true
			} else if !exceeding && (e.isKeyTriggerFree(stKey) || (st.MaxUsedPercent <= 0 && st.MinFreeSpace > 0)) {
				metric = float64(freeKB)
				threshold = float64(st.MinFreeSpace)
				isFreeSpace = true
			}
			e.Eval(ctx, stKey, metric, Rule{
				Threshold:   threshold,
				Cooldown:    time.Duration(st.CooldownInterval) * time.Second,
				Level:       "error",
				Exceeding:   &exceeding,
				IsFreeSpace: isFreeSpace,
			})
		}
	}
	return nil
}

// sanitizeKey 将挂载点路径转换为合法的告警状态 key（替换 / 和空格）。
func sanitizeKey(path string) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(strings.Trim(path, "/"))
}

// SetHistoryRepo 设置告警历史 DB 仓储，实现告警记录落盘（全量保留，不做删除截断）。
func (e *Evaluator) SetHistoryRepo(repo *db.AlertHistoryRepo) {
	e.history = repo
}

// GetHistory 查询最新的告警历史记录（最多 limit 条）。
func (e *Evaluator) GetHistory(ctx context.Context, limit int) ([]model.AlertHistory, error) {
	if e.history == nil {
		return []model.AlertHistory{}, nil
	}
	return e.history.List(ctx, limit)
}

// publishFire 推送告警触发/持续重推通知。
// Notify 必须用指针（omitempty 对 struct 值无效）。
func (e *Evaluator) publishFire(ctx context.Context, key string, metric float64, rule Rule) {
	level := cmp.Or(rule.Level, "warning")
	msgText := fmt.Sprintf("%s 当前值 %.2f，阈值 %.2f", key, metric, rule.Threshold)

	if e.history != nil {
		_ = e.history.Create(ctx, &model.AlertHistory{
			RuleKey:    key,
			Metric:     metric,
			Threshold:  rule.Threshold,
			AlertState: "fire",
			Level:      level,
			Message:    msgText,
			CreatedAt:  time.Now(),
		})
	}

	if e.hub == nil {
		return
	}
	e.hub.PublishToRole(ctx, model.RoleSystemAdmin, push.Message{
		Name: push.EventSystem,
		Payload: push.EventPayload{
			Action: "notify",
			Notify: &push.Notification{
				Title:   "资源告警触发",
				Message: msgText,
				Level:   level,
			},
		},
	})
}

// publishRecover 推送告警恢复通知。
func (e *Evaluator) publishRecover(ctx context.Context, key string, metric float64, rule Rule) {
	msgText := fmt.Sprintf("%s 已恢复（当前值 %.2f）", key, metric)

	if e.history != nil {
		_ = e.history.Create(ctx, &model.AlertHistory{
			RuleKey:    key,
			Metric:     metric,
			Threshold:  rule.Threshold,
			AlertState: "recover",
			Level:      "info",
			Message:    msgText,
			CreatedAt:  time.Now(),
		})
	}

	if e.hub == nil {
		return
	}
	e.hub.PublishToRole(ctx, model.RoleSystemAdmin, push.Message{
		Name: push.EventSystem,
		Payload: push.EventPayload{
			Action: "notify",
			Notify: &push.Notification{
				Title:   "告警恢复",
				Message: msgText,
				Level:   "info",
			},
		},
	})
}
