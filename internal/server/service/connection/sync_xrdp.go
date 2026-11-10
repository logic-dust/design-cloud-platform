package connection

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// reconcileXRDPSeats updates XRDP dial rows with live X seats from the probe.
//
// Semantics:
//   - Seat appear → write display/pid, keep/ensure running.
//   - Live seat (had pid) disappear → stopped tombstone (clear display/pid, keep
//     resource_id; node kept as last placement until Start re-selects).
//   - Spec-reserved display without pid is not treated as logout.
//   - Disconnect keepalive still has a live X seat, so it stays running.
//   - Terminating tombstones use owner+display like VNC.
func (s *Service) reconcileXRDPSeats(ctx context.Context, nodeID string, reported []contract.XRDPSeatInfo) error {
	connections, err := s.connectionRepo.ListXRDPByNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("list XRDP connections for node %s: %w", nodeID, err)
	}

	ownersToRefresh := make(map[string]struct{})
	active := make([]model.Connection, 0, len(connections))
	for _, c := range connections {
		if c.Status == model.ConnectionTerminating {
			s.reconcileTerminatingXRDP(ctx, nodeID, c, reported, ownersToRefresh)
			continue
		}
		active = append(active, c)
	}

	byID := make(map[uint]model.Connection, len(active))
	byUser := make(map[string]model.Connection, len(active))
	byUserDisplay := make(map[string]model.Connection, len(active))
	for _, c := range active {
		byID[c.ID] = c
		if c.Username != "" {
			byUser[c.Username] = c
		}
		if c.Username != "" && c.Display != nil {
			byUserDisplay[fmt.Sprintf("%s:%d", c.Username, *c.Display)] = c
		}
	}

	matched := make(map[uint]bool)
	matchCache := newVNCMatchCache()

	for _, info := range reported {
		if info.Username == "" && info.PID == 0 {
			continue
		}
		portalUser := info.Username
		if mapped, ok := s.portalUsername(ctx, info.Username); ok {
			portalUser = mapped
		}

		var row *model.Connection
		if info.ConnectionID > 0 {
			if c, ok := byID[info.ConnectionID]; ok && !matched[c.ID] {
				row = &c
			}
		}
		if row == nil && portalUser != "" && info.Display != nil {
			if c, ok := byUserDisplay[fmt.Sprintf("%s:%d", portalUser, *info.Display)]; ok && !matched[c.ID] {
				row = &c
			}
		}
		if row == nil && portalUser != "" {
			if c, ok := byUser[portalUser]; ok && !matched[c.ID] {
				row = &c
			}
		}
		if row == nil {
			if portalUser == "" || info.Display == nil || info.SessionEndingConfirmed {
				continue
			}
			// idx_conn_node_user_port allows at most one XRDP row (port 3389) per (node, user).
			if _, exists := byUser[portalUser]; exists {
				continue
			}
			if has, _ := s.connectionRepo.HasDisplayOnNode(ctx, nodeID, *info.Display, 0); has {
				continue
			}
			now := time.Now()
			connName := model.ProbedConnectionName("xrdp", nodeID, uint(*info.Display))
			var boundResourceID *uint
			bestRes, tied, ok := s.matchResource(ctx, portalUser, nodeID, contract.XRDPProtocol, info.Display, matchCache)
			if ok {
				boundResourceID = &bestRes.ID
				connName = model.ConnectionName(bestRes.Name, nodeID)
			} else if len(tied) > 1 {
				s.warnTiedCandidateResources(nodeID, portalUser, info.Display, tied)
			}
			var pidPtr *uint
			if info.PID > 0 {
				pidPtr = &info.PID
			}
			createdAt := now
			if info.StartedAt > 0 {
				createdAt = time.Unix(info.StartedAt, 0)
			}
			newConn := &model.Connection{
				ResourceID:     boundResourceID,
				Name:           connName,
				Username:       portalUser,
				Protocol:       contract.XRDPProtocol,
				NodeID:         nodeID,
				Port:           3389,
				Display:        info.Display,
				PID:            pidPtr,
				Status:         model.ConnectionRunning,
				LastAccessedAt: &now,
				CreatedAt:      createdAt,
			}
			if err := s.connectionRepo.Create(ctx, newConn); err != nil {
				if s.logger != nil {
					s.logger.Warn("failed to adopt probed XRDP connection",
						"nodeID", nodeID, "username", portalUser, "display", *info.Display, "error", err)
				}
				continue
			}
			byUser[portalUser] = *newConn
			matched[newConn.ID] = true
			ownersToRefresh[portalUser] = struct{}{}
			continue
		}
		if row.Status == model.ConnectionStopped {
			if info.PID > 0 && !info.SessionEndingConfirmed {
				matched[row.ID] = true
				pidPtr := &info.PID
				fields := map[string]any{
					"status": model.ConnectionRunning,
					"pid":    pidPtr,
				}
				if info.Display != nil {
					fields["display"] = info.Display
				}
				if row.ResourceID == nil {
					effectiveDisplay := info.Display
					if effectiveDisplay == nil {
						effectiveDisplay = row.Display
					}
					if effectiveDisplay != nil {
						bestRes, tied, ok := s.matchResource(ctx, row.Username, nodeID, contract.XRDPProtocol, effectiveDisplay, matchCache)
						if ok {
							fields["resource_id"] = bestRes.ID
							fields["name"] = model.ConnectionName(bestRes.Name, nodeID)
						} else if len(tied) > 1 {
							s.warnTiedCandidateResources(nodeID, row.Username, effectiveDisplay, tied)
						}
					}
				}
				if err := s.connectionRepo.UpdateFields(ctx, row.ID, fields); err == nil {
					ownersToRefresh[row.Username] = struct{}{}
				}
			}
			continue
		}
		if row.Status == model.ConnectionPending {
			matched[row.ID] = true
			continue
		}

		matched[row.ID] = true
		var pidPtr *uint
		if info.PID > 0 {
			pidPtr = &info.PID
		}
		fields := make(map[string]any)
		if row.Status != model.ConnectionRunning {
			fields["status"] = model.ConnectionRunning
		}
		if !uintPtrEqual(row.PID, pidPtr) {
			fields["pid"] = pidPtr
		}
		if info.Display != nil && !intPtrEqual(row.Display, info.Display) {
			fields["display"] = info.Display
		}
		if row.ResourceID == nil {
			effectiveDisplay := info.Display
			if effectiveDisplay == nil {
				effectiveDisplay = row.Display
			}
			if effectiveDisplay != nil {
				bestRes, tied, ok := s.matchResource(ctx, row.Username, nodeID, contract.XRDPProtocol, effectiveDisplay, matchCache)
				if ok {
					fields["resource_id"] = bestRes.ID
					fields["name"] = model.ConnectionName(bestRes.Name, nodeID)
				} else if len(tied) > 1 {
					s.warnTiedCandidateResources(nodeID, row.Username, effectiveDisplay, tied)
				}
			}
		}
		if len(fields) > 0 {
			if err := s.connectionRepo.UpdateFields(ctx, row.ID, fields); err == nil {
				ownersToRefresh[row.Username] = struct{}{}
			}
		}
		s.tryReapLogoutXRDPSeat(ctx, nodeID, *row, info)
	}

	for _, c := range active {
		if matched[c.ID] {
			continue
		}
		if c.Status != model.ConnectionRunning {
			continue
		}
		if c.PID == nil {
			// No live seat attached yet (dial registration / fixed display reserve).
			continue
		}
		if withinVNCProbeGrace(c.ControlledAt, time.Now()) {
			continue
		}
		// Live seat gone → stopped tombstone (quota + UI card via resource_id).
		port := cmp.Or(c.Port, 3389)
		if err := s.connectionRepo.MarkStoppedClearDisplay(ctx, c.ID, port); err == nil {
			ownersToRefresh[c.Username] = struct{}{}
		}
	}

	for owner := range ownersToRefresh {
		s.notifyOwnerRefresh(ctx, "", owner, 0)
	}
	return nil
}

func (s *Service) reconcileTerminatingXRDP(
	ctx context.Context,
	nodeID string,
	c model.Connection,
	reported []contract.XRDPSeatInfo,
	ownersToRefresh map[string]struct{},
) {
	ownersToRefresh[c.Username] = struct{}{}

	if c.Display == nil {
		_ = s.connectionRepo.DeleteByID(ctx, c.ID)
		return
	}

	var hit *contract.XRDPSeatInfo
	for i := range reported {
		info := &reported[i]
		if info.Display != nil && *info.Display == *c.Display {
			hit = info
			break
		}
	}
	if hit == nil {
		if err := s.finishTerminatingAfterSeatGone(ctx, c); err != nil && s.logger != nil {
			s.logger.Warn("finish terminating after seat gone", "connectionID", c.ID, "error", err)
		}
		return
	}

	reportUser := hit.Username
	if portal, ok := s.portalUsername(ctx, hit.Username); ok {
		reportUser = portal
	}
	if reportUser == c.Username {
		if err := s.finishTerminatingKill(ctx, c); err != nil && s.logger != nil {
			s.logger.Warn("terminating XRDP tombstone kill still failing",
				"connectionID", c.ID, "nodeID", nodeID, "display", *c.Display, "error", err)
		}
		return
	}
	_ = s.connectionRepo.DeleteByID(ctx, c.ID)
}
