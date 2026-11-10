package node

import (
	"context"
	"fmt"
	"strings"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

const maxGroupBatchNodeIDs = 10_000

func (s *Service) resolveExistingNodeIDs(ctx context.Context, nodeIDs []string) ([]string, error) {
	if len(nodeIDs) > maxGroupBatchNodeIDs {
		return nil, contract.NewValidationError(fmt.Sprintf("nodeIds exceeds limit of %d", maxGroupBatchNodeIDs))
	}
	seen := make(map[string]struct{}, len(nodeIDs))
	var unique []string
	for _, id := range nodeIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil, contract.NewValidationError("nodeIds is required")
	}
	found, err := s.nodeRepo.ListByNodeIDs(ctx, unique)
	if err != nil {
		return nil, err
	}
	foundSet := make(map[string]struct{}, len(found))
	for _, n := range found {
		foundSet[n.NodeID] = struct{}{}
	}
	var missing []string
	for _, id := range unique {
		if _, ok := foundSet[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, MissingNodeIDsError{MissingNodeIDs: missing}
	}
	return unique, nil
}

func (s *Service) resolveSelectorNodes(ctx context.Context, sel *NodeSettingsBatchSelector) ([]model.Node, error) {
	if sel == nil || sel.IsEmpty() {
		return nil, contract.NewValidationError("selector cannot be empty")
	}
	if len(sel.NodeIDs) > maxGroupBatchNodeIDs {
		return nil, contract.NewValidationError(fmt.Sprintf("nodeIds exceeds limit of %d", maxGroupBatchNodeIDs))
	}
	for _, g := range sel.Groups {
		if err := model.ValidateNodeGroupName(g); err != nil {
			return nil, contract.NewValidationError(err.Error())
		}
	}

	var parsedStatuses []model.NodeStatus
	for _, st := range sel.Status {
		parsed, err := model.ParseNodeStatus(st)
		if err != nil {
			return nil, contract.NewValidationError(fmt.Sprintf("invalid status %q in selector", st))
		}
		if !parsed.IsRuntimeStatus() {
			return nil, contract.NewValidationError(fmt.Sprintf("status %q is not a runtime filter (online, offline, maintenance)", st))
		}
		parsedStatuses = append(parsedStatuses, parsed)
	}

	if len(sel.NodeIDs) > 0 {
		if _, err := s.resolveExistingNodeIDs(ctx, sel.NodeIDs); err != nil {
			return nil, err
		}
	}

	filter := db.NodeFilter{
		NodeIDs: sel.NodeIDs,
		OS:      sel.OS,
		Arch:    sel.Arch,
		Status:  parsedStatuses,
		Groups:  sel.Groups,
	}
	return s.nodeRepo.ListByFilter(ctx, filter)
}

// ResolveSelectorNodeIDs resolves batch selector to a fixed node ID snapshot (pagination happens after this).
func (s *Service) ResolveSelectorNodeIDs(ctx context.Context, sel *NodeSettingsBatchSelector) ([]string, error) {
	nodes, err := s.resolveSelectorNodes(ctx, sel)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.NodeID
	}
	return ids, nil
}

func (s *Service) ListViewsFiltered(ctx context.Context, groups []string) ([]NodeAdminView, error) {
	if len(groups) == 0 {
		return s.ListViews(ctx)
	}
	for _, g := range groups {
		if err := model.ValidateNodeGroupName(g); err != nil {
			return nil, contract.NewValidationError(err.Error())
		}
	}
	nodes, err := s.resolveSelectorNodes(ctx, &NodeSettingsBatchSelector{Groups: groups})
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	out := make([]NodeAdminView, len(nodes))
	for i, n := range nodes {
		out[i] = BuildNodeAdminView(n)
	}
	return out, nil
}
