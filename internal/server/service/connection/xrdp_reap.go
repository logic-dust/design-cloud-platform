package connection

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

const xrdpReapKillCooldown = 90 * time.Second

func (s *Service) xrdpReapKillKey(nodeID string, display int) string {
	return fmt.Sprintf("%s:%d", nodeID, display)
}

func (s *Service) withinXRDPReapCooldown(nodeID string, display int) bool {
	if s == nil {
		return false
	}
	s.xrdpReapMu.Lock()
	defer s.xrdpReapMu.Unlock()
	if s.lastXRDPReapKillAt == nil {
		s.lastXRDPReapKillAt = make(map[string]time.Time)
	}
	key := s.xrdpReapKillKey(nodeID, display)
	last, ok := s.lastXRDPReapKillAt[key]
	return ok && time.Since(last) < xrdpReapKillCooldown
}

func (s *Service) markXRDPReapKill(nodeID string, display int) {
	if s == nil {
		return
	}
	s.xrdpReapMu.Lock()
	defer s.xrdpReapMu.Unlock()
	if s.lastXRDPReapKillAt == nil {
		s.lastXRDPReapKillAt = make(map[string]time.Time)
	}
	s.lastXRDPReapKillAt[s.xrdpReapKillKey(nodeID, display)] = time.Now()
}

// tryReapLogoutXRDPSeat kills a managed running seat when the agent reports
// session_ending_confirmed (X still up, logind session missing/closing).
func (s *Service) tryReapLogoutXRDPSeat(
	ctx context.Context,
	nodeID string,
	row model.Connection,
	info contract.XRDPSeatInfo,
) {
	if !info.SessionEndingConfirmed || row.Display == nil {
		return
	}
	if row.Status != model.ConnectionRunning {
		return
	}
	if s.withinXRDPReapCooldown(nodeID, *row.Display) {
		return
	}
	var pid uint
	if info.PID > 0 {
		pid = info.PID
	} else if row.PID != nil {
		pid = *row.PID
	}
	s.markXRDPReapKill(nodeID, *row.Display)
	if err := s.dispatchKillXRDP(ctx, nodeID, row.Username, row.ID, *row.Display, pid); err != nil {
		if s.logger != nil {
			s.logger.Warn("logout XRDP reap kill failed; marking terminating",
				"connectionID", row.ID, "nodeID", nodeID, "display", *row.Display, "error", err)
		}
		_ = s.connectionRepo.MarkTerminatingKeepResource(ctx, row.ID)
		return
	}
	port := cmp.Or(row.Port, 3389)
	if err := s.connectionRepo.MarkStoppedClearDisplay(ctx, row.ID, port); err != nil && s.logger != nil {
		s.logger.Warn("logout XRDP reap mark stopped failed",
			"connectionID", row.ID, "error", err)
	}
}
