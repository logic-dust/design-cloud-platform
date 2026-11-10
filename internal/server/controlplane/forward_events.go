package controlplane

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"log/slog"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/concurrency"
	"design-cloud-engine/internal/platform/crypto"
	"design-cloud-engine/internal/platform/host"
	"design-cloud-engine/internal/server/controlplane/leader"
	"design-cloud-engine/internal/server/push"
)

// ClusterEventForwarderConfig is the topology the forwarder needs. It is a plain
// value so the composition root can pass config fields without the control plane
// knowing the whole config type.
type ClusterEventForwarderConfig struct {
	// NodeID is this server's identity; peers with the same ID are skipped.
	NodeID string
	// Members is the expanded cluster membership, used for the single-node
	// short-circuit.
	Members []string
	// ServerPatterns keeps the configured entries (host patterns included) so
	// expansion semantics stay identical to the previous inline closure.
	ServerPatterns []string
}

// NewClusterEventForwarder builds the push hub's cross-node callback.
//
// Leader semantics are unchanged from the original inline implementation:
//   - leader: fan out to every peer node;
//   - standby: forward only to the observed leader, drop during the election
//     window (TTL 30s and receiver-side dedup absorb re-delivery);
//   - single node: short-circuit.
//
// TTL, dedup and concurrency limits stay control-plane policy; the push hub only
// sees an opaque callback, so it never imports this package.
func NewClusterEventForwarder(cfg ClusterEventForwarderConfig, leaderState leader.Reader, forwarder *ForwardClient, logger *slog.Logger) push.ClusterForwarder {
	if logger == nil {
		logger = slog.Default()
	}
	return func(_ context.Context, targetType string, userIDs []string, role model.Role, msg push.Message) {
		if len(cfg.Members) <= 1 || forwarder == nil {
			return
		}
		var payloadJSON string
		if msg.Payload != nil {
			if b, err := json.Marshal(msg.Payload); err == nil {
				payloadJSON = string(b)
			}
		}
		req := &pb.ForwardClusterEventRequest{
			SourceNodeId: cfg.NodeID,
			EventId:      crypto.GenRandomId(24),
			ExpiresAt:    time.Now().Add(ClusterEventDefaultTTL).Unix(),
			TargetType:   targetType,
			UserIds:      userIDs,
			Role:         int32(role),
			EventName:    string(msg.Name),
			PayloadJson:  payloadJSON,
		}

		if leaderState != nil && !leaderState.IsLeader() {
			leaderID := leaderState.LeaderID()
			if leaderID != "" && leaderID != cfg.NodeID {
				if !concurrency.TryBoundedGo(ForwardSlots, logger, "forward-cluster-event-standby", func() {
					fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = forwarder.ForwardClusterEvent(fCtx, leaderID, req)
				}) {
					logger.Warn("dropping cluster event: forwarding capacity is full",
						"event", msg.Name, "target", leaderID)
				}
				return
			}
			// 选主窗口期（Leader 未观测到）：事件本周期丢弃（TTL 30s，
			// 窗口期重复投递由接收端 event_dedup 收敛）。显式记录，避免静默丢失不可排查。
			logger.Warn("cluster push event dropped on standby: leader not available",
				"event", msg.Name, "event_id", req.EventId, "leader_id", leaderID)
			return
		}

		for _, srv := range cfg.ServerPatterns {
			nodeIDs, err := host.ExpandHostPattern(srv)
			if err != nil {
				continue
			}
			for _, nid := range nodeIDs {
				if nid == cfg.NodeID {
					continue
				}
				targetID := nid
				if !concurrency.TryBoundedGo(ForwardSlots, logger, "forward-cluster-event-leader", func() {
					fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = forwarder.ForwardClusterEvent(fCtx, targetID, req)
				}) {
					logger.Warn("dropping cluster event: forwarding capacity is full",
						"event", msg.Name, "target", targetID)
				}
			}
		}
	}
}
