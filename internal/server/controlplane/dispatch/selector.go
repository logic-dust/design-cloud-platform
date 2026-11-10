package dispatch

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

func (d *Dispatcher) connectedEligibleAgent(ctx context.Context, nodeID string, selector []string) bool {
	if !d.registry.IsConnected(nodeID) {
		return false
	}
	node, err := d.nodes.GetByNodeID(ctx, nodeID)
	if err != nil || node.Status != model.NodeStatusOnline {
		return false
	}
	return model.NodeHasGroups(node.Groups, selector)
}

func (d *Dispatcher) selectTargetAgent(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	if req.TargetNode != "" {
		if !d.registry.IsConnected(req.TargetNode) {
			d.logger.Warn("Target agent not connected",
				"target_node", req.TargetNode,
				"trace_id", req.TraceID)
			return "", fmt.Errorf("target agent not connected: node %s is offline or unreachable", req.TargetNode)
		}
		node, err := d.nodes.GetByNodeID(ctx, req.TargetNode)
		if err != nil {
			if !errors.Is(err, contract.ErrNotFound) {
				d.logger.Warn("failed to check node maintenance status, denying dispatch",
					"target_node", req.TargetNode, "error", err)
				return "", fmt.Errorf("maintenance check failed: %w", err)
			}
			// 节点仍有实时连接但尚未入库时允许定向派发。
		} else if node.Status == model.NodeStatusMaintenance {
			return "", errNodeInMaintenance
		} else if node.Status != model.NodeStatusOnline {
			return "", fmt.Errorf("target agent not connected: node %s is not online", req.TargetNode)
		}
		d.logger.Debug("Using pinned target agent", "target_node_id", req.TargetNode)
		return req.TargetNode, nil
	}

	var targetNodeID string
	switch req.NodeAffinity {
	case execution.NodeAffinityLocalPreferred:
		if d.connectedEligibleAgent(ctx, d.nodeID, req.NodeSelector) {
			targetNodeID = d.nodeID
			d.logger.Debug("Using preferred local agent", "target_node_id", targetNodeID)
		} else {
			targetNodeID = d.selectLeastLoadedAgent(ctx, req.NodeSelector)
		}
	default:
		targetNodeID = d.selectLeastLoadedAgent(ctx, req.NodeSelector)
	}

	if targetNodeID == "" {
		d.logger.Warn("No agents available for dispatch",
			"trace_id", req.TraceID,
			"execution_id", req.ExecutionID,
			"node_selector", req.NodeSelector,
			"node_affinity", req.NodeAffinity)
		return "", fmt.Errorf("no agents available")
	}
	d.logger.Debug("Using pool-selected agent",
		"target_node_id", targetNodeID,
		"affinity", req.NodeAffinity)
	return targetNodeID, nil
}

// selectLeastLoadedAgent uses Power of Two Choices after one batched DB lookup.
func (d *Dispatcher) selectLeastLoadedAgent(ctx context.Context, selector []string) string {
	connectedNodeIDs := d.registry.ConnectedNodeIDs()
	if len(connectedNodeIDs) == 0 {
		return ""
	}

	nodes, err := d.nodes.ListByNodeIDs(ctx, connectedNodeIDs)
	if err != nil {
		d.logger.Warn("Failed to batch load node records for dispatch", "error", err)
		return ""
	}
	nodeByID := make(map[string]model.Node, len(nodes))
	for _, node := range nodes {
		nodeByID[node.NodeID] = node
	}

	eligible := make([]string, 0, len(connectedNodeIDs))
	for _, nodeID := range connectedNodeIDs {
		node, ok := nodeByID[nodeID]
		if !ok || node.Status != model.NodeStatusOnline ||
			!model.NodeHasGroups(node.Groups, selector) {
			continue
		}
		eligible = append(eligible, nodeID)
	}

	switch len(eligible) {
	case 0:
		return ""
	case 1:
		return eligible[0]
	}

	firstIndex := rand.IntN(len(eligible))
	secondIndex := rand.IntN(len(eligible))
	if firstIndex == secondIndex {
		return eligible[firstIndex]
	}

	first, second := eligible[firstIndex], eligible[secondIndex]
	if d.nodeScore(first) <= d.nodeScore(second) {
		return first
	}
	return second
}

func (d *Dispatcher) nodeScore(nodeID string) float64 {
	snapshot, ok := d.registry.Snapshot(nodeID)
	if !ok || snapshot.LatestFrame == nil {
		return 99999.0
	}
	return float64(snapshot.LatestFrame.ActiveTasks)*100.0 + snapshot.LatestFrame.CpuUtil
}
