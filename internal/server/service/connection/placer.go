package connection

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// Placer 负责为动态连接选择最佳物理节点。
type Placer struct {
	nodeRepo       *db.NodeRepo
	connectionRepo *db.ConnectionRepo
	settingsSvc    SettingsReader
}

func NewPlacer(nodeRepo *db.NodeRepo, connRepo *db.ConnectionRepo) *Placer {
	return &Placer{
		nodeRepo:       nodeRepo,
		connectionRepo: connRepo,
	}
}

// WithSettings attaches settings reader to resolve cluster-wide connection policies.
func (p *Placer) WithSettings(s SettingsReader) *Placer {
	if p != nil {
		p.settingsSvc = s
	}
	return p
}

func expandCandidateIDs(ctx context.Context, nodeRepo *db.NodeRepo, res model.ConnectionResource) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	for _, id := range res.Nodes {
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	if len(res.NodeGroups) == 0 {
		return ids, nil
	}
	if nodeRepo == nil {
		return nil, fmt.Errorf("node repo unavailable to expand node groups")
	}
	allNodes, err := nodeRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes for candidate groups: %w", err)
	}
	groupSet := make(map[string]struct{}, len(res.NodeGroups))
	for _, group := range res.NodeGroups {
		if group != "" {
			groupSet[group] = struct{}{}
		}
	}
	for _, node := range allNodes {
		for _, group := range node.Groups {
			if _, ok := groupSet[group]; ok {
				ids[node.NodeID] = struct{}{}
				break
			}
		}
	}
	return ids, nil
}

// RankedNodes 返回按 running 实例数升序、node_id 升序排列的健康候选节点。
// 支持传入 excludeNodeIDs，直接从候选名单中剔除已有连接占用的节点。
func (p *Placer) RankedNodes(ctx context.Context, res model.ConnectionResource, excludeNodeIDs map[string]struct{}) ([]model.Node, error) {
	if p == nil || p.nodeRepo == nil {
		return nil, ErrPlacerNoNodeAvailable
	}
	candidateIDs, err := expandCandidateIDs(ctx, p.nodeRepo, res)
	if err != nil {
		return nil, err
	}
	if len(candidateIDs) == 0 {
		return nil, ErrCandidateSetEmpty
	}

	candidateSlice := make([]string, 0, len(candidateIDs))
	for id := range candidateIDs {
		if excludeNodeIDs != nil {
			if _, excluded := excludeNodeIDs[id]; excluded {
				continue
			}
		}
		candidateSlice = append(candidateSlice, id)
	}
	if len(candidateSlice) == 0 {
		return nil, ErrAllNodesOccupied
	}

	nodes, err := p.nodeRepo.ListByNodeIDs(ctx, candidateSlice)
	if err != nil {
		return nil, fmt.Errorf("list candidate nodes: %w", err)
	}

	var healthyNodes []model.Node
	for _, node := range nodes {
		if node.Status == model.NodeStatusOffline || node.Status == model.NodeStatusMaintenance || !node.AllowConnections {
			continue
		}
		if res.Protocol == contract.VNCProtocol && node.OS != "linux" {
			continue
		}
		healthyNodes = append(healthyNodes, node)
	}
	if len(healthyNodes) == 0 {
		return nil, ErrNoHealthyNode
	}

	activeCounts := map[string]int{}
	if p.connectionRepo != nil {
		counts, err := p.connectionRepo.CountActiveByNode(ctx)
		if err == nil {
			activeCounts = counts
		}
	}

	var defaultMaxConn uint
	if p.settingsSvc != nil {
		if cs, err := p.settingsSvc.GetConnectionSettings(ctx); err == nil {
			defaultMaxConn = cs.DefaultMaxConnectionsPerNode
		}
	}

	var eligibleNodes []model.Node
	for _, node := range healthyNodes {
		maxConn := node.MaxConnections
		if maxConn == 0 {
			maxConn = defaultMaxConn
		}
		if maxConn > 0 && activeCounts[node.NodeID] >= int(maxConn) {
			continue
		}
		eligibleNodes = append(eligibleNodes, node)
	}
	if len(eligibleNodes) == 0 {
		return nil, ErrNodeConnectionLimit
	}

	slices.SortFunc(eligibleNodes, func(a, b model.Node) int {
		if c := cmp.Compare(activeCounts[a.NodeID], activeCounts[b.NodeID]); c != 0 {
			return c
		}
		return cmp.Compare(a.NodeID, b.NodeID)
	})
	return eligibleNodes, nil
}

// SelectNode 返回负载最轻的健康候选节点。
func (p *Placer) SelectNode(ctx context.Context, res model.ConnectionResource, excludeNodeIDs map[string]struct{}) (model.Node, error) {
	nodes, err := p.RankedNodes(ctx, res, excludeNodeIDs)
	if err != nil {
		return model.Node{}, err
	}
	if len(nodes) == 0 {
		return model.Node{}, ErrNoHealthyNode
	}
	return nodes[0], nil
}
