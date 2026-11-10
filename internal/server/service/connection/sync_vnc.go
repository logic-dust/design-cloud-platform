package connection

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// vncProbeGrace is how long a freshly started VNC stays running when the agent
// report has not yet seen the process. controlled_at is on the shared row, so
// the control leader and the server that handled Start see the same window.
const vncProbeGrace = 15 * time.Second

func withinVNCProbeGrace(controlledAt *time.Time, now time.Time) bool {
	return controlledAt != nil && now.Sub(*controlledAt) < vncProbeGrace
}

func uintPtrEqual(a, b *uint) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// SyncConnectionReport reconciles agent-reported seats/sessions for a node.
func (s *Service) SyncConnectionReport(ctx context.Context, nodeID string, report contract.ConnectionReport) error {
	if nodeID == "" {
		return fmt.Errorf("node ID is required")
	}
	if err := s.reconcileVNCSeats(ctx, nodeID, report.VNCSeats); err != nil {
		return err
	}
	if err := s.reconcileXRDPSeats(ctx, nodeID, report.XRDPSeats); err != nil {
		return err
	}
	return s.reconcileRDPSessions(ctx, nodeID, report.RDPSessions)
}

func (s *Service) reconcileVNCSeats(ctx context.Context, nodeID string, reportedConns []contract.VNCSeatInfo) error {
	connections, err := s.connectionRepo.ListVNCByNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("list connections for node %s: %w", nodeID, err)
	}

	ownersToRefresh := make(map[string]struct{})
	active := make([]model.Connection, 0, len(connections))
	for _, c := range connections {
		if c.Status == model.ConnectionTerminating {
			s.reconcileTerminatingVNC(ctx, nodeID, c, reportedConns, ownersToRefresh)
			continue
		}
		active = append(active, c)
	}

	matchCache := newVNCMatchCache()

	byID := make(map[uint]model.Connection)
	byUserPort := make(map[string]model.Connection)
	byUserDisplay := make(map[string]model.Connection)
	for _, c := range active {
		byID[c.ID] = c
		if c.Username != "" && c.Port > 0 {
			byUserPort[fmt.Sprintf("%s:%d", c.Username, c.Port)] = c
		}
		if c.Username != "" && c.Display != nil {
			byUserDisplay[fmt.Sprintf("%s:%d", c.Username, *c.Display)] = c
		}
	}

	matchedConnIDs := make(map[uint]bool)

	for _, info := range reportedConns {
		if strings.HasPrefix(info.Name, "dce-app") {
			continue
		}
		if info.Username == "" && info.Port == 0 && info.PID == 0 {
			continue
		}

		dispNum := info.Display
		var portalUser string
		var hasPortalUser bool
		if info.Username != "" {
			portalUser, hasPortalUser = s.portalUsername(ctx, info.Username)
		}
		lookupUser := cmp.Or(portalUser, info.Username)

		var matched *model.Connection
		if info.ConnectionID > 0 {
			if c, ok := byID[info.ConnectionID]; ok && !matchedConnIDs[c.ID] {
				matched = &c
			}
		}
		if matched == nil && lookupUser != "" && info.Port > 0 {
			if c, ok := byUserPort[fmt.Sprintf("%s:%d", lookupUser, info.Port)]; ok && !matchedConnIDs[c.ID] {
				matched = &c
			}
		}
		if matched == nil && lookupUser != "" && dispNum != nil {
			if c, ok := byUserDisplay[fmt.Sprintf("%s:%d", lookupUser, *dispNum)]; ok && !matchedConnIDs[c.ID] {
				matched = &c
			}
		}

		var pidPtr *uint
		if info.PID > 0 {
			pidPtr = &info.PID
		}

		if matched != nil {
			matchedConnIDs[matched.ID] = true
			fields := make(map[string]any)
			if matched.Status != model.ConnectionRunning {
				fields["status"] = model.ConnectionRunning
			}
			if !uintPtrEqual(matched.PID, pidPtr) {
				fields["pid"] = pidPtr
			}
			if info.Port > 0 && matched.Port != info.Port {
				fields["port"] = info.Port
			}
			if dispNum != nil && !intPtrEqual(matched.Display, dispNum) {
				fields["display"] = dispNum
			}
			if matched.ResourceID == nil && matched.Status != model.ConnectionPending {
				effectiveDisplay := dispNum
				if effectiveDisplay == nil {
					effectiveDisplay = matched.Display
				}
				if effectiveDisplay != nil {
					bestRes, tied, ok := s.matchVNCResource(ctx, matched.Username, nodeID, effectiveDisplay, matchCache)
					if ok {
						fields["resource_id"] = bestRes.ID
						fields["name"] = model.ConnectionName(bestRes.Name, nodeID)
					} else if len(tied) > 1 {
						s.warnTiedCandidateResources(nodeID, matched.Username, effectiveDisplay, tied)
					}
				}
			}

			if len(fields) > 0 {
				if err := s.connectionRepo.UpdateFields(ctx, matched.ID, fields); err == nil {
					ownersToRefresh[matched.Username] = struct{}{}
				}
			}
			continue
		}

		if info.Port == 0 || !hasPortalUser {
			continue
		}
		if dispNum != nil {
			if has, _ := s.connectionRepo.HasDisplayOnNode(ctx, nodeID, *dispNum, 0); has {
				continue
			}
		}

		now := time.Now()
		connName := model.ProbedConnectionName(info.Name, nodeID, info.Port)
		var boundResourceID *uint
		if dispNum != nil {
			bestRes, tied, ok := s.matchVNCResource(ctx, portalUser, nodeID, dispNum, matchCache)
			if ok {
				boundResourceID = &bestRes.ID
				connName = model.ConnectionName(bestRes.Name, nodeID)
			} else if len(tied) > 1 {
				s.warnTiedCandidateResources(nodeID, portalUser, dispNum, tied)
			}
		}

		createdAt := now
		if info.StartedAt > 0 {
			createdAt = time.Unix(info.StartedAt, 0)
		}

		newConn := &model.Connection{
			ResourceID:     boundResourceID,
			Name:           connName,
			Username:       portalUser,
			Protocol:       contract.VNCProtocol,
			NodeID:         nodeID,
			Port:           info.Port,
			Display:        dispNum,
			PID:            pidPtr,
			Status:         model.ConnectionRunning,
			LastAccessedAt: &now,
			CreatedAt:      createdAt,
		}
		if err := s.connectionRepo.Create(ctx, newConn); err != nil {
			s.logger.Warn("failed to adopt probed VNC connection",
				"node_id", nodeID, "username", portalUser, "port", info.Port, "name", connName, "error", err)
			continue
		}
		matchedConnIDs[newConn.ID] = true
		ownersToRefresh[portalUser] = struct{}{}
	}

	for _, c := range active {
		if matchedConnIDs[c.ID] {
			continue
		}

		fields := make(map[string]any)

		if c.Status == model.ConnectionRunning {
			if !withinVNCProbeGrace(c.ControlledAt, time.Now()) {
				placeholderPort, err := s.connectionRepo.PickPlaceholderPort(ctx, c.NodeID, c.Username)
				if err != nil {
					if s.logger != nil {
						s.logger.Warn("pick placeholder port after probe stopped", "connectionID", c.ID, "error", err)
					}
				} else if err := s.connectionRepo.MarkStoppedClearDisplay(ctx, c.ID, placeholderPort); err == nil {
					ownersToRefresh[c.Username] = struct{}{}
				}
				continue
			}
		}

		if c.ResourceID == nil && c.Status != model.ConnectionPending && c.Display != nil {
			bestRes, tied, ok := s.matchVNCResource(ctx, c.Username, nodeID, c.Display, matchCache)
			if ok {
				fields["resource_id"] = bestRes.ID
				fields["name"] = model.ConnectionName(bestRes.Name, nodeID)
			} else if len(tied) > 1 {
				s.warnTiedCandidateResources(nodeID, c.Username, c.Display, tied)
			}
		}

		if len(fields) > 0 {
			if err := s.connectionRepo.UpdateFields(ctx, c.ID, fields); err == nil {
				ownersToRefresh[c.Username] = struct{}{}
			}
		}
	}

	for owner := range ownersToRefresh {
		s.notifyOwnerRefresh(ctx, "", owner, 0)
	}

	return nil
}

// reconcileTerminatingVNC finishes delete tombstones using owner+display against the probe report.
func (s *Service) reconcileTerminatingVNC(
	ctx context.Context,
	nodeID string,
	c model.Connection,
	reported []contract.VNCSeatInfo,
	ownersToRefresh map[string]struct{},
) {
	ownersToRefresh[c.Username] = struct{}{}

	if c.Display == nil {
		_ = s.connectionRepo.DeleteByID(ctx, c.ID)
		return
	}

	var hit *contract.VNCSeatInfo
	for i := range reported {
		info := &reported[i]
		if info.Display != nil && *info.Display == *c.Display {
			hit = info
			break
		}
	}

	if hit == nil {
		// Process gone — drop tombstone.
		_ = s.connectionRepo.DeleteByID(ctx, c.ID)
		return
	}

	reportUser := hit.Username
	if portal, ok := s.portalUsername(ctx, hit.Username); ok {
		reportUser = portal
	}

	if reportUser == c.Username {
		if err := s.finishTerminatingKill(ctx, c); err != nil && s.logger != nil {
			s.logger.Warn("terminating tombstone kill still failing",
				"connectionID", c.ID, "nodeID", nodeID, "display", *c.Display, "error", err)
		}
		return
	}

	// Display reused by another owner — previous kill effectively released it.
	_ = s.connectionRepo.DeleteByID(ctx, c.ID)
}
