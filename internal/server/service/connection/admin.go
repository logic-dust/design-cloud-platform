package connection

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
)

const defaultAdminCollectCooldown = 2 * time.Minute

// --- 管理采集触发 ---

// TriggerCollect triggers a VNC information collection via the cluster collect facade.
// Respects a cooldown window to prevent redundant triggers.
func (s *Service) TriggerCollect(ctx context.Context, target contract.CollectTarget) error {
	if s.collectTrigger == nil {
		return fmt.Errorf("collect trigger is not configured")
	}
	if target == contract.CollectTargetVNC {
		if !s.allowAdminCollect() {
			s.logger.Debug("Skipping duplicate VNC collect trigger within cooldown window")
			return nil
		}
	}
	return s.collectTrigger.TriggerCollect(ctx, target)
}

func (s *Service) allowAdminCollect() bool {
	now := s.clockNow()
	window := s.adminCollectWindow()

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	if !s.lastAdminCollectAt.IsZero() && now.Sub(s.lastAdminCollectAt) < window {
		return false
	}
	s.lastAdminCollectAt = now
	return true
}

func (s *Service) adminCollectWindow() time.Duration {
	if s.adminCollectCooldown > 0 {
		return s.adminCollectCooldown
	}
	return defaultAdminCollectCooldown
}

// --- 连接清理 ---

// CleanupRunResult holds the result of a connection cleanup execution.
type CleanupRunResult struct {
	Deleted int64 `json:"deleted"`
}

// ExecuteCleanup executes on-demand cleanup for stale managed connection records.
// It is invoked by the admin API, not by a background loop.
func (s *Service) ExecuteCleanup(ctx context.Context) (CleanupRunResult, error) {
	if s == nil || s.leaseRepo == nil {
		return CleanupRunResult{}, ErrCleanupServiceUnavailable
	}

	acquired, err := s.leaseRepo.TryAcquire(ctx, "connection_cleanup", s.ownerID, time.Minute)
	if err != nil {
		return CleanupRunResult{}, fmt.Errorf("acquire cleanup lease: %w", err)
	}
	if !acquired {
		return CleanupRunResult{}, ErrCleanupLeaseHeld
	}

	deleted, err := s.connectionRepo.DeleteStaleManaged(ctx, s.clockNow())
	if err != nil {
		return CleanupRunResult{}, fmt.Errorf("delete stale managed connections: %w", err)
	}
	return CleanupRunResult{Deleted: deleted}, nil
}
