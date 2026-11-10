package connection

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

const defaultAdminCollectCooldown = 2 * time.Minute

// --- 管理采集触发 ---

// TriggerCollect triggers a desktop runtime information collection via the cluster collect facade.
// Respects a cooldown window to prevent redundant triggers.
func (s *Service) TriggerCollect(ctx context.Context, target contract.CollectTarget) error {
	if s.collectTrigger == nil {
		return fmt.Errorf("collect trigger is not configured")
	}
	if target == contract.CollectTargetConnection {
		if !s.allowAdminCollect() {
			s.logger.Debug("Skipping duplicate connection collect trigger within cooldown window")
			return nil
		}
	}
	return s.collectTrigger.TriggerCollect(ctx, target)
}

func (s *Service) allowAdminCollect() bool {
	now := time.Now()
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

// --- 连接对账与清理 ---

// StoppedConnectionRetentionDays is how long stopped rows are kept before physical delete.
const StoppedConnectionRetentionDays = 90

// CleanupRunResult holds the result of a connection cleanup execution.
type CleanupRunResult struct {
	Updated int64 `json:"updated"`
	Pruned  int64 `json:"pruned"`
}

// ExecuteCleanup 对 online 节点上的 running VNC 做 CONNECTION_CHECK，重试 terminating
// 墓碑 teardown（含无探针的 RDP/SSH），并 prune 过期 stopped 行。
// 节点 offline/maintenance 不再写 connection.status=unknown（展示态由 List/Get 读时派生）。
func (s *Service) ExecuteCleanup(ctx context.Context) (CleanupRunResult, error) {
	if s == nil || s.leaseRepo == nil {
		return CleanupRunResult{}, ErrCleanupServiceUnavailable
	}

	acquired, _, err := s.leaseRepo.TryAcquire(ctx, "connection_cleanup", s.ownerID, time.Minute)
	if err != nil {
		return CleanupRunResult{}, fmt.Errorf("acquire cleanup lease: %w", err)
	}
	if !acquired {
		return CleanupRunResult{}, ErrCleanupLeaseHeld
	}

	runningConns, err := s.connectionRepo.ListRunningVNC(ctx)
	if err != nil {
		return CleanupRunResult{}, fmt.Errorf("list running connections: %w", err)
	}

	var updated int64
	vncByNodeUser := make(map[nodeUserKey][]model.Connection)
	for _, c := range runningConns {
		if s.nodeRepo != nil {
			node, err := s.nodeRepo.GetByNodeID(ctx, c.NodeID)
			if err != nil || node.Status != model.NodeStatusOnline {
				continue
			}
		}
		key := nodeUserKey{nodeID: c.NodeID, username: c.Username}
		vncByNodeUser[key] = append(vncByNodeUser[key], c)
	}

	for key, conns := range vncByNodeUser {
		if s.execSvc == nil {
			continue
		}
		n, err := s.checkManagedSeatsOnNode(ctx, key.nodeID, conns)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("cleanup probe failed; leaving running VNC unchanged", "nodeId", key.nodeID, "username", key.username, "error", err)
			}
			continue
		}
		updated += int64(n)
	}

	updated += s.retryTerminatingTombstones(ctx)

	pruned, err := s.pruneOldStoppedConnections(ctx)
	if err != nil && s.logger != nil {
		s.logger.Warn("connection cleanup: prune old stopped rows failed", "error", err)
	} else if pruned > 0 && s.logger != nil {
		s.logger.Info("connection cleanup: pruned old stopped rows", "count", pruned)
	}

	return CleanupRunResult{Updated: updated, Pruned: pruned}, nil
}

// retryTerminatingTombstones re-runs shared teardown for every terminating row.
// Covers RDP/SSH (no seat probe) and VNC/XRDP when probe has not finished them yet.
func (s *Service) retryTerminatingTombstones(ctx context.Context) int64 {
	if s.connectionRepo == nil {
		return 0
	}
	rows, err := s.connectionRepo.ListTerminating(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("connection cleanup: list terminating failed", "error", err)
		}
		return 0
	}
	var n int64
	for _, c := range rows {
		if err := s.finishTerminatingKill(ctx, c); err != nil {
			if s.logger != nil {
				s.logger.Warn("connection cleanup: terminating teardown still failing",
					"connectionID", c.ID, "protocol", c.Protocol.String(), "nodeID", c.NodeID, "error", err)
			}
			continue
		}
		n++
	}
	return n
}
