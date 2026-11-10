package controlplane

// Runtime 是集群控制面的常驻内核：Agent 注册表、派发器、跨节点转发客户端，
// 以及它们共同依赖的仓储与集群配置。
//
// 它先于业务层构造，业务层通过 Executor() / Registry() / Forwarder() 消费它；
// 入站 gRPC 适配（Server）则反过来持有 Runtime。这样构造顺序被固定为
//
//	Runtime（无业务依赖） → service/application → Server（入站适配）
//
// 业务端口（Settings/Alert/Publisher/ConnectionReportHandler/CertHooks）以构造
// 参数形式进入 Server，不再存在运行期 Wire 回填。

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/controlplane/dispatch"
	"design-cloud-engine/internal/server/controlplane/leader"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"
)

// Runtime 的字段同时服务于同包的 Server（通过嵌入），因此保持未导出。
type Runtime struct {
	ctx        context.Context
	cancel     context.CancelFunc
	registry   *agentregistry.Registry
	dispatcher *dispatch.Dispatcher
	forwarder  *ForwardClient
	leader     leader.Reader
	nodes      *db.NodeRepo
	leases     *db.TaskLeaseRepo
	executions *db.ExecutionRepo
	storage    *db.StorageRepo
	clusterCfg config.Cluster
	certs      Certs
	nodeID     string
	logger     *slog.Logger
	eventDedup *eventDedup
}

// NewRuntime builds the control-plane kernel without touching business services.
func NewRuntime(clusterCfg config.Cluster, certs Certs, nodeID string, leaderState leader.Reader, logger *slog.Logger, repos *db.Repos) (*Runtime, error) {
	clusterLog := logger.With("component", "control")

	registry := agentregistry.New(clusterLog)
	forwardClient := newForwardClient(clusterCfg.Port, certs, clusterCfg.TLSServerName, nodeID, clusterLog)
	dispatcher := dispatch.New(
		registry,
		leaderState,
		clusterLog,
		forwardClient,
		nodeID,
		repos.Node,
		repos.Execution,
	)

	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		ctx:        ctx,
		cancel:     cancel,
		registry:   registry,
		dispatcher: dispatcher,
		forwarder:  forwardClient,
		leader:     leaderState,
		nodes:      repos.Node,
		leases:     repos.TaskLease,
		executions: repos.Execution,
		storage:    repos.Storage,
		clusterCfg: clusterCfg,
		certs:      certs,
		nodeID:     nodeID,
		logger:     clusterLog,
		eventDedup: newEventDedup(),
	}, nil
}

// Executor returns the configured execution.Executor (the dispatch service).
// 业务层只应通过它派发动作，不得直接接触 Dispatcher。
func (rt *Runtime) Executor() execution.Executor { return rt.dispatcher }

// Forwarder returns the forward client for communicating with peer control nodes.
func (rt *Runtime) Forwarder() *ForwardClient { return rt.forwarder }

// Leader exposes the leader view the runtime was constructed with. It is used by
// the composition root to build the push hub's cluster forwarding callback so the
// hub never has to import the control plane.
func (rt *Runtime) Leader() leader.Reader { return rt.leader }

// Registry exposes the agent registry (connection state queries, worker loops).
func (rt *Runtime) Registry() *agentregistry.Registry { return rt.registry }

// Start launches the kernel's background loops: pending-request cleanup and the
// agent health checker. Business-driven loops (node reconciler, execution prune)
// are started by Server, which owns their dependencies.
func (rt *Runtime) Start() {
	rt.registry.StartPendingRequestCleanup(pendingCleanupInterval, pendingRequestTTL)
	rt.registry.StartHealthChecker(healthCheckInterval, healthCheckPingInterval, healthCheckStaleThreshold)
}

// Stop cancels the kernel context, stops registry loops and closes peer
// connections. The inbound gRPC server is stopped separately by Server.Stop.
func (rt *Runtime) Stop() {
	rt.cancel()
	rt.registry.Stop()
	if rt.forwarder != nil {
		rt.forwarder.Close()
	}
}

// HandleLeaderDemotion 在失去租约时把本机 Agent 全部改指向新 Leader：
// 清空在途派发、下发重定向命令并断开连接。
func (rt *Runtime) HandleLeaderDemotion(newLeaderID string) {
	rt.logger.Warn("Leader demoted, closing all agent streams", "new_leader", newLeaderID)
	rt.registry.FailAllPendingRequests(fmt.Errorf("leader demoted, new leader is %q", newLeaderID))
	for _, nodeID := range rt.registry.ConnectedNodeIDs() {
		conn, ok := rt.registry.GetConnection(nodeID)
		if !ok {
			continue
		}
		rt.sendRedirect(conn, "leader_demoted")
		rt.registry.Disconnect(nodeID)
	}
}

func (rt *Runtime) sendRedirect(conn *agentregistry.Connection, reason string) {
	if conn == nil {
		return
	}
	leaderID := ""
	if rt.leader != nil {
		leaderID = rt.leader.LeaderID()
	}
	_ = conn.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_RedirectCommand{
			RedirectCommand: &pb.RedirectCommand{
				LeaderNodeId: leaderID,
				LeaderAddr:   rt.leaderAddr(leaderID),
				Reason:       reason,
			},
		},
	})
}

func (rt *Runtime) leaderAddr(leaderID string) string {
	if leaderID == "" {
		return ""
	}
	return net.JoinHostPort(leaderID, fmt.Sprint(rt.clusterCfg.Port))
}
