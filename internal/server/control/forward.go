package control

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
)

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

func (c *ForwardClient) ForwardDispatch(ctx context.Context, leaderID string, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if leaderID == "" {
		return nil, fmt.Errorf("leader is unknown")
	}

	conn, err := c.getConn(leaderID)
	if err != nil {
		return nil, err
	}

	protoReq, err := toForwardDispatchRequest(c.nodeID, req)
	if err != nil {
		return nil, err
	}

	resp, err := pb.NewControlServiceClient(conn).ForwardDispatch(ctx, protoReq)
	if err != nil {
		c.logger.Warn("ForwardDispatch failed", "leader", leaderID, "error", err, "trace_id", req.TraceID, "execution_id", req.ExecutionID)
		c.evict(leaderID)
		return nil, fmt.Errorf("forward dispatch to leader %s: %w", leaderID, err)
	}

	return fromForwardDispatchResponse(resp), nil
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
	if err != nil {
		c.logger.Warn("ForwardClusterEvent failed", "target", targetNodeID, "event", req.EventName, "error", err)
		c.evict(targetNodeID)
		return fmt.Errorf("forward event to node %s: %w", targetNodeID, err)
	}

	return nil
}
