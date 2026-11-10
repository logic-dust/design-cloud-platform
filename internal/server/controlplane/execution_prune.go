package controlplane

import (
	"context"
	"runtime/debug"
	"time"

	"design-cloud-engine/internal/platform/config"
)

const (
	// executionPruneLeaseName 多节点部署下保证同一时刻只有一个节点执行清理。
	executionPruneLeaseName = "execution_records_prune"
	// executionPruneInterval 清理周期。
	executionPruneInterval = time.Hour
	// executionStaleAfter 非终态记录视为进程已死的滞留时长。执行超时没有上限，
	// 窗口必须长于正常命令；超过此时长仍为 pending/dispatched 则标为 timedOut，不删除。
	executionStaleAfter = 24 * time.Hour
	// executionPruneBatchSize 单批删除条数；分批避免长事务与锁尖峰。
	executionPruneBatchSize = 500
	// executionPruneTimeout 单次清理的总耗时上限。
	executionPruneTimeout = 2 * time.Minute
)

// executionRetention 返回终态执行记录的保留时长，来自 cluster.execution_retention_days
// 配置（0 或负值用 config.DefaultExecutionRetentionDays）。
func (s *Server) executionRetention() time.Duration {
	days := s.clusterCfg.ExecutionRetentionDays
	if days <= 0 {
		days = config.DefaultExecutionRetentionDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// StartExecutionPruneLoop 启动执行记录过期清理。同步派发同样会落执行记录（幂等需要），
// 记录随调用量线性增长，必须按保留期回收终态记录，否则表会无界膨胀。
func (s *Server) StartExecutionPruneLoop() {
	if s == nil || s.leases == nil || s.executions == nil {
		return
	}
	s.registry.StartWorker(s.executionPruneLoop)
}

func (s *Server) executionPruneLoop(done <-chan struct{}) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("panic recovered in execution prune loop",
				"panic", rec,
				"stack", string(debug.Stack()),
			)
		}
	}()

	retention := s.executionRetention()
	s.logger.Info("Starting execution record prune loop",
		"interval", executionPruneInterval,
		"retention", retention,
		"stale_after", executionStaleAfter)

	ticker := time.NewTicker(executionPruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.pruneExecutionRecords()
		case <-done:
			return
		}
	}
}

func (s *Server) pruneExecutionRecords() {
	ctx, cancel := context.WithTimeout(s.ctx, executionPruneTimeout)
	defer cancel()

	acquired, _, err := s.leases.TryAcquire(ctx, executionPruneLeaseName, s.nodeID, executionPruneInterval)
	if err != nil {
		s.logger.Warn("Execution prune: failed to acquire lease", "error", err)
		return
	}
	if !acquired {
		s.logger.Debug("Execution prune: skipped (lease held elsewhere)")
		return
	}

	before := time.Now().Add(-s.executionRetention())
	var total int64
	for {
		deleted, err := s.executions.PruneTerminal(ctx, before, executionPruneBatchSize)
		if err != nil {
			s.logger.Warn("Execution prune: delete batch failed", "error", err, "deleted", total)
			return
		}
		total += deleted
		if deleted < executionPruneBatchSize {
			break
		}
	}
	if total > 0 {
		s.logger.Info("Pruned expired execution records", "count", total, "retention", s.executionRetention())
	}

	s.pruneStorageReports(ctx)

	staleBefore := time.Now().Add(-executionStaleAfter)
	var expired int64
	for {
		n, err := s.executions.ExpireStale(ctx, staleBefore, executionPruneBatchSize)
		if err != nil {
			s.logger.Warn("Execution prune: expire stale batch failed", "error", err, "expired", expired)
			return
		}
		expired += n
		if n < executionPruneBatchSize {
			break
		}
	}
	if expired > 0 {
		s.logger.Info("Expired stale non-terminal execution records", "count", expired, "stale_after", executionStaleAfter)
	}
}
