package controlplane

// 本文件负责控制面向 Agent 主动下发的命令：采集探针广播与节点配置推送。
//
// 这两类动作只需要「连接注册表 + 集群转发 + 配置读取」三种能力，不依赖控制面
// 的入站 gRPC 适配（Server）或其业务端口回填。因此它们挂在 Runtime / SettingsPusher
// 上，可在业务层构造之前创建，装配顺序由此变成
//
//	Runtime → SettingsPusher（依赖 settings 读能力） → service → Server
//
// 而不是「service 需要 Server、Server 又需要 service」的构造环。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/platform/crypto"
)

// TriggerCollect 向所有在线 Agent 广播一次采集探针请求。
func (rt *Runtime) TriggerCollect(ctx context.Context, target contract.CollectTarget) error {
	probeName := ""
	switch target {
	case contract.CollectTargetConnection:
		probeName = "connection-probe"
	case contract.CollectTargetMetrics:
		probeName = "metrics"
	case contract.CollectTargetStorage:
		probeName = "storage-probe"
	default:
		return fmt.Errorf("unknown collect target: %q", target)
	}

	return rt.broadcastCommand(ctx, probeName, &pb.ServerCommand{
		Payload: &pb.ServerCommand_CollectProbeCommand{
			CollectProbeCommand: &pb.CollectProbeCommand{
				ProbeName:   probeName,
				TraceId:     crypto.GenRandomId(24),
				RequestedAt: time.Now().Unix(),
			},
		},
	})
}

func (rt *Runtime) broadcastCommand(ctx context.Context, name string, command *pb.ServerCommand) error {
	nodeIDs := rt.registry.ConnectedNodeIDs()
	if len(nodeIDs) == 0 {
		// 无在线 Agent 是正常运维态；视为空操作，避免 API 返回 500。
		rt.logger.Warn("command skipped: no connected agents", "command", name)
		return nil
	}

	var errs []error
	for _, nodeID := range nodeIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := rt.registry.SendCommand(nodeID, command); err != nil {
			rt.logger.Warn("failed to send agent command",
				"command", name, "nodeID", nodeID, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
		}
	}
	return errors.Join(errs...)
}

// SettingsPusher 把「有效配置」推送给 Agent。
//
// 它只依赖 Runtime（连接与转发）和配置读取端口（由 settings 服务实现），
// 因此可以在 service 构造过程中被注入 node 服务——这是消除 control↔service
// 构造环的关键：推送能力不再由 Server 提供。
type SettingsPusher struct {
	rt       *Runtime
	provider contract.AgentSettingsProvider
}

// NewSettingsPusher binds config pushing to the control runtime and a settings reader.
func NewSettingsPusher(rt *Runtime, provider contract.AgentSettingsProvider) *SettingsPusher {
	return &SettingsPusher{rt: rt, provider: provider}
}

// Broadcast 向所有已连接 Agent 推送最新有效配置（按节点动态覆盖）。
func (p *SettingsPusher) Broadcast(ctx context.Context) error {
	nodeIDs := p.rt.registry.ConnectedNodeIDs()
	if len(nodeIDs) == 0 {
		return nil
	}

	var errs []error
	for _, nodeID := range nodeIDs {
		if err := p.rt.registry.SendCommand(nodeID, p.updateCommand(ctx, nodeID)); err != nil {
			p.rt.logger.Warn("Failed to send UpdateSettings command", "nodeID", nodeID, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
		} else {
			p.rt.logger.Info("Pushed updated settings to agent", "nodeID", nodeID)
		}
	}
	return errors.Join(errs...)
}

// PushNodeSettingsUpdate 向单个节点推送有效配置；Standby 时转发到 Leader。
func (p *SettingsPusher) PushNodeSettingsUpdate(ctx context.Context, nodeID string) error {
	rt := p.rt
	if rt.leader != nil && !rt.leader.IsLeader() {
		leaderID := rt.leader.LeaderID()
		if leaderID != "" && leaderID != rt.nodeID && rt.forwarder != nil {
			rt.logger.Info("Standby forwarding PushNodeSettingsUpdate to leader", "leaderID", leaderID, "nodeID", nodeID)
			forwardCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return rt.forwarder.ForwardClusterEvent(forwardCtx, leaderID, &pb.ForwardClusterEventRequest{
				SourceNodeId: rt.nodeID,
				EventId:      crypto.GenRandomId(24),
				ExpiresAt:    time.Now().Add(ClusterEventDefaultTTL).Unix(),
				TargetType:   "broadcast",
				EventName:    "cluster:node_overrides_updated",
				PayloadJson:  nodeID,
			})
		}
		return nil
	}

	if _, ok := rt.registry.GetConnection(nodeID); !ok {
		return nil
	}
	if err := rt.registry.SendCommand(nodeID, p.updateCommand(ctx, nodeID)); err != nil {
		rt.logger.Warn("Failed to push updated settings to node", "nodeID", nodeID, "error", err)
		return err
	}
	rt.logger.Info("Pushed updated settings to node", "nodeID", nodeID)
	return nil
}

// agentSettingsFor 是 Server 侧的 nil-safe 读入口：零值 Server（如构造期单测）
// 没有 pusher 时返回空配置，生产路径由 NewServer 保证 pusher 非 nil。
func (s *Server) agentSettingsFor(ctx context.Context, nodeID string) *pb.AgentSettings {
	if s.deps.SettingsPusher == nil {
		return &pb.AgentSettings{}
	}
	return s.deps.SettingsPusher.agentSettings(ctx, nodeID)
}

func (p *SettingsPusher) updateCommand(ctx context.Context, nodeID string) *pb.ServerCommand {
	return &pb.ServerCommand{
		Payload: &pb.ServerCommand_UpdateSettingsCommand{
			UpdateSettingsCommand: &pb.UpdateSettingsCommand{
				AgentSettings: p.agentSettings(ctx, nodeID),
			},
		},
	}
}

func (p *SettingsPusher) agentSettings(ctx context.Context, nodeID string) *pb.AgentSettings {
	if p.provider != nil {
		settings, err := p.provider.GetAgentSettingsForNode(ctx, nodeID)
		if err == nil && settings != nil {
			return settings
		}
	}
	// 未注入 provider 时返回空配置（如基础单测）。
	return &pb.AgentSettings{}
}
