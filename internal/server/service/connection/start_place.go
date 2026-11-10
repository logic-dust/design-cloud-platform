package connection

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// startDialViaResource reactivates a stopped dial row (RDP/XRDP) by re-running
// resource node placement onto the same connection id (tombstone card stays stable).
func (s *Service) startDialViaResource(ctx context.Context, conn model.Connection, requestUser string) error {
	if conn.ResourceID == nil {
		return contract.NewInvalidParamError("缺失 resource_id，无法启动连接")
	}
	if s.connectionResourceRepo == nil {
		return fmt.Errorf("connection resource repo uninitialized")
	}
	res, err := s.connectionResourceRepo.GetByID(ctx, *conn.ResourceID)
	if err != nil {
		return err
	}
	_, err = s.placeDialOnConnection(ctx, conn, res, requestUser)
	return err
}

// placeDialOnConnection selects a node for the resource and writes it onto conn
// (same row). Sibling instances' nodes are excluded; this row's previous node is
// not sticky — it may be chosen again only if the placer ranks it best.
func (s *Service) placeDialOnConnection(ctx context.Context, conn model.Connection, res model.ConnectionResource, requestUser string) (model.Connection, error) {
	if s.placer == nil {
		return model.Connection{}, fmt.Errorf("placer uninitialized")
	}
	if conn.ResourceID == nil {
		return model.Connection{}, contract.NewInvalidParamError("缺失 resource_id")
	}

	siblings, err := s.connectionRepo.ListByUsernameAndResource(ctx, conn.Username, *conn.ResourceID)
	if err != nil {
		return model.Connection{}, fmt.Errorf("list connections: %w", err)
	}
	usedNodes := make(map[string]struct{})
	for _, c := range siblings {
		if c.ID == conn.ID || c.NodeID == "" {
			continue
		}
		// Only exclude nodes held by other live (non-stopped) siblings so a
		// stopped peer does not block placement; stopped peers are tombstones.
		if c.Status == model.ConnectionStopped || c.Status == model.ConnectionTerminating {
			continue
		}
		usedNodes[c.NodeID] = struct{}{}
	}

	node, err := s.placer.SelectNode(ctx, res, usedNodes)
	if err != nil {
		return model.Connection{}, fmt.Errorf("select node for %s failed: %w", res.Protocol.String(), err)
	}

	dialPort := cmp.Or(res.Protocol.DialDefaultPort(), 22)
	if res.Port != nil && *res.Port > 0 {
		dialPort = *res.Port
	}
	now := time.Now()
	fields := map[string]any{
		"status":           model.ConnectionRunning,
		"node_id":          node.NodeID,
		"port":             dialPort,
		"name":             model.ConnectionName(res.Name, node.NodeID),
		"display":          nil,
		"pid":              nil,
		"last_accessed_at": now,
	}
	if res.Protocol == contract.XRDPProtocol && res.Display != nil {
		fields["display"] = res.Display
	}
	if err := s.connectionRepo.UpdateFields(ctx, conn.ID, fields); err != nil {
		return model.Connection{}, fmt.Errorf("update dial placement: %w", err)
	}
	conn.Status = model.ConnectionRunning
	conn.NodeID = node.NodeID
	conn.Port = dialPort
	conn.Name = model.ConnectionName(res.Name, node.NodeID)
	conn.Display = nil
	if res.Protocol == contract.XRDPProtocol && res.Display != nil {
		conn.Display = res.Display
	}
	conn.PID = nil
	conn.LastAccessedAt = &now
	s.notifyOwnerRefresh(ctx, requestUser, conn.Username, conn.ID)
	s.attachDialHost(ctx, &conn)
	return conn, nil
}

// refreshDialAccess bumps last_accessed on an already-running dial row (no re-place).
func (s *Service) refreshDialAccess(ctx context.Context, conn model.Connection, requestUser string) (model.Connection, error) {
	now := time.Now()
	if err := s.connectionRepo.UpdateFields(ctx, conn.ID, map[string]any{
		"status":           model.ConnectionRunning,
		"last_accessed_at": now,
	}); err != nil {
		return model.Connection{}, fmt.Errorf("refresh dial connection: %w", err)
	}
	conn.Status = model.ConnectionRunning
	conn.LastAccessedAt = &now
	s.notifyOwnerRefresh(ctx, requestUser, conn.Username, conn.ID)
	s.attachDialHost(ctx, &conn)
	return conn, nil
}

func (s *Service) loadResourceForConn(ctx context.Context, conn model.Connection) (model.ConnectionResource, error) {
	if conn.ResourceID == nil {
		return model.ConnectionResource{}, contract.NewInvalidParamError("缺失 resource_id")
	}
	if s.connectionResourceRepo == nil {
		return model.ConnectionResource{}, fmt.Errorf("connection resource repo uninitialized")
	}
	return s.connectionResourceRepo.GetByID(ctx, *conn.ResourceID)
}

// startVNCViaResource starts a stopped VNC row by re-selecting a node from the
// resource (fixed-display resources stay on their sole node).
func (s *Service) startVNCViaResource(ctx context.Context, conn model.Connection, requestUser string) error {
	res, err := s.loadResourceForConn(ctx, conn)
	if err != nil {
		return err
	}

	var candidates []model.Node
	if res.Display != nil {
		if len(res.Nodes) != 1 {
			return fmt.Errorf("fixed display requires exactly one node")
		}
		node, err := s.nodeRepo.GetByNodeID(ctx, res.Nodes[0])
		if err != nil {
			return fmt.Errorf("get target node %s: %w", res.Nodes[0], err)
		}
		if node.Status == model.NodeStatusOffline || node.Status == model.NodeStatusMaintenance || !node.AllowConnections || node.OS != "linux" {
			return ErrTargetNodeUnavailable
		}
		candidates = []model.Node{node}
	} else {
		if s.placer == nil {
			return fmt.Errorf("placer uninitialized")
		}
		ranked, err := s.placer.RankedNodes(ctx, res, nil)
		if err != nil {
			return err
		}
		candidates = ranked
	}
	if len(candidates) == 0 {
		return ErrNoHealthyNode
	}

	now := time.Now()
	if err := s.connectionRepo.MarkPendingFromStopped(ctx, conn.ID, now); err != nil {
		return err
	}

	var lastErr error
	for _, targetNode := range candidates {
		if err := s.connectionRepo.UpdateFields(ctx, conn.ID, map[string]any{
			"node_id": targetNode.NodeID,
			"name":    model.ConnectionName(res.Name, targetNode.NodeID),
		}); err != nil {
			lastErr = err
			continue
		}

		var fixed *int
		if res.Display != nil {
			fixed = res.Display
		}
		var avoid []int
		if fixed == nil {
			avoid, err = s.buildAvoidDisplays(ctx, targetNode.NodeID, res.ID)
			if err != nil {
				lastErr = err
				continue
			}
		}

		chosenDisplay, port, pid, err := s.launchVNCOnNode(ctx, targetNode, conn.Username, conn.ID, fixed, avoid, res.VNCConfig)
		if err != nil {
			lastErr = fmt.Errorf("node %s: %w", targetNode.NodeID, err)
			continue
		}
		var pidPtr *uint
		if pid > 0 {
			pidPtr = &pid
		}
		if err := s.connectionRepo.MarkRunningFromPending(ctx, conn.ID, chosenDisplay, port, pidPtr, time.Now()); err != nil {
			s.rollbackLaunchedVNC(ctx, targetNode.NodeID, conn.Username, conn.ID, chosenDisplay)
			lastErr = err
			if db.IsUniqueConstraintError(err) {
				lastErr = ErrDisplayConflict
			}
			continue
		}
		s.notifyOwnerRefresh(ctx, requestUser, conn.Username, conn.ID)
		return nil
	}

	s.revertStartToStopped(ctx, conn)
	s.notifyOwnerRefresh(ctx, requestUser, conn.Username, conn.ID)
	if lastErr != nil {
		return fmt.Errorf("launch vnc failed: %w", lastErr)
	}
	return ErrNoHealthyNode
}
