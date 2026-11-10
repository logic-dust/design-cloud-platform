package connection

import (
	"cmp"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/model"
)

const (
	defaultUserRefreshCooldown      = 10 * time.Second
	minUserRefreshCooldownRetention = 5 * time.Minute
	maxUserRefreshCooldownEntries   = 1000
)

type nodeUserKey struct {
	nodeID   string
	username string
}

func (s *Service) RefreshUserConnections(ctx context.Context, username string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if !s.allowUserRefresh(username) {
		s.logger.Debug("Skipping duplicate user connection refresh within cooldown window", "username", username)
		return nil
	}

	connections, err := s.connectionRepo.ListByUser(ctx, username)
	if err != nil {
		return fmt.Errorf("list connections for refresh: %w", err)
	}

	// Group seat-backed connections (VNC + XRDP with pid) by (target node, username).
	byNodeUser := make(map[nodeUserKey][]model.Connection)
	for _, conn := range connections {
		switch conn.Protocol {
		case contract.VNCProtocol:
		case contract.XRDPProtocol:
			if conn.PID == nil && conn.Display == nil {
				continue
			}
		default:
			continue
		}
		// 占坑行还在等本次拉起，不能被刷新改成 stopped。
		if conn.Status == model.ConnectionPending {
			continue
		}
		if conn.NodeID == "" {
			continue
		}
		key := nodeUserKey{nodeID: conn.NodeID, username: conn.Username}
		byNodeUser[key] = append(byNodeUser[key], conn)
	}

	for key, conns := range byNodeUser {
		if _, err := s.checkManagedSeatsOnNode(ctx, key.nodeID, conns); err != nil {
			s.logger.Warn("connection check failed for node", "nodeId", key.nodeID, "username", key.username, "error", err)
		}
	}
	return nil
}

func (s *Service) allowUserRefresh(username string) bool {
	now := time.Now()
	window := s.userRefreshWindow()

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	if s.lastUserRefreshAt == nil {
		s.lastUserRefreshAt = make(map[string]time.Time)
	}
	s.pruneStaleUserRefreshLocked(now, window)
	if last, ok := s.lastUserRefreshAt[username]; ok && now.Sub(last) < window {
		return false
	}
	s.lastUserRefreshAt[username] = now
	return true
}

func (s *Service) pruneStaleUserRefreshLocked(now time.Time, window time.Duration) {
	if len(s.lastUserRefreshAt) <= maxUserRefreshCooldownEntries {
		return
	}
	retention := max(10*window, minUserRefreshCooldownRetention)
	cutoff := now.Add(-retention)
	for username, last := range s.lastUserRefreshAt {
		if last.Before(cutoff) {
			delete(s.lastUserRefreshAt, username)
		}
	}
}

func (s *Service) userRefreshWindow() time.Duration {
	if s.userRefreshCooldown > 0 {
		return s.userRefreshCooldown
	}
	return defaultUserRefreshCooldown
}

func (s *Service) checkManagedSeatsOnNode(ctx context.Context, nodeID string, conns []model.Connection) (int, error) {
	if len(conns) == 0 {
		return 0, nil
	}
	orig := make(map[uint]model.Connection, len(conns))
	for _, conn := range conns {
		orig[conn.ID] = conn
	}

	username := conns[0].Username
	dialHost := s.dialHostForNode(ctx, nodeID)
	items := make([]payload.ConnectionCheckItem, 0, len(conns))
	for _, conn := range conns {
		var pidVal uint = 0
		if conn.PID != nil {
			pidVal = *conn.PID
		}
		items = append(items, payload.ConnectionCheckItem{
			ConnectionID: conn.ID,
			Username:     conn.Username,
			Host:         dialHost,
			Port:         conn.Port,
			Display:      conn.Display,
			Protocol:     conn.Protocol.String(),
			PID:          pidVal,
			Name:         conn.Name,
		})
	}

	executionID := fmt.Sprintf("conn-check-%s-%d", nodeID, time.Now().UnixNano())
	req := execution.NewExecuteRequest(execution.ActionConnectionCheck, executionID,
		execution.WithTargetUser(username),
		execution.WithTargetNode(nodeID),
		execution.WithPayload(payload.ConnectionCheck{Connections: items}),
	)

	stdout, err := execution.Run(ctx, s.execSvc, req)
	if err != nil {
		return 0, fmt.Errorf("connection check on node %s: %w", nodeID, err)
	}

	var result payload.ConnectionCheckResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return 0, fmt.Errorf("failed to parse connection check result from node %s: %w", nodeID, err)
	}

	changed := 0
	for _, r := range result.Connections {
		before := orig[r.ConnectionID]
		switch r.State {
		case payload.ConnectionCheckStateRunning:
			fields := map[string]any{
				"status": model.ConnectionRunning,
			}
			rowChanged := before.Status != model.ConnectionRunning
			if r.Info != nil {
				if r.Info.Port > 0 {
					fields["port"] = r.Info.Port
					if r.Info.Port != before.Port {
						rowChanged = true
					}
				}
				if r.Info.PID > 0 {
					fields["pid"] = &r.Info.PID
					if before.PID == nil || *before.PID != r.Info.PID {
						rowChanged = true
					}
				}
			}
			if rowChanged {
				changed++
				_ = s.connectionRepo.UpdateFields(ctx, r.ConnectionID, fields)
			}
		case payload.ConnectionCheckStateStopped:
			port := before.Port
			if before.Protocol == contract.XRDPProtocol {
				port = cmp.Or(port, 3389)
			} else {
				placeholderPort, err := s.connectionRepo.PickPlaceholderPort(ctx, before.NodeID, before.Username)
				if err != nil {
					if s.logger != nil {
						s.logger.Warn("pick placeholder port after connection check stopped", "connectionId", r.ConnectionID, "error", err)
					}
					continue
				}
				port = placeholderPort
			}
			if before.Status != model.ConnectionStopped || before.PID != nil || before.Display != nil {
				changed++
			}
			_ = s.connectionRepo.MarkStoppedClearDisplay(ctx, r.ConnectionID, port)
		default:
			if s.logger != nil {
				s.logger.Debug("connection check returned unknown, skipping", "connectionId", r.ConnectionID, "message", r.Message)
			}
		}
	}
	return changed, nil
}
