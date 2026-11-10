package contract

import (
	"context"

	pb "design-cloud-engine/internal/contract/proto"
)

// AlertEvaluator 是集中式告警引擎的抽象接口。
// 控制面（control）仅依赖该契约执行告警评估，具体实现在 service/alert 包，
// 由 service 容器组装后注入——避免控制面反向依赖业务层（见 arch-rules.yaml）。
type AlertEvaluator interface {
	// EvalMetrics 对节点资源指标执行告警评估（内存/磁盘/CPU/负载）。
	EvalMetrics(ctx context.Context, nodeID string, metrics *pb.Metrics) error
	// EvalStorage 对共享存储 probe 上报执行告警评估（使用率/最小可用空间）。
	EvalStorage(ctx context.Context, report *pb.StorageProbeItem) error
}

// CollectTarget defines the type of data collection target.
type CollectTarget string

const (
	CollectTargetConnection CollectTarget = "connection"
	CollectTargetMetrics    CollectTarget = "metrics"
	CollectTargetStorage    CollectTarget = "storage"
)

// CollectTrigger is the unified facade interface for broadcasting on-demand
// collection probes to agents across the cluster control plane.
type CollectTrigger interface {
	TriggerCollect(ctx context.Context, target CollectTarget) error
}

// ClusterEventPublisher defines the notification publisher interface for control plane
// to push node status and lifecycle notifications to system admins without introducing a reverse dependency on the push hub.
type ClusterEventPublisher interface {
	PublishNodeEvent(ctx context.Context, role int, nodeID, action, title, message, level string)
	DeliverRemoteEvent(ctx context.Context, targetType string, userIDs []string, role int, eventName string, payloadJSON string)
}

// AgentSettingsProvider defines the minimal read-only view of node agent settings
// needed by the control plane without introducing a reverse dependency on service/settings.
type AgentSettingsProvider interface {
	GetAgentSettingsForNode(ctx context.Context, nodeID string) (*pb.AgentSettings, error)
	GetStorageProbeInterval(ctx context.Context) int
}

// NodeSettingsPusher defines the control-plane facade for pushing settings updates to an individual node.
type NodeSettingsPusher interface {
	PushNodeSettingsUpdate(ctx context.Context, nodeID string) error
}
