package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

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

	// Group fixed VNC connections by target node.
	byNode := make(map[string][]model.Connection)
	var noNode []model.Connection
	for _, conn := range connections {
		src := conn.SourceType()
		if src != model.ConnectionSourceFixed || conn.Protocol != contract.VNCProtocol {
			continue
		}
		nodeID := conn.TargetNodeID
		if nodeID == "" {
			noNode = append(noNode, conn)
			continue
		}
		byNode[nodeID] = append(byNode[nodeID], conn)
	}

	// Connections with no TargetNodeID: mark unknown conservatively.
	for _, conn := range noNode {
		if conn.Status == model.ConnectionUnknown || conn.Status == model.ConnectionStopped {
			continue
		}
		if err := s.updateStatus(ctx, &contract.ConnectionInfo{ConnectionID: conn.ID}, "", model.ConnectionUnknown); err != nil {
			s.logger.Warn("failed to mark connection unknown", "connectionId", conn.ID, "error", err)
		}
	}

	// For each node, dispatch CONNECTION_CHECK and merge results.
	for nodeID, conns := range byNode {
		if err := s.checkConnectionsOnNode(ctx, nodeID, conns); err != nil {
			s.logger.Warn("connection check failed for node", "nodeId", nodeID, "error", err)
		}
	}
	s.invalidateConnListCache(username)
	return nil
}

func (s *Service) allowUserRefresh(username string) bool {
	now := s.clockNow()
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

func (s *Service) checkConnectionsOnNode(ctx context.Context, nodeID string, conns []model.Connection) error {
	if len(conns) == 0 {
		return nil
	}

	// All connections belong to the same user (filtered by ListByUser).
	username := conns[0].Username
	items := make([]payload.ConnectionCheckItem, 0, len(conns))
	for _, conn := range conns {
		items = append(items, payload.ConnectionCheckItem{
			ConnectionID: conn.ID,
			Username:     conn.Username,
			Host:         conn.Host,
			Port:         conn.Port,
			Display:      conn.Display,
			Protocol:     string(conn.Protocol),
			PID:          conn.PID,
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
		return fmt.Errorf("connection check on node %s: %w", nodeID, err)
	}

	var result payload.ConnectionCheckResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return fmt.Errorf("failed to parse connection check result from node %s: %w", nodeID, err)
	}

	for _, r := range result.Connections {
		switch r.State {
		case payload.ConnectionCheckStateRunning:
			info := connectionCheckItemToInfo(r)
			if err := s.updateStatus(ctx, info, nodeID, model.ConnectionRunning); err != nil {
				s.logger.Warn("failed to update running connection", "connectionId", r.ConnectionID, "error", err)
			}
		case payload.ConnectionCheckStateStopped:
			if err := s.updateStatus(ctx, &contract.ConnectionInfo{ConnectionID: r.ConnectionID}, nodeID, model.ConnectionStopped); err != nil {
				s.logger.Warn("failed to mark connection stopped", "connectionId", r.ConnectionID, "error", err)
			}
		default:
			s.logger.Debug("connection check returned unknown, skipping", "connectionId", r.ConnectionID, "message", r.Message)
		}
	}
	return nil
}

func connectionCheckItemToInfo(r payload.ConnectionCheckItemResult) *contract.ConnectionInfo {
	info := &contract.ConnectionInfo{ConnectionID: r.ConnectionID}
	if r.Info != nil {
		info.Host = r.Info.Host
		info.Port = r.Info.Port
		info.Display = r.Info.Display
		info.Protocol = contract.Protocol(r.Info.Protocol)
		info.PID = r.Info.PID
		info.Name = r.Info.Name
		info.Username = r.Info.Username
	}
	return info
}
