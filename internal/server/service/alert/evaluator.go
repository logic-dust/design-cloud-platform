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
	"design-cloud-engine/internal/server/push"
)

// Evaluator 集中式告警状态机。
// 阈值读取自 DomainAlert 配置（settings），推送走 push.Hub（SSE），
// 日志统一在引擎内输出（alert_state=fire/repeat/recover），保证单一出口。
type Evaluator struct {
	hub      *push.Hub
	settings SettingsReader
	history  *db.AlertHistoryRepo
	logger   *slog.Logger

	mu    sync.Mutex
	state map[string]*alertState // key: ruleKey (如 "node.node-01.memory")
}

// NewEvaluator 创建告警引擎实例。
// historyRepo 可选——nil 时 GetHistory 返回空列表，但告警引擎本身不受影响。
func NewEvaluator(hub *push.Hub, svc SettingsReader, historyRepo *db.AlertHistoryRepo, logger *slog.Logger) *Evaluator {
	return &Evaluator{
		hub:      hub,
		settings: svc,
		history:  historyRepo,
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

// capacityAlertConfig 封装容量双阈值判定（使用率上限 + 剩余容量下限）配置。
type capacityAlertConfig struct {
	MaxUsedPercent   float64 // 0=不启用
	MinFreeSpace     int64   // 单位 KiB，0=不启用
	CooldownInterval int     // 冷却时间（秒）
	Level            string  // 告警级别 "warning" | "error"
}

// evalCapacity 针对具有「使用率上限（百分比）」与「剩余容量下限（KiB）」双维度的容量指标执行统一告警评估。
// 内存、各磁盘挂载点以及共享存储统一复用此判定收敛逻辑。
func (e *Evaluator) evalCapacity(ctx context.Context, key string, total, used int64, cfg capacityAlertConfig) {
	if total <= 0 {
		return
	}
	usedPct := float64(used) / float64(total) * 100
	freeKiB := total - used
	isActive := e.isKeyActive(key)

	pctExceeded := cfg.MaxUsedPercent > 0 && usedPct >= cfg.MaxUsedPercent
	// 只有当总量大于 MinFreeSpace 时，MinFreeSpace 才生效，避免小规格目标误报
	freeExceeded := cfg.MinFreeSpace > 0 && total > cfg.MinFreeSpace && freeKiB < cfg.MinFreeSpace

	if isActive {
		// 迟滞防抖：已处于告警态时，需回落超过裕度才算恢复
		if cfg.MaxUsedPercent > 0 {
			pctExceeded = usedPct >= cfg.MaxUsedPercent*(1-DefaultHysteresisMargin)
		}
		if cfg.MinFreeSpace > 0 && total > cfg.MinFreeSpace {
			freeExceeded = float64(freeKiB) < float64(cfg.MinFreeSpace)*(1+DefaultHysteresisMargin)
		}
	}
	exceeding := pctExceeded || freeExceeded

	if !exceeding && !isActive {
		return
	}

	// 稳定判定：防跳变选择 metric、threshold 与 unit
	var (
		metric      float64
		threshold   float64
		isFreeSpace bool
		unit        string
	)

	// 1. 若当前处于激活态，优先沿用上次触发的量纲（只要该维度仍超标），防止持续重推时消息在百分比与容量间跳变
	wasTriggerFree := e.isKeyTriggerFree(key)
	if isActive && exceeding {
		if wasTriggerFree && freeExceeded {
			metric = float64(freeKiB)
			threshold = float64(cfg.MinFreeSpace)
			isFreeSpace = true
			unit = "KiB"
		} else if !wasTriggerFree && pctExceeded {
			metric = usedPct
			threshold = cfg.MaxUsedPercent
			isFreeSpace = false
			unit = "%"
		}
	}

	// 2. 新触发或触发维度转移时：优先容量不足告警（更为具体严重），其次使用率告警
	if unit == "" {
		if freeExceeded && !pctExceeded {
			metric = float64(freeKiB)
			threshold = float64(cfg.MinFreeSpace)
			isFreeSpace = true
			unit = "KiB"
		} else if !exceeding && (wasTriggerFree || (cfg.MaxUsedPercent <= 0 && cfg.MinFreeSpace > 0)) {
			// 恢复态且由 FreeSpace 触发或仅启用了 MinFreeSpace
			metric = float64(freeKiB)
			threshold = float64(cfg.MinFreeSpace)
			isFreeSpace = true
			unit = "KiB"
		} else {
			metric = usedPct
			threshold = cfg.MaxUsedPercent
			isFreeSpace = false
			unit = "%"
		}
	}

	e.Eval(ctx, key, metric, Rule{
		Threshold:   threshold,
		Cooldown:    time.Duration(cfg.CooldownInterval) * time.Second,
		Level:       cfg.Level,
		Exceeding:   &exceeding,
		IsFreeSpace: isFreeSpace,
		Unit:        unit,
	})
}

// EvalMetrics 对节点资源指标执行告警评估（内存/磁盘/CPU/负载）。
// 在 control 层入库后调用；仅在对应规则启用时评估，毫秒级内存操作，不阻塞采集。
func (e *Evaluator) EvalMetrics(ctx context.Context, nodeID string, metrics *pb.Metrics) error {
	if metrics == nil || e.settings == nil {
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

	// 1. 内存使用率与最小剩余空间
	if metrics.GetMemTotal() > 0 && rule.Memory.Enabled {
		e.evalCapacity(ctx, nodeKey+".memory", metrics.GetMemTotal(), metrics.GetMemUsed(), capacityAlertConfig{
			MaxUsedPercent:   rule.Memory.MaxUsedPercent,
			MinFreeSpace:     rule.Memory.MinFreeSpace,
			CooldownInterval: rule.Memory.CooldownInterval,
			Level:            "warning",
		})
	}

	// 2. 磁盘使用率与最小剩余空间（每个挂载点统一独立状态机）
	if rule.Disk.Enabled {
		for _, du := range metrics.GetDiskUsages() {
			if du.GetTotal() > 0 {
				mountKey := nodeKey + ".disk." + sanitizeKey(du.GetMountPoint())
				e.evalCapacity(ctx, mountKey, du.GetTotal(), du.GetUsed(), capacityAlertConfig{
					MaxUsedPercent:   rule.Disk.MaxUsedPercent,
					MinFreeSpace:     rule.Disk.MinFreeSpace,
					CooldownInterval: rule.Disk.CooldownInterval,
					Level:            "error",
				})
			}
		}
	}

	// 3. CPU / 负载（默认关闭，HPC 高利用率不算问题）。
	// 注意：cpu_util 是 0-1 比例（如 0.9=90%），需 *100 与 MaxUsedPercent(0-100) 对齐。
	if rule.CPU.Enabled && rule.CPU.MaxUsedPercent > 0 {
		e.Eval(ctx, nodeKey+".cpu", metrics.GetCpuUtil()*100, Rule{
			Threshold: rule.CPU.MaxUsedPercent,
			Cooldown:  time.Duration(rule.CPU.CooldownInterval) * time.Second,
			Level:     "warning",
			Unit:      "%",
		})
	}
	if rule.Load.Enabled && metrics.GetCpuCores() > 0 {
		loadPct := (metrics.GetLoad1() / float64(metrics.GetCpuCores())) * 100
		e.Eval(ctx, nodeKey+".load", loadPct, Rule{
			Threshold: rule.Load.MaxLoadPercent,
			Cooldown:  time.Duration(rule.Load.CooldownInterval) * time.Second,
			Level:     "warning",
			Unit:      "%",
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
	stKey := "storage." + report.GetStorageId()

	if total > 0 {
		e.evalCapacity(ctx, stKey, total, used, capacityAlertConfig{
			MaxUsedPercent:   st.MaxUsedPercent,
			MinFreeSpace:     st.MinFreeSpace,
			CooldownInterval: st.CooldownInterval,
			Level:            "error",
		})
	}
	return nil
}

// sanitizeKey 将挂载点路径转换为合法的告警状态 key（替换 / 和空格）。
func sanitizeKey(path string) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(strings.Trim(path, "/"))
}

// GetHistory 查询最新的告警历史记录（最多 limit 条）。
func (e *Evaluator) GetHistory(ctx context.Context, limit int) ([]model.AlertHistory, error) {
	if e.history == nil {
		return []model.AlertHistory{}, nil
	}
	return e.history.List(ctx, limit)
}

// formatMetricValue 格式化指标数值展示。当 unit 为 KiB 时自适应转换为 KiB/MiB/GiB/TiB 提升可读性。
func formatMetricValue(val float64, unit string) string {
	switch unit {
	case "%":
		return fmt.Sprintf("%.2f%%", val)
	case "KiB":
		switch {
		case val >= 1024*1024*1024:
			return fmt.Sprintf("%.2f TiB", val/(1024*1024*1024))
		case val >= 1024*1024:
			return fmt.Sprintf("%.2f GiB", val/(1024*1024))
		case val >= 1024:
			return fmt.Sprintf("%.2f MiB", val/1024)
		default:
			return fmt.Sprintf("%.2f KiB", val)
		}
	default:
		if unit != "" {
			return fmt.Sprintf("%.2f %s", val, unit)
		}
		return fmt.Sprintf("%.2f", val)
	}
}

// publishFire 推送告警触发/持续重推通知。
// Notify 必须用指针（omitempty 对 struct 值无效）。
func (e *Evaluator) publishFire(ctx context.Context, key string, metric float64, rule Rule) {
	level := cmp.Or(rule.Level, "warning")
	msgText := fmt.Sprintf("%s 当前值 %s，阈值 %s", key, formatMetricValue(metric, rule.Unit), formatMetricValue(rule.Threshold, rule.Unit))

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
	msgText := fmt.Sprintf("%s 已恢复（当前值 %s）", key, formatMetricValue(metric, rule.Unit))

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
