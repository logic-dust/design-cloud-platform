package connection

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// maxTiedWarnEntries 限制并列告警去重表的容量，避免长期运行后无界增长。
const maxTiedWarnEntries = 1024

type resourceMatchInput struct {
	Resources  []model.ConnectionResource
	NodeID     string
	NodeGroups []string
	Protocol   contract.Protocol
	Display    *int
}

// candidateScore 用于对满足硬性过滤条件的候选规格打分排序。
type candidateScore struct {
	fixedDisplay int // res.Display != nil 为 1，否则 0
	nodeExact    int // nodeID 在 res.Nodes 中为 1，否则 0
	singleNode   int // len(res.Nodes) == 1 && len(res.NodeGroups) == 0 为 1，否则 0
	publicAuth   int // res.Public 为 1，否则 0：公开规格优先，避免无归属实例先消耗私有规格配额
}

func compareCandidateScore(a, b candidateScore) int {
	if c := cmp.Compare(a.fixedDisplay, b.fixedDisplay); c != 0 {
		return c
	}
	if c := cmp.Compare(a.nodeExact, b.nodeExact); c != 0 {
		return c
	}
	if c := cmp.Compare(a.singleNode, b.singleNode); c != 0 {
		return c
	}
	return cmp.Compare(a.publicAuth, b.publicAuth)
}

// matchConnectionResource 在恰好一个最优规格时返回该规格。
// 若无匹配或存在并列最优候选（四键完全一致），返回 false 及匹配到的并列最优规格列表（用于外部日志/告警）。
func matchConnectionResource(in resourceMatchInput) (model.ConnectionResource, []model.ConnectionResource, bool) {
	if in.NodeID == "" || len(in.Resources) == 0 {
		return model.ConnectionResource{}, nil, false
	}
	if (in.Protocol == contract.VNCProtocol || in.Protocol == contract.XRDPProtocol) && in.Display == nil {
		return model.ConnectionResource{}, nil, false
	}

	type rankedCandidate struct {
		res   model.ConnectionResource
		score candidateScore
	}
	var eligible []rankedCandidate

	for _, res := range in.Resources {
		// 1. 协议一致
		if res.Protocol != in.Protocol {
			continue
		}

		// 2. 节点命中：res.Nodes 直指 或 node.Groups 与 res.NodeGroups 有交集
		nodeExact := slices.Contains(res.Nodes, in.NodeID)
		groupHit := false
		if !nodeExact && len(res.NodeGroups) > 0 && len(in.NodeGroups) > 0 {
			for _, ng := range res.NodeGroups {
				if ng != "" && slices.Contains(in.NodeGroups, ng) {
					groupHit = true
					break
				}
			}
		}
		if !nodeExact && !groupHit {
			continue
		}

		// 3. Display 约束
		if in.Display != nil {
			displayVal := *in.Display
			if res.Display != nil {
				if *res.Display != displayVal {
					continue
				}
			} else {
				if displayVal < dynamicDisplayMin || displayVal > dynamicDisplayMax {
					continue
				}
			}
		} else {
			if res.Display != nil {
				continue
			}
		}

		score := candidateScore{}
		if res.Display != nil {
			score.fixedDisplay = 1
		}
		if nodeExact {
			score.nodeExact = 1
		}
		if len(res.Nodes) == 1 && len(res.NodeGroups) == 0 {
			score.singleNode = 1
		}
		if res.Public {
			score.publicAuth = 1
		}

		eligible = append(eligible, rankedCandidate{
			res:   res,
			score: score,
		})
	}

	if len(eligible) == 0 {
		return model.ConnectionResource{}, nil, false
	}

	// 降序排序，最优排在最前面
	slices.SortFunc(eligible, func(a, b rankedCandidate) int {
		return compareCandidateScore(b.score, a.score)
	})

	best := eligible[0]
	// 检查是否有与 best 得分完全相同的并列候选
	var tied []model.ConnectionResource
	for _, cand := range eligible {
		if compareCandidateScore(cand.score, best.score) == 0 {
			tied = append(tied, cand.res)
		} else {
			break
		}
	}

	if len(tied) > 1 {
		return model.ConnectionResource{}, tied, false
	}

	return best.res, nil, true
}

func (s *Service) warnTiedCandidateResources(nodeID, username string, display *int, tied []model.ConnectionResource) {
	if s == nil || s.logger == nil || len(tied) <= 1 {
		return
	}
	ids := make([]string, 0, len(tied))
	for _, res := range tied {
		ids = append(ids, fmt.Sprintf("%d", res.ID))
	}
	slices.Sort(ids)
	idsKey := strings.Join(ids, ",")
	slotKey := fmt.Sprintf("%s:%s:rdp", nodeID, username)
	if display != nil {
		slotKey = fmt.Sprintf("%s:%s:%d", nodeID, username, *display)
	}

	s.tiedWarnMu.Lock()
	if s.tiedWarned == nil {
		s.tiedWarned = make(map[string]string)
	}
	if len(s.tiedWarned) >= maxTiedWarnEntries {
		clear(s.tiedWarned)
	}
	prev, exists := s.tiedWarned[slotKey]
	if exists && prev == idsKey {
		s.tiedWarnMu.Unlock()
		return
	}
	s.tiedWarned[slotKey] = idsKey
	s.tiedWarnMu.Unlock()

	if display != nil {
		s.logger.Warn("vnc desktop matches multiple tied connection resources; remaining unassigned",
			"node_id", nodeID,
			"username", username,
			"display", *display,
			"candidate_resource_ids", idsKey,
		)
		return
	}
	s.logger.Warn("desktop session matches multiple tied connection resources; remaining unassigned",
		"node_id", nodeID,
		"username", username,
		"candidate_resource_ids", idsKey,
	)
}

// vncMatchCache 用于在单次对账批次内缓存用户可访问资源与节点详情，避免 N+1 查询。
type vncMatchCache struct {
	userResources map[string][]model.ConnectionResource // username -> accessible resources
	userNotFound  map[string]bool                       // username -> not found
	nodeGroups    map[string][]string                   // nodeID -> groups
}

func newVNCMatchCache() *vncMatchCache {
	return &vncMatchCache{
		userResources: make(map[string][]model.ConnectionResource),
		userNotFound:  make(map[string]bool),
		nodeGroups:    make(map[string][]string),
	}
}

// matchResource 为服务层取数并按协议执行规格匹配，对账循环可通过 cache 跨连接共享数据。
func (s *Service) matchResource(ctx context.Context, username, nodeID string, proto contract.Protocol, display *int, cache *vncMatchCache) (model.ConnectionResource, []model.ConnectionResource, bool) {
	if s == nil || s.connectionResourceRepo == nil || s.userSvc == nil {
		return model.ConnectionResource{}, nil, false
	}
	if username == "" || nodeID == "" {
		return model.ConnectionResource{}, nil, false
	}

	var resources []model.ConnectionResource
	if cache != nil {
		if cache.userNotFound[username] {
			return model.ConnectionResource{}, nil, false
		}
		if cached, ok := cache.userResources[username]; ok {
			resources = cached
		}
	}

	if resources == nil {
		user, err := s.userSvc.GetDBUser(ctx, username)
		if err != nil || user.ID == 0 {
			if err != nil && !errors.Is(err, contract.ErrNotFound) && s.logger != nil {
				s.logger.Warn("resource match: resolve user failed", "username", username, "error", err)
			}
			if cache != nil {
				cache.userNotFound[username] = true
			}
			return model.ConnectionResource{}, nil, false
		}
		list, err := s.connectionResourceRepo.ListAccessibleForUser(ctx, user.ID)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("resource match: list accessible resources failed", "username", username, "error", err)
			}
			return model.ConnectionResource{}, nil, false
		}
		resources = list
		if cache != nil {
			cache.userResources[username] = resources
		}
	}

	var nodeGroups []string
	if cache != nil {
		if cached, ok := cache.nodeGroups[nodeID]; ok {
			nodeGroups = cached
		}
	}
	if nodeGroups == nil && s.nodeRepo != nil {
		node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
		if err == nil {
			nodeGroups = node.Groups
		} else if !errors.Is(err, contract.ErrNotFound) && s.logger != nil {
			s.logger.Warn("resource match: get node failed", "node_id", nodeID, "error", err)
		}
		if cache != nil {
			cache.nodeGroups[nodeID] = nodeGroups
		}
	}

	return matchConnectionResource(resourceMatchInput{
		Resources:  resources,
		NodeID:     nodeID,
		NodeGroups: nodeGroups,
		Protocol:   proto,
		Display:    display,
	})
}

// matchVNCResource 为服务层取数并执行匹配，对账循环可通过 cache 跨连接共享数据。
func (s *Service) matchVNCResource(ctx context.Context, username, nodeID string, display *int, cache *vncMatchCache) (model.ConnectionResource, []model.ConnectionResource, bool) {
	return s.matchResource(ctx, username, nodeID, contract.VNCProtocol, display, cache)
}
