package controlplane

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/concurrency"
	"design-cloud-engine/internal/platform/host"
	controldispatch "design-cloud-engine/internal/server/controlplane/dispatch"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// ForwardSlots limits concurrent cluster event forward RPCs across control
// nodes. It is control-plane policy, not a generic utility: acquire before
// spawning work, release when the RPC finishes.
var ForwardSlots = make(chan struct{}, 8)

type ForwardClient struct {
	logger        *slog.Logger
	mu            sync.Mutex
	conns         map[string]*grpc.ClientConn
	clusterPort   int
	certs         Certs
	tlsServerName string
	nodeID        string
}

func newForwardClient(clusterPort int, certs Certs, tlsServerName string, nodeID string, logger *slog.Logger) *ForwardClient {
	return &ForwardClient{
		logger:        logger.With("component", "control_forward"),
		conns:         make(map[string]*grpc.ClientConn),
		clusterPort:   clusterPort,
		certs:         certs,
		tlsServerName: tlsServerName,
		nodeID:        nodeID,
	}
}

func (c *ForwardClient) getConn(leaderID string) (*grpc.ClientConn, error) {
	addr := net.JoinHostPort(leaderID, fmt.Sprint(c.clusterPort))

	c.mu.Lock()
	defer c.mu.Unlock()

	if conn, ok := c.conns[leaderID]; ok {
		state := conn.GetState()
		if state != connectivity.Shutdown && state != connectivity.TransientFailure {
			return conn, nil
		}
		c.logger.Warn("Leader connection is stale, recreating", "leader", leaderID, "state", state)
		_ = conn.Close()
		delete(c.conns, leaderID)
	}

	serverName := cmp.Or(c.tlsServerName, leaderID)

	creds, err := buildClientTLSCredentials(c.certs, serverName)
	if err != nil {
		return nil, fmt.Errorf("build control client credentials: %w", err)
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                defaultKeepaliveTime,
			Timeout:             defaultKeepaliveTimeout,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect leader %s: %w", leaderID, err)
	}

	c.conns[leaderID] = conn
	return conn, nil
}

func (c *ForwardClient) evict(leaderID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, ok := c.conns[leaderID]
	if !ok {
		return
	}
	_ = conn.Close()
	delete(c.conns, leaderID)
}

func (c *ForwardClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for leaderID, conn := range c.conns {
		_ = conn.Close()
		delete(c.conns, leaderID)
	}
}

// defaultForwardTimeout 是转发调用的强制兜底 deadline：调用方未带 deadline 时由客户端补齐，
// 服务端则直接拒绝无 deadline 的调用，避免请求挂死。
const defaultForwardTimeout = 5 * time.Second

func (c *ForwardClient) ForwardDispatch(ctx context.Context, leaderID string, leaderEpoch uint64, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if leaderID == "" {
		return nil, fmt.Errorf("leader is unknown")
	}
	if _, ok := ctx.Deadline(); !ok {
		// 兜底 deadline 必须覆盖业务超时：同步派发的等待上限由 req.Timeout 决定，
		// 固定小值会让 Leader 侧 ctx 先到期，进而下发 cancel 杀掉 Agent 上正在执行的动作。
		// defaultForwardTimeout 在此仅作为网络与建连的余量。
		fallback := defaultForwardTimeout
		if req.Timeout > 0 {
			fallback += time.Duration(req.Timeout) * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fallback)
		defer cancel()
	}

	conn, err := c.getConn(leaderID)
	if err != nil {
		return nil, err
	}

	protoReq, err := controldispatch.ToForwardRequest(c.nodeID, leaderEpoch, req)
	if err != nil {
		return nil, err
	}

	resp, err := pb.NewControlServiceClient(conn).ForwardDispatch(ctx, protoReq)
	if err != nil {
		c.logger.Warn("ForwardDispatch failed", "leader", leaderID, "error", err, "trace_id", req.TraceID, "execution_id", req.ExecutionID)
		if shouldEvictConn(err) {
			c.evict(leaderID)
		}
		return nil, fmt.Errorf("forward dispatch to leader %s: %w", leaderID, err)
	}

	return controldispatch.FromForwardResponse(resp), nil
}

// shouldEvictConn 仅对传输层不可用重建连接。FailedPrecondition（非 Leader / epoch 过期）
// 说明对端仍然可达，重建连接只会让重试路径多一次 TLS 握手；DeadlineExceeded / Canceled
// 属于调用侧超时或取消，与连接健康无关。
func shouldEvictConn(err error) bool {
	return status.Code(err) == codes.Unavailable
}

func (c *ForwardClient) ForwardClusterEvent(ctx context.Context, targetNodeID string, req *pb.ForwardClusterEventRequest) error {
	if targetNodeID == "" {
		return fmt.Errorf("target node is unknown")
	}

	conn, err := c.getConn(targetNodeID)
	if err != nil {
		return err
	}

	_, err = pb.NewControlServiceClient(conn).ForwardClusterEvent(ctx, req)
	if err != nil && isCriticalClusterEvent(req.EventName) {
		// 关键事件（节点配置变更等）扇出失败重试 1 次，避免静默丢失
		c.logger.Warn("ForwardClusterEvent failed for critical event, retrying once", "target", targetNodeID, "event", req.EventName, "event_id", req.EventId, "error", err)
		_, err = pb.NewControlServiceClient(conn).ForwardClusterEvent(ctx, req)
	}
	if err != nil {
		c.logger.Warn("ForwardClusterEvent failed", "target", targetNodeID, "event", req.EventName, "event_id", req.EventId, "error", err)
		c.evict(targetNodeID)
		return fmt.Errorf("forward event to node %s: %w", targetNodeID, err)
	}

	return nil
}

// isCriticalClusterEvent 标记扇出失败需要重试一次的关键集群事件。
func isCriticalClusterEvent(eventName string) bool {
	return eventName == "cluster:node_overrides_updated"
}

// ForwardDispatch 是控制面 gRPC 服务端入口：Standby 转发来的派发请求在 Leader 上执行。
func (s *Server) ForwardDispatch(ctx context.Context, req *pb.ForwardDispatchRequest) (*pb.ForwardDispatchResponse, error) {
	if s.dispatcher == nil {
		return nil, fmt.Errorf("dispatch service is not initialized")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, status.Error(codes.InvalidArgument, "forward dispatch requires a deadline")
	}
	if req.Hop >= 1 {
		return nil, status.Error(codes.FailedPrecondition, "forward hop limit exceeded")
	}
	if s.leader != nil {
		if !s.leader.IsLeader() {
			leaderID := s.leader.LeaderID()
			if leaderID != "" {
				return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("not leader: current leader is %s", leaderID))
			}
			return nil, status.Error(codes.FailedPrecondition, "not leader")
		}
		if req.LeaderEpoch != s.leader.LocalEpoch() {
			return nil, status.Error(codes.FailedPrecondition,
				fmt.Sprintf("stale leader epoch: got %d, current %d", req.LeaderEpoch, s.leader.LocalEpoch()))
		}
	}

	execReq, err := controldispatch.FromForwardRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := s.dispatcher.DispatchToAgent(ctx, execReq)
	if err != nil {
		return nil, err
	}
	return controldispatch.ToForwardResponse(resp), nil
}

// ForwardClusterEvent 是控制面 gRPC 服务端入口：接收对等控制节点转发的集群事件。
func (s *Server) ForwardClusterEvent(ctx context.Context, req *pb.ForwardClusterEventRequest) (*pb.ForwardClusterEventResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("empty cluster event request")
	}

	// 超 TTL 事件直接丢弃
	now := time.Now()
	if req.ExpiresAt > 0 && now.Unix() > req.ExpiresAt {
		s.logger.Warn("Dropping expired cluster event", "event_id", req.EventId, "event", req.EventName, "source", req.SourceNodeId)
		return &pb.ForwardClusterEventResponse{Success: true}, nil
	}

	// 去重：变更节点直发所有 peer + Leader 二次扇出的双路径会让同一事件到达同一节点两次
	if req.EventId != "" {
		expiresAt := now.Add(ClusterEventDefaultTTL)
		if req.ExpiresAt > 0 {
			expiresAt = time.Unix(req.ExpiresAt, 0)
		}
		if s.eventDedup.seenOrAdd(req.EventId, expiresAt) {
			s.logger.Debug("Dropping duplicate cluster event", "event_id", req.EventId, "event", req.EventName, "source", req.SourceNodeId)
			return &pb.ForwardClusterEventResponse{Success: true}, nil
		}
	}

	// Leader 收到 Standby 转发的节点配置变更通知后，向目标 Agent 下推。
	if req.EventName == "cluster:node_overrides_updated" {
		nodeID := strings.TrimSpace(req.PayloadJson)
		if nodeID != "" {
			concurrency.SafeGo(s.logger, "push-node-settings-update", func() {
				pCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.deps.SettingsPusher.PushNodeSettingsUpdate(pCtx, nodeID); err != nil {
					s.logger.Warn("Failed to execute forwarded PushNodeSettingsUpdate on leader", "nodeID", nodeID, "error", err)
				}
			})
		}
		return &pb.ForwardClusterEventResponse{Success: true}, nil
	}

	if s.deps.Publisher != nil {
		s.deps.Publisher.DeliverRemoteEvent(ctx, req.TargetType, req.UserIds, int(req.Role), req.EventName, req.PayloadJson)
	}

	// Leader 再广播给其他 Standby 控制节点（排除来源节点）；hop 上限 1 是防御性防环。
	if req.Hop < 1 && s.leader != nil && s.leader.IsLeader() && s.forwarder != nil {
		req.Hop++
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
				if !concurrency.TryBoundedGo(ForwardSlots, s.logger, "forward-cluster-event", func() {
					fCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = s.forwarder.ForwardClusterEvent(fCtx, targetID, req)
				}) {
					s.logger.Warn("dropping forwarded cluster event: forwarding capacity is full",
						"event", req.EventName, "target", targetID)
				}
			}
		}
	}

	return &pb.ForwardClusterEventResponse{Success: true}, nil
}
