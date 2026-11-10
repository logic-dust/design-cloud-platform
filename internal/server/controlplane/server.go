// Package controlplane implements the server-side control plane for cluster communication.
// Agents actively connect to this server using the cluster execution.
package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// Server configuration constants
const (
	pendingCleanupInterval = 5 * time.Minute
	pendingRequestTTL      = 10 * time.Minute

	// 连接层健康检查（内存连接看门狗，激进）：比 DB 层 reconciler 的
	// NodeHeartbeatTimeout（180s，保守）更早摘除失联连接——连接摘除要快
	// （gRPC keepalive 兜底传输层），DB 状态更新要稳（防误判离线写库）。
	// 两轨参数不同是设计意图，勿合并。
	healthCheckInterval       = 30 * time.Second
	healthCheckPingInterval   = 60 * time.Second
	healthCheckStaleThreshold = 90 * time.Second

	gracefulShutdownTimeout = 10 * time.Second

	// Bounds DB status updates even when not tied to a request.
	nodeStatusUpdateTimeout = 5 * time.Second
)

// ConnectionRuntimeHandler defines an interface for processing desktop runtime reports from agents.
type ConnectionRuntimeHandler interface {
	SyncConnectionReport(ctx context.Context, nodeID string, report contract.ConnectionReport) error
}

// Server 是控制面的入站 gRPC 适配：把 Agent 的 Sync 流、节点间转发请求和证书
// 续期请求，翻译成对 Runtime（注册表/派发器/转发客户端）与业务端口（ServiceDeps）
// 的调用。
//
// 所有依赖在构造时固定：Runtime 先于业务层构造，业务端口以构造参数传入。
// 构造完成后不再修改任何依赖——不存在运行期 Set/Wire 回填。
type Server struct {
	pb.UnimplementedControlServiceServer

	*Runtime

	grpcServer          *grpc.Server
	serveDone           chan struct{}
	serveErrors         chan error
	deps                ServiceDeps
	connectionReconcile *connectionReconcileWorker

	// Certificate lifecycle engine (inspection + auto-renewal). Hooks are passed
	// in by the assembly layer; nil disables the feature.
	renewMu     sync.Mutex
	renewStates map[string]*certRenewState
	caWarnedAt  atomic.Int64 // unix seconds of the last CA expiry warning

	// Heartbeat DB flush throttling.
	heartbeatFlushInterval time.Duration
}

// ServiceDeps 汇总控制面对业务层的窄契约依赖。
//
// control 与 service 互需对方能力（services 需 Executor/Registry/Pusher，
// control 需 Settings/Alert/Push/EventHandler/CertHooks），构成真实构造环。
// arch-rules 禁止 control import service，故这些端口由总装层（package server）
// 在构造 Server 时作为参数传入：构造顺序固定为 Runtime → service/application →
// Server，因此不需要运行期回填。
type ServiceDeps struct {
	SettingsProvider        contract.AgentSettingsProvider
	AlertEvaluator          contract.AlertEvaluator
	Publisher               contract.ClusterEventPublisher
	ConnectionReportHandler ConnectionRuntimeHandler
	// SettingsPusher 承担向 Agent 下发配置的写侧（读侧是 SettingsProvider）。
	// 它在 service 构造期即存在，因此不构成 control↔service 构造环。
	SettingsPusher *SettingsPusher
	// CertHooks 为 nil 时合法：禁用证书巡检与续期。
	CertHooks CertRenewalHooks
}

// NewServer creates the inbound gRPC adapter on top of an already-built Runtime.
//
// 必需的业务端口在此一次性校验：装配错误必须在启动期 panic，而不是运行期
// nil 解引用。可选端口（CertHooks）为 nil 表示关闭该特性。
func NewServer(rt *Runtime, deps ServiceDeps) (*Server, error) {
	if rt == nil {
		return nil, fmt.Errorf("failed to create control server: runtime is required")
	}
	if deps.SettingsProvider == nil || deps.AlertEvaluator == nil ||
		deps.Publisher == nil || deps.ConnectionReportHandler == nil || deps.SettingsPusher == nil {
		panic("control.NewServer: missing required service dependency (settings/alert/publisher/desktop-handler/settings-pusher)")
	}

	// 步骤 1：创建带 keepalive、mTLS 与统一错误处理的 gRPC Server
	grpcServer, err := newGRPCServer(rt.certs, rt.logger)
	if err != nil {
		return nil, err
	}

	// 步骤 2：组装入站适配对象
	server := &Server{
		Runtime:     rt,
		grpcServer:  grpcServer,
		deps:        deps,
		renewStates: make(map[string]*certRenewState),
	}
	server.connectionReconcile = newConnectionReconcileWorker(rt.ctx, deps.ConnectionReportHandler, rt.logger)

	// 步骤 3：断连语义是入站适配的业务决策（落库 + 集群通知），由 Server 设置；
	// Runtime 只负责连接生命周期本身。
	rt.registry.SetDisconnectHandler(func(nodeID string) {
		ctx, cancel := context.WithTimeout(rt.ctx, nodeStatusUpdateTimeout)
		defer cancel()
		server.markNodeDisconnected(ctx, nodeID)
	})

	// 步骤 4：注册 ControlService
	pb.RegisterControlServiceServer(grpcServer, server)
	return server, nil
}

func (s *Server) Start(address string) (<-chan error, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}
	ctx := context.Background()

	// 步骤 1：清理旧在线状态，登记本机身份并标记控制节点 online
	s.initializeLocalNode(ctx)

	// 步骤 2：启动内核循环（挂起请求清理、连接健康检查）与业务循环（节点状态调和）
	s.logger.Info("Starting Cluster gRPC server", "address", address)
	s.Runtime.Start()
	s.registry.StartWorker(s.startNodeStatusReconciler)
	s.StartExecutionPruneLoop()

	// 步骤 3：对外提供 gRPC 服务
	s.serveDone = make(chan struct{})
	s.serveErrors = make(chan error, 1)
	go func() {
		defer close(s.serveDone)
		defer close(s.serveErrors)
		if err := s.grpcServer.Serve(listener); err != nil {
			if !errors.Is(err, grpc.ErrServerStopped) {
				s.logger.Error("Cluster gRPC server failed", "error", err)
				s.serveErrors <- err
			}
		}
	}()
	return s.serveErrors, nil
}

// Stop gracefully stops the server, notifying all connected agents
func (s *Server) Stop() {
	s.logger.Info("Initiating graceful shutdown of Cluster gRPC server")

	// 步骤 0：优雅停止前，将当前已连接节点的最新心跳刷新入库
	s.flushConnectedHeartbeats()

	// 步骤 1：取消根 context，断连回调不再使用有效父 context
	s.cancel()
	if s.connectionReconcile != nil {
		select {
		case <-s.connectionReconcile.done:
		case <-time.After(connectionReconcileShutdownTimeout):
			s.logger.Warn("Desktop reconcile worker did not finish before shutdown timeout")
		}
	}

	// 步骤 2：停止注册表后台协程并关闭转发客户端（cancel 幂等）
	s.Runtime.Stop()

	// 步骤 3：优雅停止 gRPC，超时则强制 Stop
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		s.logger.Info("Control gRPC server stopped gracefully")
	case <-time.After(gracefulShutdownTimeout):
		s.logger.Warn("Graceful shutdown timed out, forcing stop")
		s.grpcServer.Stop()
	}
	if s.serveDone != nil {
		<-s.serveDone
	}
}

const defaultHeartbeatFlushInterval = 45 * time.Second

func (s *Server) heartbeatFlushThreshold() time.Duration {
	if s.heartbeatFlushInterval > 0 {
		return s.heartbeatFlushInterval
	}
	timeout := time.Duration(s.clusterCfg.NodeHeartbeatTimeout) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	maxInterval := max(timeout/3, 10*time.Second)
	return min(defaultHeartbeatFlushInterval, maxInterval)
}

// SetHeartbeatFlushIntervalForTest overrides the heartbeat flush threshold for testing.
func (s *Server) SetHeartbeatFlushIntervalForTest(d time.Duration) {
	s.heartbeatFlushInterval = d
}

func (s *Server) flushConnectedHeartbeats() {
	if s.nodes == nil || s.registry == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), nodeStatusUpdateTimeout)
	defer cancel()
	now := time.Now()
	for _, nodeID := range s.registry.ConnectedNodeIDs() {
		conn, ok := s.registry.GetConnection(nodeID)
		if !ok || conn == nil {
			continue
		}
		if err := s.nodes.TouchHeartbeat(ctx, nodeID); err != nil {
			s.logger.Warn("Failed to flush node heartbeat on shutdown", "nodeID", nodeID, "error", err)
		} else {
			conn.MarkHeartbeatFlushed(now)
		}
	}
}

// Sync 是 Agent 长连接主循环：注册一次后分流处理各类入站消息。
func (s *Server) Sync(stream pb.ControlService_SyncServer) error {
	var conn *agentregistry.Connection
	var nodeID string

	peerAddr := "unknown"
	if p, ok := peer.FromContext(stream.Context()); ok && p.Addr != nil {
		peerAddr = p.Addr.String()
	}

	defer func() {
		if conn != nil && nodeID != "" {
			s.registry.Unregister(nodeID, conn)
			s.logger.Info("Agent disconnected", "nodeID", nodeID, "peer", peerAddr)
		}
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if conn != nil {
				s.logger.Debug("Stream closed", "error", err, "nodeID", nodeID)
			}
			return err
		}

		// 步骤 1：本连接已被同 nodeID 新会话替换则立即退出
		if conn != nil && conn.IsReplaced() {
			s.logger.Info("Agent connection replaced by new session, closing old stream", "nodeID", nodeID)
			return fmt.Errorf("agent connection replaced by new session: %s", nodeID)
		}

		// 步骤 2：Leader 降级后通知 Agent 重定向并关闭流
		if conn != nil && s.leader != nil && !s.leader.IsLeader() {
			s.logger.Warn("Closing agent stream after leader demotion", "nodeID", nodeID, "peer", peerAddr)
			s.sendRedirect(conn, "leader_demoted")
			return fmt.Errorf("server is no longer leader")
		}

		// 步骤 3：按消息类型分发到具体处理函数
		switch payload := msg.Payload.(type) {
		case *pb.AgentMessage_RegisterRequest:
			if conn != nil {
				return fmt.Errorf("agent already registered on this stream: %s", nodeID)
			}
			nodeID = payload.RegisterRequest.NodeId
			conn, err = s.handleRegister(stream, payload.RegisterRequest, peerAddr)
			if err != nil {
				return err
			}

		case *pb.AgentMessage_HeartbeatRequest:
			s.handleHeartbeat(conn, payload.HeartbeatRequest)

		case *pb.AgentMessage_CommandResult:
			s.handleCommandResult(conn, payload.CommandResult)

		case *pb.AgentMessage_Metrics:
			s.handleMetrics(stream.Context(), conn, payload.Metrics)

		case *pb.AgentMessage_ConnectionReport:
			s.handleConnectionReport(stream.Context(), conn, payload.ConnectionReport)

		case *pb.AgentMessage_StorageProbeReport:
			s.handleStorageProbeReport(stream.Context(), payload.StorageProbeReport)

		case *pb.AgentMessage_RenewCertRequest:
			if conn != nil && nodeID != "" {
				s.handleRenewCertRequest(conn, nodeID, payload.RenewCertRequest)
			}

		case *pb.AgentMessage_RenewCertResult:
			if nodeID != "" {
				s.handleRenewCertResult(nodeID, payload.RenewCertResult)
			}
		}
	}
}
