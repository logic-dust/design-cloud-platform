package session

import (
	"context"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
)

// SetProxyPruner registers an optional callback to prune expired pending proxy sessions.
func (s *Service) SetProxyPruner(pruner func(time.Time) int) {
	if pruner != nil {
		s.proxyPruner = pruner
	}
}

// StartCleanupLoop 启动定时清理过期 session 的后台任务，清理间隔从 auth 设置读取。
// 接收 ctx 以便在服务器关闭时优雅地停止计时器并退出协程。
// 使用 DB task lease 确保多 Server 环境下只有一个节点执行清理。
// 每次 tick 都会重新读取间隔设置，修改 settings 后下个周期生效。
func (s *Service) StartCleanupLoop(ctx context.Context, leaseRepo *db.TaskLeaseRepo, ownerID string) {
	if s == nil || leaseRepo == nil {
		return
	}
	s.logger.Info("Session cleanup scheduler started (dynamic interval mode)")

	common.SafeGo(s.logger, "session-cleanup-worker", func() {
		// Run one cleanup pass immediately on startup with the current interval.
		interval := s.resolveInterval(ctx)
		func() {
			defer common.SafeRecover(s.logger, "session-cleanup-tick")()
			s.tryCleanup(ctx, leaseRepo, ownerID, interval)
		}()

		for {
			interval := s.resolveInterval(ctx)

			timer := time.NewTimer(interval)
			select {
			case <-timer.C:
				func() {
					defer common.SafeRecover(s.logger, "session-cleanup-tick")()
					s.tryCleanup(ctx, leaseRepo, ownerID, interval)
				}()
			case <-ctx.Done():
				timer.Stop()
				s.logger.Info("Session cleanup task stopped")
				return
			}
		}
	})
}

func (s *Service) resolveInterval(ctx context.Context) time.Duration {
	const defaultInterval = 10 * time.Minute

	if s.settingsSvc == nil {
		return defaultInterval
	}
	auth, err := s.settingsSvc.GetAuthSettings(ctx)
	if err != nil {
		s.logger.Warn("Session cleanup: failed to read auth settings, using default", "default", defaultInterval, "error", err)
		return defaultInterval
	}

	sec := auth.SessionCleanupInterval
	if sec <= 0 {
		return defaultInterval
	}
	// Clamp within schema bounds
	sec = min(max(sec, 60), 86400)
	return time.Duration(sec) * time.Second
}

func (s *Service) tryCleanup(ctx context.Context, leaseRepo *db.TaskLeaseRepo, ownerID string, leaseInterval time.Duration) {
	// Local in-memory evictions run on every node, regardless of the DB lease.
	if s.cache != nil {
		if deleted := s.cache.evictExpired(time.Now()); deleted > 0 {
			s.logger.Debug("Expired session cache entries evicted", "count", deleted)
		}
	}
	if s.proxyPruner != nil {
		if pruned := s.proxyPruner(time.Now()); pruned > 0 {
			s.logger.Debug("Expired proxy pending sessions pruned", "count", pruned)
		}
	}

	acquired, err := leaseRepo.TryAcquire(ctx, "session_cleanup", ownerID, leaseInterval)
	if err != nil {
		s.logger.Warn("Session cleanup: failed to acquire lease", "error", err)
		return
	}
	if !acquired {
		s.logger.Debug("Session cleanup: skipped (lease held elsewhere)")
		return
	}

	if err := s.sessionRepo.DeleteExpired(ctx); err != nil {
		s.logger.Error("Failed to cleanup expired sessions", "error", err)
	} else {
		s.logger.Info("Expired sessions cleaned up")
	}
}
