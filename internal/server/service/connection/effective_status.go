package connection

import (
	"context"

	"design-cloud-engine/internal/model"
)

// withEffectiveStatuses overlays node online/maintenance onto connection.Status for API responses.
// Terminating rows should already be filtered from user-facing lists.
func (s *Service) withEffectiveStatuses(ctx context.Context, conns []model.Connection) ([]model.Connection, error) {
	if len(conns) == 0 || s.nodeRepo == nil {
		return conns, nil
	}
	ids := make([]string, 0, len(conns))
	seen := make(map[string]struct{}, len(conns))
	for _, c := range conns {
		if c.NodeID == "" {
			continue
		}
		if _, ok := seen[c.NodeID]; ok {
			continue
		}
		seen[c.NodeID] = struct{}{}
		ids = append(ids, c.NodeID)
	}
	if len(ids) == 0 {
		return conns, nil
	}
	nodes, err := s.nodeRepo.ListByNodeIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.Node, len(nodes))
	for _, n := range nodes {
		byID[n.NodeID] = n
	}
	out := make([]model.Connection, len(conns))
	copy(out, conns)
	for i := range out {
		n, ok := byID[out[i].NodeID]
		if !ok {
			if out[i].NodeID != "" && out[i].Status == model.ConnectionRunning {
				out[i].Status = model.ConnectionUnknown
			}
			continue
		}
		switch n.Status {
		case model.NodeStatusMaintenance:
			out[i].Status = model.ConnectionMaintenance
		case model.NodeStatusOffline:
			if out[i].Status != model.ConnectionStopped && out[i].Status != model.ConnectionPending {
				out[i].Status = model.ConnectionUnknown
			}
		}
	}
	return out, nil
}

func excludeTerminating(conns []model.Connection) []model.Connection {
	out := make([]model.Connection, 0, len(conns))
	for _, c := range conns {
		if c.Status == model.ConnectionTerminating {
			continue
		}
		out = append(out, c)
	}
	return out
}
