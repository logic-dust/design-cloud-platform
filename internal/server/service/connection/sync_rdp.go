package connection

import (
	"context"
	"fmt"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// reconcileRDPSessions aligns Windows desktop RDP portal rows with OS login sessions from the probe.
// - running + no matching sessions for that user → stopped tombstone (resource kept)
// - terminating + sessions gone / teardown ok → finish tombstone
// - stopped + matching sessions appear → resume running
// - RDP desktop sessions without row → adopt unmanaged connection
func (s *Service) reconcileRDPSessions(
	ctx context.Context,
	nodeID string,
	rdp []contract.RDPSessionInfo,
) error {
	rows, err := s.connectionRepo.ListRDPByNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("list RDP connections for node %s: %w", nodeID, err)
	}
	rdpCount := countSessionsByUser(rdpUsernames(rdp))

	rdpUserHasRow := make(map[string]bool)
	for _, c := range rows {
		rdpUserHasRow[strings.ToLower(c.Username)] = true
	}

	owners := make(map[string]struct{})
	matchCache := newVNCMatchCache()
	for _, c := range rows {
		live := s.rdpLiveCount(ctx, c.Username, rdpCount)

		switch c.Status {
		case model.ConnectionTerminating:
			if live == 0 {
				if err := s.finishTerminatingAfterSeatGone(ctx, c); err != nil && s.logger != nil {
					s.logger.Warn("finish RDP terminating after sessions gone", "connectionID", c.ID, "error", err)
				}
			} else if err := s.finishTerminatingKill(ctx, c); err != nil && s.logger != nil {
				s.logger.Warn("terminating RDP teardown still failing",
					"connectionID", c.ID, "protocol", c.Protocol.String(), "error", err)
			}
			owners[c.Username] = struct{}{}
		case model.ConnectionRunning:
			if live == 0 {
				if err := s.markStoppedTombstone(ctx, c); err != nil {
					if s.logger != nil {
						s.logger.Warn("mark RDP stopped after sessions gone", "connectionID", c.ID, "error", err)
					}
					continue
				}
				owners[c.Username] = struct{}{}
				continue
			}
			if c.ResourceID == nil {
				bestRes, tied, ok := s.matchResource(ctx, c.Username, nodeID, contract.RDPProtocol, nil, matchCache)
				if ok {
					if err := s.connectionRepo.UpdateFields(ctx, c.ID, map[string]any{
						"resource_id": bestRes.ID,
						"name":        model.ConnectionName(bestRes.Name, nodeID),
					}); err == nil {
						owners[c.Username] = struct{}{}
					}
				} else if len(tied) > 1 {
					s.warnTiedCandidateResources(nodeID, c.Username, nil, tied)
				}
			}
		case model.ConnectionStopped:
			fields := make(map[string]any)
			if live > 0 {
				fields["status"] = model.ConnectionRunning
			}
			if c.ResourceID == nil {
				bestRes, tied, ok := s.matchResource(ctx, c.Username, nodeID, contract.RDPProtocol, nil, matchCache)
				if ok {
					fields["resource_id"] = bestRes.ID
					fields["name"] = model.ConnectionName(bestRes.Name, nodeID)
				} else if len(tied) > 1 {
					s.warnTiedCandidateResources(nodeID, c.Username, nil, tied)
				}
			}
			if len(fields) > 0 {
				if err := s.connectionRepo.UpdateFields(ctx, c.ID, fields); err == nil {
					owners[c.Username] = struct{}{}
				}
			}
		}
	}

	for osUser, count := range rdpCount {
		if count <= 0 {
			continue
		}
		portalUser := osUser
		if mapped, ok := s.portalUsername(ctx, osUser); ok {
			portalUser = mapped
		}
		if rdpUserHasRow[strings.ToLower(portalUser)] {
			continue
		}
		now := time.Now()
		connName := model.ProbedConnectionName("rdp", nodeID, 3389)
		var boundResourceID *uint
		bestRes, tied, ok := s.matchResource(ctx, portalUser, nodeID, contract.RDPProtocol, nil, matchCache)
		if ok {
			boundResourceID = &bestRes.ID
			connName = model.ConnectionName(bestRes.Name, nodeID)
		} else if len(tied) > 1 {
			s.warnTiedCandidateResources(nodeID, portalUser, nil, tied)
		}
		newConn := &model.Connection{
			ResourceID:     boundResourceID,
			Name:           connName,
			Username:       portalUser,
			Protocol:       contract.RDPProtocol,
			NodeID:         nodeID,
			Port:           3389,
			Status:         model.ConnectionRunning,
			LastAccessedAt: &now,
		}
		if err := s.connectionRepo.Create(ctx, newConn); err != nil {
			if s.logger != nil {
				s.logger.Warn("failed to adopt probed RDP connection",
					"nodeID", nodeID, "username", portalUser, "error", err)
			}
			continue
		}
		rdpUserHasRow[strings.ToLower(portalUser)] = true
		owners[portalUser] = struct{}{}
	}

	for u := range owners {
		s.notifyOwnerRefresh(ctx, "", u, 0)
	}
	return nil
}

func (s *Service) rdpLiveCount(ctx context.Context, portalUser string, byOSUser map[string]int) int {
	n := 0
	for osUser, c := range byOSUser {
		if strings.EqualFold(osUser, portalUser) {
			n += c
			continue
		}
		if mapped, ok := s.portalUsername(ctx, osUser); ok && strings.EqualFold(mapped, portalUser) {
			n += c
		}
	}
	return n
}

func rdpUsernames(sessions []contract.RDPSessionInfo) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.Username)
	}
	return out
}

func countSessionsByUser(names []string) map[string]int {
	m := make(map[string]int, len(names))
	for _, n := range names {
		k := strings.ToLower(strings.TrimSpace(n))
		if k == "" {
			continue
		}
		m[k]++
	}
	return m
}
