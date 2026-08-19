// Package control implements the server-side control plane for cluster communication.
// Agents actively connect to this server using the cluster execution.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/control/leader"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/common/host"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
)

// Server configuration constants
const (
	defaultKeepaliveTime    = 30 * time.Second
	defaultKeepaliveTimeout = 5 * time.Second
	defaultKeepaliveMinTime = 10 * time.Second

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

type standbyError struct {
	leaderID string
	message  string
}

func (e standbyError) Error() string {
	if e.message != "" {
		return e.message
	}
	if e.leaderID != "" {
		return leader.StandbyMessage + "; leader=" + e.leaderID
	}
	return leader.StandbyMessage
}

// EventHandler defines an interface for processing connection-related events from agents.
type EventHandler interface {
	SyncVNCConnections(ctx context.Context, nodeID string, connections []contract.ConnectionInfo) error
	SyncStaticRDPConnection(ctx context.Context, nodeID, peerAddr string, rdp *pb.RDPStatus) error
}

type Server struct {
	pb.UnimplementedControlServiceServer

	ctx              context.Context
	cancel           context.CancelFunc
	registry         *AgentRegistry
	grpcServer       *grpc.Server
	logger           *slog.Logger
	connHandler      EventHandler
	dispatcher       *Dispatcher
	forwarder        *ForwardClient
	leader           leader.Reader
	nodes            *db.NodeRepo
	leases           *db.TaskLeaseRepo
	executions       *db.ExecutionRepo
	clusterCfg       config.Cluster
	certs            Certs
	nodeID           string
	settingsProvider contract.AgentSettingsProvider
	alertEvaluator   contract.AlertEvaluator
	publisher        contract.ClusterEventPublisher
	storage          *db.StorageRepo

	// Certificate lifecycle engine (inspection + auto-renewal). Hooks are wired
	// by the assembly layer; nil disables the feature.
	certHooks   CertRenewalHooks
	renewMu     sync.Mutex
	renewStates map[string]*certRenewState
	caWarnedAt  atomic.Int64 // unix seconds of the last CA expiry warning
}

// SetSettingsProvider sets the AgentSettingsProvider for retrieving runtime cluster settings.
func (s *Server) SetSettingsProvider(provider contract.AgentSettingsProvider) {
	s.settingsProvider = provider
}

// SetAlertEvaluator sets the centralized alert evaluator (injected from the service layer).
func (s *Server) SetAlertEvaluator(evaluator contract.AlertEvaluator) {
	s.alertEvaluator = evaluator
}

// SetPublisher sets the push publisher for emitting cluster node notifications.
func (s *Server) SetPublisher(pub contract.ClusterEventPublisher) {
	s.publisher = pub
}

// NewControlPlane creates the cluster gRPC server, forward client, and dispatch service,
// then wires them together. It returns the fully initialized server control plane.
func NewControlPlane(clusterCfg config.Cluster, certs Certs, nodeID string, leaderState leader.Reader, logger *slog.Logger, repos *db.Repos) (*Server, error) {
	controlSrv, err := NewServer(clusterCfg, certs, nodeID, leaderState, logger, repos)
	if err != nil {
		return nil, fmt.Errorf("failed to create control server: %w", err)
	}

	forwardClient := newForwardClient(clusterCfg.Port, certs, clusterCfg.TLSServerName, nodeID, logger)

	dispatcher := newDispatcher(
		controlSrv.Registry(),
		leaderState,
		logger,
		forwardClient,
		nodeID,
		repos.Node,
		repos.Execution,
	)
	controlSrv.SetDispatcher(dispatcher)
	controlSrv.forwarder = forwardClient

	return controlSrv, nil
}

func NewServer(clusterCfg config.Cluster, certs Certs, nodeID string, leaderState leader.Reader, logger *slog.Logger, repos *db.Repos) (*Server, error) {
	clusterLog := logger.With("component", "control")

	var opts []grpc.ServerOption

	// Keepalive is critical for detecting dead connections.
	kaServerParams := keepalive.ServerParameters{
		Time:    defaultKeepaliveTime,
		Timeout: defaultKeepaliveTimeout,
	}

	kaEnforcementPolicy := keepalive.EnforcementPolicy{
		MinTime:             defaultKeepaliveMinTime,
		PermitWithoutStream: true,
	}

	opts = append(opts,
		grpc.KeepaliveParams(kaServerParams),
		grpc.KeepaliveEnforcementPolicy(kaEnforcementPolicy),
	)

	creds, err := buildServerTLSCredentials(certs)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		grpc.Creds(creds),
		grpc.ChainUnaryInterceptor(
			recoveryUnaryServerInterceptor(clusterLog),
			domainErrorUnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(recoveryStreamServerInterceptor(clusterLog)),
	)

	grpcServer := grpc.NewServer(opts...)
	registry := newAgentRegistry(clusterLog)

	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{
		ctx:         ctx,
		cancel:      cancel,
		registry:    registry,
		grpcServer:  grpcServer,
		logger:      clusterLog,
		leader:      leaderState,
		nodes:       repos.Node,
		leases:      repos.TaskLease,
		executions:  repos.Execution,
		clusterCfg:  clusterCfg,
		certs:       certs,
		nodeID:      nodeID,
		storage:     repos.Storage,
		renewStates: make(map[string]*certRenewState),
	}
	registry.SetDisconnectHandler(func(nodeID string) {
		ctx, cancel := context.WithTimeout(server.ctx, nodeStatusUpdateTimeout)
		defer cancel()
		server.markNodeDisconnected(ctx, nodeID)
	})

	pb.RegisterControlServiceServer(grpcServer, server)

	return server, nil
}

// SetControlEventHandler wires the handler used to process agent-reported connection events.
func (s *Server) SetControlEventHandler(handler EventHandler) {
	s.connHandler = handler
}

func (s *Server) SetDispatcher(dispatcher *Dispatcher) {
	s.dispatcher = dispatcher
}

// Executor returns the configured execution.Executor (the dispatch service).
func (s *Server) Executor() execution.Executor {
	return s.dispatcher
}

func (s *Server) Start(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Reset online nodes → offline. Agents transition back to online on reconnect.
	ctx := context.Background()
	if affected, err := s.nodes.ResetOnlineToOffline(ctx); err != nil {
		s.logger.Warn("Failed to reset node status on startup", "error", err)
	} else if affected > 0 {
		s.logger.Info("Reset stale node status on startup", "affected", affected)
	}

	// Each control node registers its own certificate fingerprint so its local
	// agent can pass gRPC identity validation without the OTP bootstrap flow.
	// (server and local agent share the same cert file, so fingerprints match.)
	fingerprint, fpErr := LocalCertFingerprint(s.certs)
	if fpErr != nil {
		s.logger.Warn("Failed to compute local cert fingerprint for self-registration", "error", fpErr)
	}
	if err := s.nodes.UpsertSelf(ctx, s.nodeID, fingerprint); err != nil {
		s.logger.Warn("Failed to register self in nodes table", "error", err)
	} else {
		s.logger.Info("Registered self in nodes table", "node_id", s.nodeID, "is_server", true, "has_fingerprint", fingerprint != "")
	}

	if err := s.nodes.MarkOnline(ctx, s.nodeID, s.nodeID); err != nil {
		s.logger.Warn("Failed to mark control node online on startup", "error", err)
	} else {
		s.logger.Info("Control node marked online", "node_id", s.nodeID)
	}

	s.logger.Info("Starting Cluster gRPC server", "address", address)

	s.registry.wg.Add(1)
	go s.registry.StartPendingRequestCleanup(pendingCleanupInterval, pendingRequestTTL)

	s.registry.wg.Add(1)
	go s.registry.StartHealthChecker(healthCheckInterval, healthCheckPingInterval, healthCheckStaleThreshold)
	s.registry.wg.Add(1)
	go s.startNodeStatusReconciler()

	go func() {
		if err := s.grpcServer.Serve(listener); err != nil {
			s.logger.Error("Cluster gRPC server failed", "error", err)
		}
	}()

	return nil
}

// Stop gracefully stops the server, notifying all connected agents
func (s *Server) Stop() {
	s.logger.Info("Initiating graceful shutdown of Cluster gRPC server")

	// Cancel root context first so disconnect handlers use a cancelled parent.
	s.cancel()

	// Stop registry background goroutines and wait for them to finish
	s.registry.Stop()

	if s.forwarder != nil {
		s.forwarder.Close()
	}

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
}

func (s *Server) Registry() *AgentRegistry {
	return s.registry
}

func (s *Server) broadcastCollect(ctx context.Context, name string, cmd *pb.ServerCommand) error {
	connectedAgents := s.registry.GetConnectedAgents()
	if len(connectedAgents) == 0 {
		// No agents online is a normal operational state (e.g. all nodes shut down).
		// Treat as a no-op rather than an error so the API returns 202 instead of 500.
		s.logger.Warn("collect skipped: no connected agents",
			"target", name)
		return nil
	}

	var errs []error
	for _, nodeID := range connectedAgents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.registry.SendCommand(nodeID, cmd); err != nil {
			s.logger.Warn("failed to send collect command",
				"target", name, "nodeID", nodeID, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) TriggerCollect(ctx context.Context, target contract.CollectTarget) error {
	switch target {
	case contract.CollectTargetVNC:
		return s.broadcastProbeCollect(ctx, "vnc-probe")
	case contract.CollectTargetResource:
		return s.broadcastProbeCollect(ctx, "resource-report")
	case contract.CollectTargetStorage:
		return s.broadcastProbeCollect(ctx, "storage-probe")
	default:
		return fmt.Errorf("unknown collect target: %q", target)
	}
}

func (s *Server) broadcastProbeCollect(ctx context.Context, probeName string) error {
	traceID := crypto.GenRandomId(24)
	return s.broadcastCollect(ctx, probeName, &pb.ServerCommand{
		Payload: &pb.ServerCommand_CollectProbeCommand{
			CollectProbeCommand: &pb.CollectProbeCommand{
				ProbeName:   probeName,
				TraceId:     traceID,
				RequestedAt: time.Now().Unix(),
			},
		},
	})
}

// BroadcastSettingsUpdate broadcasts the latest configuration to all connected agents.
// It recalculates the per-node overrides for each agent dynamically.
func (s *Server) BroadcastSettingsUpdate(ctx context.Context) error {
	connectedAgents := s.registry.GetConnectedAgents()
	if len(connectedAgents) == 0 {
		return nil
	}

	var errs []error
	for _, nodeID := range connectedAgents {
		settings := s.buildAgentSettingsForNode(ctx, nodeID)
		cmd := &pb.ServerCommand{
			Payload: &pb.ServerCommand_UpdateSettingsCommand{
				UpdateSettingsCommand: &pb.UpdateSettingsCommand{
					AgentSettings: settings,
				},
			},
		}
		if err := s.registry.SendCommand(nodeID, cmd); err != nil {
			s.logger.Warn("Failed to send UpdateSettings command", "nodeID", nodeID, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
		} else {
			s.logger.Info("Pushed updated settings to agent", "nodeID", nodeID)
		}
	}
	return errors.Join(errs...)
}

// PushNodeSettingsUpdate pushes the latest effective configuration to a single node.
// Supports multi-control HA topology: if current node is Standby, forwards to Leader.
func (s *Server) PushNodeSettingsUpdate(ctx context.Context, nodeID string) error {
	if s.leader != nil && !s.leader.IsLeader() {
		leaderID := s.leader.LeaderID()
		if leaderID != "" && leaderID != s.nodeID && s.forwarder != nil {
			s.logger.Info("Standby forwarding PushNodeSettingsUpdate to leader", "leaderID", leaderID, "nodeID", nodeID)
			req := &pb.ForwardClusterEventRequest{
				SourceNodeId: s.nodeID,
				TargetType:   "broadcast",
				EventName:    "cluster:node_settings_updated",
				PayloadJson:  nodeID,
			}
			fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return s.forwarder.ForwardClusterEvent(fCtx, leaderID, req)
		}
		return nil
	}

	_, ok := s.registry.GetConnection(nodeID)
	if !ok {
		// Node is not connected, skip silently (agent pulls latest on next register).
		return nil
	}

	settings := s.buildAgentSettingsForNode(ctx, nodeID)
	cmd := &pb.ServerCommand{
		Payload: &pb.ServerCommand_UpdateSettingsCommand{
			UpdateSettingsCommand: &pb.UpdateSettingsCommand{
				AgentSettings: settings,
			},
		},
	}
	if err := s.registry.SendCommand(nodeID, cmd); err != nil {
		s.logger.Warn("Failed to push updated settings to node", "nodeID", nodeID, "error", err)
		return err
	}
	s.logger.Info("Pushed updated settings to node", "nodeID", nodeID)
	return nil
}

func (s *Server) Sync(stream pb.ControlService_SyncServer) error {
	var conn *AgentConnection
	var nodeID string

	// Get peer info for logging
	peerAddr := "unknown"
	if p, ok := peer.FromContext(stream.Context()); ok {
		if p.Addr != nil {
			peerAddr = p.Addr.String()
		}
	}

	defer func() {
		if conn != nil && nodeID != "" {
			s.registry.Unregister(nodeID, conn)
			s.logger.Info("Agent disconnected", "nodeID", nodeID, "peer", peerAddr)
		}
	}()

	// Message handling loop
	for {
		msg, err := stream.Recv()
		if err != nil {
			if conn != nil {
				s.logger.Debug("Stream closed", "error", err, "nodeID", nodeID)
			}
			return err
		}

		// Exit immediately if this connection was superseded by a new registration for the
		// same nodeID. The replaced flag is set by Register() before the redirect is sent.
		if conn != nil && conn.replaced.Load() {
			s.logger.Info("Agent connection replaced by new session, closing old stream", "nodeID", nodeID)
			return fmt.Errorf("agent connection replaced by new session: %s", nodeID)
		}

		if conn != nil && s.leader != nil && !s.leader.IsLeader() {
			s.logger.Warn("Closing agent stream after leader demotion", "nodeID", nodeID, "peer", peerAddr)
			s.sendRedirect(conn, "leader_demoted")
			return fmt.Errorf("server is no longer leader")
		}

		switch payload := msg.Payload.(type) {
		case *pb.AgentMessage_RegisterRequest:
			if conn != nil {
				return fmt.Errorf("agent already registered on this stream: %s", nodeID)
			}
			nodeID = payload.RegisterRequest.NodeId
			conn, err = s.handleRegister(stream, payload.RegisterRequest, peerAddr)
			if err != nil {
				s.logger.Warn("Agent registration failed", "error", err, "nodeID", nodeID)
				registerAck := &pb.RegisterResponse{
					Accepted: false,
					Message:  err.Error(),
				}
				if standbyErr, ok := errors.AsType[standbyError](err); ok {
					registerAck.LeaderNodeId = standbyErr.leaderID
					registerAck.LeaderAddr = s.leaderAddr(standbyErr.leaderID)
				}
				if err := stream.Send(&pb.ServerCommand{
					Payload: &pb.ServerCommand_RegisterResponse{
						RegisterResponse: registerAck,
					},
				}); err != nil {
					s.logger.Debug("Failed to send rejection response", "error", err)
				}
				return err
			}

		case *pb.AgentMessage_HeartbeatRequest:
			s.handleHeartbeat(conn, payload.HeartbeatRequest)

		case *pb.AgentMessage_CommandResult:
			s.handleCommandResult(conn, payload.CommandResult)

		case *pb.AgentMessage_ResourceReport:
			s.handleResourceReport(stream.Context(), conn, payload.ResourceReport)

		case *pb.AgentMessage_ConnectionReport:
			s.handleConnectionReport(stream.Context(), conn, payload.ConnectionReport)

		case *pb.AgentMessage_StorageProbeReport:
			report := payload.StorageProbeReport
			if report == nil {
				continue
			}
			reportedAt := time.Unix(report.GetReportedAt(), 0)
			for _, item := range report.GetItems() {
				if item == nil {
					continue
				}
				dbReport := &model.StorageReport{
					StorageID: item.GetStorageId(), Total: item.GetTotal(),
					Used:       item.GetUsed(),
					ReportedAt: reportedAt, // agent 采集端打点时间
				}
				if err := s.storage.InsertStorageReport(stream.Context(), dbReport); err != nil {
					s.logger.Error("Failed to persist storage probe report", "storageID", item.GetStorageId(), "error", err)
				}
				// 集中式告警引擎评估（共享存储使用率 / 最小可用空间）
				if s.alertEvaluator != nil {
					if err := s.alertEvaluator.EvalStorage(stream.Context(), item); err != nil {
						s.logger.Debug("Alert evaluation skipped for storage probe report", "storageID", item.GetStorageId(), "error", err)
					}
				}
			}

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

func (s *Server) ForwardDispatch(ctx context.Context, req *pb.ForwardDispatchRequest) (*pb.ForwardDispatchResponse, error) {
	if s.dispatcher == nil {
		return nil, fmt.Errorf("dispatch service is not initialized")
	}
	if s.leader != nil && !s.leader.IsLeader() {
		leaderID := s.leader.LeaderID()
		if leaderID != "" {
			return nil, fmt.Errorf("not leader: current leader is %s", leaderID)
		}
		return nil, fmt.Errorf("not leader")
	}

	execReq, err := fromForwardDispatchRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := s.dispatcher.DispatchToAgent(ctx, execReq)
	if err != nil {
		return nil, err
	}

	return toForwardDispatchResponse(resp), nil
}

// Forwarder returns the forward client for communicating with peer control nodes.
func (s *Server) Forwarder() *ForwardClient {
	return s.forwarder
}

func (s *Server) ForwardClusterEvent(ctx context.Context, req *pb.ForwardClusterEventRequest) (*pb.ForwardClusterEventResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("empty cluster event request")
	}

	// 1. 集群内部指令处理：Leader 接收到 Standby 转发的配置变更通知，向 Agent 下推
	if req.EventName == "cluster:node_settings_updated" {
		nodeID := strings.TrimSpace(req.PayloadJson)
		if nodeID != "" {
			common.SafeGo(s.logger, "push-node-settings-update", func() {
				pCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.PushNodeSettingsUpdate(pCtx, nodeID); err != nil {
					s.logger.Warn("Failed to execute forwarded PushNodeSettingsUpdate on leader", "nodeID", nodeID, "error", err)
				}
			})
		}
		return &pb.ForwardClusterEventResponse{Success: true}, nil
	}

	if s.publisher != nil {
		s.publisher.DeliverRemoteEvent(ctx, req.TargetType, req.UserIds, int(req.Role), req.EventName, req.PayloadJson)
	}

	// If current node is Leader, broadcast to all other Standby control nodes (excluding source node)
	if s.leader != nil && s.leader.IsLeader() && s.forwarder != nil {
		for _, srv := range s.clusterCfg.Servers {
			nodeIDs, err := host.ExpandHostPattern(srv)
			if err != nil {
				continue
			}
			for _, nid := range nodeIDs {
				if nid == s.nodeID || nid == req.SourceNodeId {
					continue
				}
				targetID := nid
				common.SafeGo(s.logger, "forward-cluster-event", func() {
					fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = s.forwarder.ForwardClusterEvent(fCtx, targetID, req)
				})
			}
		}
	}

	return &pb.ForwardClusterEventResponse{Success: true}, nil
}

func (s *Server) handleRegister(stream pb.ControlService_SyncServer, req *pb.RegisterRequest, peerAddr string) (*AgentConnection, error) {
	s.logger.Info("Agent registration request",
		"nodeID", req.NodeId,
		"version", req.Version,
		"peer", peerAddr)

	if s.leader != nil && !s.leader.IsLeader() {
		leaderID := s.leader.LeaderID()
		if leaderID != "" {
			return nil, standbyError{leaderID: leaderID}
		}
		return nil, standbyError{}
	}

	// Validate agent identity (token or certificate fingerprint)
	if err := s.validateAgentIdentity(stream.Context(), req.NodeId); err != nil {
		return nil, fmt.Errorf("identity validation failed: %w", err)
	}

	// Register the connection (this will kick old connection if exists)
	conn := s.registry.Register(req.NodeId, stream, req.SystemInfo, peerAddr)

	// Mark node as online in database
	s.markNodeOnline(stream.Context(), req.NodeId)

	// Record the agent-reported OS so the Portal can tell which actions this node supports.
	if req.SystemInfo != nil && req.SystemInfo.Os != "" {
		if err := s.nodes.SetNodeOS(stream.Context(), req.NodeId, req.SystemInfo.Os); err != nil {
			s.logger.Warn("Failed to record node OS", "nodeID", req.NodeId, "error", err)
		}
	}

	// For Windows nodes, ensure the static shared RDP connection record exists.
	// Its state stays unknown until the first heartbeat carries a probe result.
	if req.SystemInfo != nil && req.SystemInfo.Os == "windows" && s.connHandler != nil {
		if err := s.connHandler.SyncStaticRDPConnection(stream.Context(), req.NodeId, peerAddr, nil); err != nil {
			s.logger.Warn("Failed to ensure static RDP connection for Windows node", "nodeID", req.NodeId, "error", err)
		}
	}

	// Build agent settings
	agentSettings := s.buildAgentSettingsForNode(stream.Context(), req.NodeId)

	// Send acceptance response
	if err := conn.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_RegisterResponse{
			RegisterResponse: &pb.RegisterResponse{
				Accepted:      true,
				Message:       "Welcome to the cluster",
				AgentSettings: agentSettings,
			},
		},
	}); err != nil {
		s.registry.Unregister(req.NodeId, conn)
		return nil, fmt.Errorf("failed to send register ack: %w", err)
	}

	s.logger.Info("Agent registered successfully", "nodeID", req.NodeId, "peer", peerAddr)
	return conn, nil
}

func (s *Server) buildAgentSettingsForNode(ctx context.Context, nodeID string) *pb.AgentSettings {
	if s.settingsProvider != nil {
		settings, err := s.settingsProvider.GetAgentSettingsForNode(ctx, nodeID)
		if err == nil && settings != nil {
			return settings
		}
	}
	// Fallback when no provider is injected (e.g. basic unit tests)
	return &pb.AgentSettings{}
}

func (s *Server) sendRedirect(conn *AgentConnection, reason string) {
	if conn == nil {
		return
	}
	leaderID := ""
	if s.leader != nil {
		leaderID = s.leader.LeaderID()
	}
	_ = conn.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_RedirectCommand{
			RedirectCommand: &pb.RedirectCommand{
				LeaderNodeId: leaderID,
				LeaderAddr:   s.leaderAddr(leaderID),
				Reason:       reason,
			},
		},
	})
}

func (s *Server) leaderAddr(leaderID string) string {
	if leaderID == "" {
		return ""
	}
	return net.JoinHostPort(leaderID, fmt.Sprint(s.clusterCfg.Port))
}

func (s *Server) handleHeartbeat(conn *AgentConnection, req *pb.HeartbeatRequest) {
	if conn == nil {
		return
	}

	now := time.Now()
	conn.mu.Lock()
	conn.LastSeen = now
	if req != nil && req.Rdp != nil {
		conn.RDPStatus = req.Rdp
	}
	conn.mu.Unlock()

	if err := s.nodes.TouchHeartbeat(conn.StreamContext(), conn.NodeID, s.nodeID); err != nil {
		s.logger.Warn("Failed to update node heartbeat in DB", "nodeID", conn.NodeID, "error", err)
	} else {
		s.logger.Debug("Heartbeat processed", "nodeID", conn.NodeID)
	}

	// For Windows nodes, synchronize static shared RDP connection readiness status.
	// A heartbeat without RDP status (Linux agents, or an indeterminate Windows probe)
	// leaves the stored state untouched.
	if req != nil && req.Rdp != nil && s.connHandler != nil {
		if err := s.connHandler.SyncStaticRDPConnection(conn.StreamContext(), conn.NodeID, conn.PeerAddr, req.Rdp); err != nil {
			s.logger.Warn("Failed to sync static RDP connection on heartbeat", "nodeID", conn.NodeID, "error", err)
		}
	}

	// Send heartbeat response
	if err := conn.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_HeartbeatResponse{
			HeartbeatResponse: &pb.HeartbeatResponse{
				ServerTimestamp: now.Unix(),
			},
		},
	}); err != nil {
		s.logger.Debug("Failed to send heartbeat ack", "error", err)
	}
}

// handleCommandResult processes agent command results returned to the control plane.
func (s *Server) handleCommandResult(conn *AgentConnection, result *pb.CommandResult) {
	if conn == nil {
		s.logger.Warn("Received command result before agent registration")
		return
	}
	if result == nil {
		s.logger.Warn("Received nil command result", "nodeID", conn.NodeID)
		return
	}
	if result.TraceId == "" {
		s.logger.Warn("Received command result without trace_id", "nodeID", conn.NodeID)
		return
	}

	s.logger.Debug("Received command result",
		"trace_id", result.TraceId,
		"success", result.Success,
		"exit_code", result.ExitCode)

	// Wake synchronous dispatch waiters before touching the execution store.
	// Sync requests do not have DB execution records, and a locked/slow DB must
	// not push an already-returned agent result past the caller's wait timeout.
	s.registry.DeliverResult(result.TraceId, result)

	// Use a server-scoped timeout context rather than the stream context so that
	// a disconnecting stream does not cancel the DB write and leave the execution
	// record permanently stuck in a running state.
	dbCtx, cancel := context.WithTimeout(s.ctx, nodeStatusUpdateTimeout)
	defer cancel()

	// Persist result to database — move status computation from DB layer to caller
	status := model.ExecutionStatusFailed
	switch result.SystemError {
	case "cancelled":
		status = model.ExecutionStatusCancelled
	case "timeout":
		status = model.ExecutionStatusTimedOut
	default:
		if result.Success {
			status = model.ExecutionStatusSucceeded
		}
	}
	persisted, err := s.executions.MarkTerminal(dbCtx, result.TraceId, status, result.Success, int(result.ExitCode), result.SystemError)
	if err != nil {
		s.logger.Error("Failed to persist command result", "trace_id", result.TraceId, "error", err)
	} else if !persisted {
		s.logger.Debug("Command result not persisted because no async-tracked execution record exists", "trace_id", result.TraceId)
	}

	// Send ACK back to agent
	ack := &pb.ServerCommand{
		Payload: &pb.ServerCommand_CommandResultAck{
			CommandResultAck: &pb.CommandResultAck{
				TraceId:   result.TraceId,
				Persisted: persisted,
			},
		},
	}

	if err := conn.Send(ack); err != nil {
		s.logger.Warn("Failed to send result ACK", "trace_id", result.TraceId, "error", err)
	}
}

func (s *Server) handleResourceReport(ctx context.Context, conn *AgentConnection, report *pb.ResourceReport) {
	if conn == nil {
		return
	}

	now := time.Now()
	// Clone before storing: the original `report` continues to be read below
	// by this goroutine only, while the stored clone is independently owned by
	// conn—safe for concurrent proto.Clone() from Snapshot/SnapshotAll in
	// dispatch goroutines. Without this, the shared pointer triggers a race
	// between this goroutine's reads and proto.Clone's internal lazy-cache
	// writes, manifesting as a panic in appendInt64NoZero.
	storedReport := proto.Clone(report).(*pb.ResourceReport)
	conn.mu.Lock()
	conn.LastSeen = now
	conn.ResourceReport = storedReport
	conn.mu.Unlock()

	// Aggregate GPU info
	gpuCount := int64(len(report.GetGpus()))
	var gpuMemTotal, gpuMemUsed int64
	var gpuUtilSum float64

	for _, gpu := range report.GetGpus() {
		gpuMemTotal += gpu.GetMemTotal()
		gpuMemUsed += gpu.GetMemUsed()
		gpuUtilSum += gpu.GetUtilization()
	}

	var gpuUtil float64
	if gpuCount > 0 {
		gpuUtil = gpuUtilSum / float64(gpuCount)
	}

	var diskUsages []model.NodeDiskInfo
	if duList := report.GetDiskUsages(); len(duList) > 0 {
		diskUsages = make([]model.NodeDiskInfo, 0, len(duList))
		for _, du := range duList {
			diskUsages = append(diskUsages, model.NodeDiskInfo{
				MountPoint:  du.GetMountPoint(),
				Filesystem:  du.GetFilesystem(),
				MonitorName: du.GetMonitorName(),
				Total:       du.GetTotal(),
				Used:        du.GetUsed(),
			})
		}
	}

	if err := s.nodes.UpsertResourceReport(ctx, &model.NodeResourceReport{
		NodeID:      conn.NodeID,
		ReportedAt:  time.Unix(report.GetReportedAt(), 0), // agent 采集端打点时间
		Uptime:      report.GetUptime(),
		ActiveTasks: report.GetActiveTasks(),
		CPUCores:    report.GetCpuCores(),
		CPUUtil:     report.GetCpuUtil(),
		MemTotal:    report.GetMemTotal(),
		MemUsed:     report.GetMemUsed(),
		SwapTotal:   report.GetSwapTotal(),
		SwapFree:    report.GetSwapFree(),
		Load1:       report.GetLoad1(),
		Load5:       report.GetLoad5(),
		Load15:      report.GetLoad15(),
		GPUCount:    gpuCount,
		GPUMemTotal: gpuMemTotal,
		GPUMemUsed:  gpuMemUsed,
		GPUUtil:     gpuUtil,
		DiskUsages:  diskUsages,
	}); err != nil {
		s.logger.Warn("Failed to persist node resource report", "nodeID", conn.NodeID, "error", err)
	}

	// 集中式告警引擎评估：采集入库只负责数据，阈值判断统一收敛到 alert.Evaluator
	// （阈值来自 DomainAlert 配置，可配置；CPU/Load 默认关闭）。日志与推送单一出口。
	if s.alertEvaluator != nil {
		if err := s.alertEvaluator.EvalResource(ctx, conn.NodeID, report); err != nil {
			s.logger.Debug("Alert evaluation skipped for node resource report", "nodeID", conn.NodeID, "error", err)
		}
	}
}

func toContractConnectionInfo(info *pb.ConnectionInfo) contract.ConnectionInfo {
	if info == nil {
		return contract.ConnectionInfo{}
	}
	return contract.ConnectionInfo{
		ConnectionID: uint(info.GetConnectionId()),
		Name:         info.GetName(),
		Username:     info.GetUsername(),
		Host:         info.GetHost(),
		Port:         uint(info.GetPort()),
		Display:      info.GetDisplay(),
		Protocol:     contract.Protocol(info.GetProtocol()),
		PID:          uint(info.GetPid()),
	}
}

func (s *Server) handleConnectionReport(ctx context.Context, conn *AgentConnection, report *pb.ConnectionReport) {
	if conn == nil || report == nil || s.connHandler == nil {
		return
	}

	connections := make([]contract.ConnectionInfo, 0, len(report.GetConnections()))
	for _, connInfo := range report.GetConnections() {
		if connInfo == nil {
			continue
		}
		connections = append(connections, toContractConnectionInfo(connInfo))
	}

	if err := s.connHandler.SyncVNCConnections(ctx, conn.NodeID, connections); err != nil {
		s.logger.Error("Failed to sync VNC connection report",
			"nodeID", conn.NodeID,
			"reported_at", report.GetReportedAt(),
			"connections", len(connections),
			"error", err)
		return
	}

	s.logger.Debug("Processed VNC connection report",
		"nodeID", conn.NodeID,
		"reported_at", report.GetReportedAt(),
		"connections", len(connections))
}

// validateAgentIdentity validates agent using certificate fingerprint
// Security model:
// - Connection allowed ONLY if certificate fingerprint matches stored value in registry
// - Registration must be done via REST Http before connection
func (s *Server) validateAgentIdentity(ctx context.Context, nodeID string) error {

	node, err := s.nodes.GetByNodeID(ctx, nodeID)
	if err != nil {
		s.logger.Warn("Agent identity validation failed: node not found",
			"node_id", nodeID,
			"error", err)
		return fmt.Errorf("node not found in registry: %s", nodeID)
	}

	// Strict check: Node MUST be registered with a fingerprint
	if node.CertFingerprint == "" {
		s.logger.Warn("Agent identity validation failed: node has no registered certificate",
			"node_id", nodeID)
		return fmt.Errorf("node not registered (no fingerprint): %s. Please bootstrap or provision the node certificate first", nodeID)
	}

	// Extract peer certificate metadata (fingerprint + bookkeeping fields)
	peerInfo, certErr := extractPeerCertInfo(ctx)
	if certErr != nil {
		s.logger.Warn("Agent identity validation failed: cannot extract peer certificate",
			"node_id", nodeID,
			"error", certErr)
		return fmt.Errorf("certificate required for verification: %w", certErr)
	}

	// Verify fingerprint matches current OR previous (dual fingerprint): the prev
	// slot covers the renewal switch window and .prev rollback. Fingerprints are
	// FAIL-CLOSED (D11): never self-healed, never empty-bootstrapped.
	if peerInfo.Fingerprint != node.CertFingerprint && peerInfo.Fingerprint != node.CertFingerprintPrev {
		expected := node.CertFingerprint
		if len(expected) > 16 {
			expected = expected[:16] + "..."
		}
		got := peerInfo.Fingerprint
		if len(got) > 16 {
			got = got[:16] + "..."
		}
		s.logger.Warn("Agent identity validation failed: certificate fingerprint mismatch",
			"node_id", nodeID,
			"expected", expected,
			"got", got)
		return fmt.Errorf("certificate fingerprint mismatch for node: %s", nodeID)
	}

	// Metadata self-heal: on a successfully authenticated handshake, refresh the
	// bookkeeping fields (subject/expiry) when they drifted. Best-effort only:
	// it must never influence the connection outcome.
	if peerInfo.Subject != node.CertSubject || !peerInfo.NotAfter.Equal(node.CertExpiresAt) {
		if err := s.nodes.UpdateCertMetadata(ctx, nodeID, peerInfo.Subject, peerInfo.NotAfter); err != nil {
			s.logger.Warn("Failed to self-heal certificate metadata", "node_id", nodeID, "error", err)
		} else {
			s.logger.Info("Certificate metadata corrected from live handshake", "node_id", nodeID)
		}
	}

	s.logger.Debug("Certificate fingerprint verified", "nodeID", nodeID)
	return nil
}

// markNodeOnline updates the node status in database and notifies system admins to refresh UI
func (s *Server) markNodeOnline(ctx context.Context, nodeID string) {
	if err := s.nodes.MarkOnline(ctx, nodeID, s.nodeID); err != nil {
		s.logger.Warn("Failed to mark node online", "error", err, "nodeID", nodeID)
	} else {
		s.logger.Debug("Node marked online in DB", "nodeID", nodeID, "server_id", s.nodeID)
		if s.publisher != nil {
			s.publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "refresh", "", "", "")
		}
	}
}

func (s *Server) markNodeDisconnected(ctx context.Context, nodeID string) {
	if err := s.nodes.MarkDisconnected(ctx, nodeID, s.nodeID); err != nil {
		s.logger.Warn("Failed to mark node disconnected", "nodeID", nodeID, "error", err)
	} else {
		s.logger.Debug("Node marked disconnected in DB", "nodeID", nodeID, "server_id", s.nodeID)
		if s.publisher != nil {
			s.publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "notify", "节点离线通知", fmt.Sprintf("节点 %s 已下线断开", nodeID), "warning")
		}
	}
}

func (s *Server) startNodeStatusReconciler() {
	defer s.registry.wg.Done()
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("panic recovered in startNodeStatusReconciler",
				"panic", rec,
				"stack", string(debug.Stack()),
			)
		}
	}()
	checkInterval := time.Duration(s.clusterCfg.NodeCheckInterval) * time.Second
	heartbeatTimeout := time.Duration(s.clusterCfg.NodeHeartbeatTimeout) * time.Second

	s.logger.Info("Starting node status checker",
		"check_interval", checkInterval,
		"heartbeat_timeout", heartbeatTimeout)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						s.logger.Error("panic recovered in node status reconciler tick",
							"panic", rec,
							"stack", string(debug.Stack()),
						)
					}
				}()

				// Self section (D13): every control node checks its own certificate
				// row regardless of leadership — only the host itself can re-sign
				// its own certificate (private key and paths are node-local).
				selfCtx, selfCancel := context.WithTimeout(context.Background(), nodeStatusUpdateTimeout)
				s.checkSelfCert(selfCtx)
				// Revocation section: drop live sessions whose node certificate was
				// revoked (fingerprint cleared), so revocation takes effect on the
				// connected session instead of waiting for a natural reconnect.
				// Not leader-gated: each node only drops the sessions it holds.
				s.dropRevokedSessions(selfCtx)
				selfCancel()

				// Only run cluster-wide checks if this node is the leader
				if s.leader != nil && !s.leader.IsLeader() {
					return
				}

				// Acquire lease to ensure only one node checks (defense in depth)
				ctx, cancel := context.WithTimeout(context.Background(), nodeStatusUpdateTimeout)
				defer cancel()

				acquired, err := s.leases.TryAcquire(ctx, "cluster_node_status_check", s.nodeID, time.Minute)
				if err != nil {
					s.logger.Warn("Failed to acquire cluster_node_status_check lease", "error", err)
					return
				}
				if !acquired {
					return
				}

				staledNodes, err := s.nodes.MarkStaleOffline(ctx, time.Now().Add(-heartbeatTimeout))
				if err != nil {
					s.logger.Warn("Failed to check node status", "error", err)
				} else if len(staledNodes) > 0 && s.publisher != nil {
					for _, nodeID := range staledNodes {
						s.publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "notify", "节点心跳超时通知", fmt.Sprintf("节点 %s 心跳超时，已标记为下线", nodeID), "warning")
					}
				}

				// Certificate lifecycle sweep (DB-first alerts + agent renewal triggers).
				s.runCertInspection(ctx)
			}()
		case <-s.registry.stopCh:
			return
		}
	}
}
