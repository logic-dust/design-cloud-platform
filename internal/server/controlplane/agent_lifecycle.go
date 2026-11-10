package controlplane

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/controlplane/leader"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"
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

func (s *Server) handleRegister(stream pb.ControlService_SyncServer, req *pb.RegisterRequest, peerAddr string) (*agentregistry.Connection, error) {
	conn, err := s.acceptAgent(stream, req, peerAddr)
	if err == nil {
		return conn, nil
	}

	s.logger.Warn("Agent registration failed", "error", err, "nodeID", req.NodeId)
	response := &pb.RegisterResponse{
		Accepted: false,
		Message:  err.Error(),
	}
	if standbyErr, ok := errors.AsType[standbyError](err); ok {
		response.LeaderNodeId = standbyErr.leaderID
		response.LeaderAddr = s.leaderAddr(standbyErr.leaderID)
	}
	if sendErr := stream.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_RegisterResponse{RegisterResponse: response},
	}); sendErr != nil {
		s.logger.Debug("Failed to send rejection response", "error", sendErr)
	}
	return nil, err
}

func (s *Server) acceptAgent(stream pb.ControlService_SyncServer, req *pb.RegisterRequest, peerAddr string) (*agentregistry.Connection, error) {
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

	if err := s.validateAgentIdentity(stream.Context(), req.NodeId); err != nil {
		return nil, fmt.Errorf("identity validation failed: %w", err)
	}

	conn := s.registry.Register(req.NodeId, stream, req.SystemInfo, peerAddr)
	s.markNodeRegister(stream.Context(), req.NodeId, req.SystemInfo)

	agentSettings := s.agentSettingsFor(stream.Context(), req.NodeId)
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

func (s *Server) initializeLocalNode(ctx context.Context) {
	if affected, err := s.nodes.ResetOnlineToOffline(ctx); err != nil {
		s.logger.Warn("Failed to reset node status on startup", "error", err)
	} else if affected > 0 {
		s.logger.Info("Reset stale node status on startup", "affected", affected)
	}

	fingerprint, err := LocalCertFingerprint(s.certs)
	if err != nil {
		s.logger.Warn("Failed to compute local cert fingerprint for self-registration", "error", err)
	}
	if err := s.nodes.UpsertSelf(ctx, s.nodeID, fingerprint); err != nil {
		s.logger.Warn("Failed to register self in nodes table", "error", err)
	} else {
		s.logger.Info("Registered self in nodes table",
			"node_id", s.nodeID,
			"is_server", true,
			"has_fingerprint", fingerprint != "")
	}
	if err := s.nodes.MarkOnline(ctx, s.nodeID); err != nil {
		s.logger.Warn("Failed to mark control node online on startup", "error", err)
	} else {
		s.logger.Info("Control node marked online", "node_id", s.nodeID)
	}
}

// validateAgentIdentity 校验 Agent 证书指纹是否与节点登记一致。
// 仅当指纹匹配（含续签窗口的 prev 槽）时允许接入；登记须先经 REST 完成。
func (s *Server) validateAgentIdentity(ctx context.Context, nodeID string) error {
	node, err := s.nodes.GetByNodeID(ctx, nodeID)
	if err != nil {
		s.logger.Warn("Agent identity validation failed: node not found",
			"node_id", nodeID,
			"error", err)
		return fmt.Errorf("node not found in registry: %s", nodeID)
	}

	if node.CertFingerprint == "" {
		s.logger.Warn("Agent identity validation failed: node has no registered certificate",
			"node_id", nodeID)
		return fmt.Errorf("node not registered (no fingerprint): %s. Please bootstrap or provision the node certificate first", nodeID)
	}

	peerInfo, certErr := extractPeerCertInfo(ctx)
	if certErr != nil {
		s.logger.Warn("Agent identity validation failed: cannot extract peer certificate",
			"node_id", nodeID,
			"error", certErr)
		return fmt.Errorf("certificate required for verification: %w", certErr)
	}

	// prev 槽覆盖续签切换窗口与 .prev 回滚；指纹校验失败即拒绝（D11）。
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

	// 握手已通过时，尽力刷新 subject/expiry 账面字段；不影响接入结果。
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

func (s *Server) markNodeRegister(ctx context.Context, nodeID string, info *pb.SystemInfo) {
	var osFamily, arch, osProduct, osVersion, kernel string
	if info != nil {
		osFamily = info.GetOs()
		arch = info.GetArch()
		osProduct = info.GetOsName()
		osVersion = info.GetOsVersion()
		kernel = info.GetKernelVersion()
	}
	if err := s.nodes.MarkOnlineRegister(ctx, nodeID, osFamily, arch, osProduct, osVersion, kernel); err != nil {
		s.logger.Warn("Failed to mark node online on register", "error", err, "nodeID", nodeID)
		if conn, ok := s.registry.GetConnection(nodeID); ok && conn != nil {
			conn.MarkHeartbeatFlushed(time.Time{})
		}
		return
	}
	s.logger.Debug("Node marked online on register", "nodeID", nodeID, "server_id", s.nodeID)
	if s.deps.Publisher != nil {
		s.deps.Publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "refresh", "", "", "")
	}
}

// HandleLeaderDemotion 在 Leader 失去租约时执行自隔离（降级即断流）：
//  1. 使所有在途同步派发立即失败（而非挂起到超时）；
//  2. 向全部已注册 Agent 发送重定向并标记连接替换，引导 Agent 主动迁移到新 Leader。
//
// 新注册拒绝由 acceptAgent 的 Standby 检查承担。该方法注册为 Elector 的降级钩子。

func (s *Server) markNodeDisconnected(ctx context.Context, nodeID string) {
	if err := s.nodes.MarkDisconnected(ctx, nodeID); err != nil {
		s.logger.Warn("Failed to mark node disconnected", "nodeID", nodeID, "error", err)
	} else {
		s.logger.Debug("Node marked disconnected in DB", "nodeID", nodeID, "server_id", s.nodeID)
		if s.deps.Publisher != nil {
			s.deps.Publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "notify", "节点离线通知", fmt.Sprintf("节点 %s 已下线断开", nodeID), "warning")
		}
	}
}

func (s *Server) startNodeStatusReconciler(done <-chan struct{}) {
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

				// 本机证书检查与吊销会话摘除：不依赖 Leader（密钥与路径仅本机可见）。
				selfCtx, selfCancel := context.WithTimeout(context.Background(), nodeStatusUpdateTimeout)
				s.checkSelfCert(selfCtx)
				s.dropRevokedSessions(selfCtx)
				selfCancel()

				if s.leader != nil && !s.leader.IsLeader() {
					return
				}

				ctx, cancel := context.WithTimeout(context.Background(), nodeStatusUpdateTimeout)
				defer cancel()

				acquired, _, err := s.leases.TryAcquire(ctx, "cluster_node_status_check", s.nodeID, time.Minute)
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
				} else if len(staledNodes) > 0 && s.deps.Publisher != nil {
					for _, nodeID := range staledNodes {
						s.deps.Publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "notify", "节点心跳超时通知", fmt.Sprintf("节点 %s 心跳超时，已标记为下线", nodeID), "warning")
					}
				}

				s.runCertInspection(ctx)
			}()
		case <-done:
			return
		}
	}
}
