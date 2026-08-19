// Package control implements the server-side control plane for cluster communication.
// This file contains Dispatcher which routes requests to connected agents.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/control/leader"

	"design-cloud-engine/internal/common"
)

const (
	networkLatencyBuffer = 5 * time.Second
)

var (
	errTimeout           = errors.New("operation timed out")
	errInFlight          = errors.New("dispatch already in-flight")
	errNodeInMaintenance = errors.New("target node is in maintenance mode")
)

type waitDispatchResultFunc func(ctx context.Context, resultCh chan any, timeout time.Duration) (any, error)

// Dispatcher implements execution.Executor using connected agents via AgentRegistry.
// Control plane is responsible for dispatching work, not executing it locally.
type Dispatcher struct {
	registry  *AgentRegistry
	leader    leader.Reader
	forwarder *ForwardClient
	logger    *slog.Logger
	nodes     *db.NodeRepo
	execs     *db.ExecutionRepo
	waiter    waitDispatchResultFunc
	nodeID    string
}

// newDispatcher creates a new control-plane dispatch service.
// forwarder must not be nil; use NewForwardClient to construct one.
func newDispatcher(registry *AgentRegistry, leaderState leader.Reader, logger *slog.Logger, forwarder *ForwardClient, nodeID string, nodes *db.NodeRepo, execs *db.ExecutionRepo) *Dispatcher {
	return &Dispatcher{
		registry:  registry,
		leader:    leaderState,
		forwarder: forwarder,
		logger:    logger.With("component", "control_dispatch"),
		nodes:     nodes,
		execs:     execs,
		waiter:    defaultWaitResult,
		nodeID:    nodeID,
	}
}

// defaultWaitResult waits for a result, timeout, or context cancellation
func defaultWaitResult(ctx context.Context, resultCh chan any, timeout time.Duration) (any, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-resultCh:
		if result == nil {
			return nil, fmt.Errorf("received nil result from channel")
		}
		if err, ok := result.(error); ok {
			return nil, err
		}
		return result, nil
	case <-timer.C:
		return nil, errTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Execute implements execution.Executor. Standby nodes forward to leader;
// the leader dispatches directly. On forward failure with AsyncTracked mode,
// falls back to a DB lookup before returning the error.
func (s *Dispatcher) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if s.leader != nil && !s.leader.IsLeader() {
		leaderID := s.leader.LeaderID()
		if s.forwarder != nil && leaderID != "" {
			resp, err := s.forwarder.ForwardDispatch(ctx, leaderID, req)
			if err == nil {
				return resp, nil
			}

			s.logger.Warn("ForwardDispatch failed",
				"leader", leaderID,
				"trace_id", req.TraceID,
				"execution_id", req.ExecutionID,
				"mode", req.Mode,
				"error", err)

			if req.Mode == execution.RunModeAsyncTracked && req.ExecutionID != "" {
				if dbResp, dbErr := s.lookupActionResult(ctx, req.ExecutionID, false); dbErr == nil {
					s.logger.Info("Recovered dispatched result from DB after forward failure",
						"execution_id", req.ExecutionID,
						"trace_id", dbResp.TraceID)
					return dbResp, nil
				}
			}
			return nil, err
		}
		return nil, fmt.Errorf("cluster leader not available: no forwarder or no leader elected (leader_id=%q)", leaderID)
	}
	return s.DispatchToAgent(ctx, req)
}

// DispatchToAgent sends a request to a connected agent and waits for the result.
// It assumes the current node owns the command plane.
func (s *Dispatcher) DispatchToAgent(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	targetNodeID, err := s.selectTargetAgent(ctx, req)
	if err != nil {
		return nil, err
	}

	s.logger.Debug("Dispatching request to agent",
		"trace_id", req.TraceID,
		"execution_id", req.ExecutionID,
		"target_node_id", targetNodeID,
		"mode", req.Mode)

	cmd, err := buildCommandRequest(req)
	if err != nil {
		return nil, err
	}

	if req.Mode == execution.RunModeAsyncTracked {
		return s.dispatchAsync(ctx, req, targetNodeID, cmd)
	}

	return s.dispatchSync(ctx, req, targetNodeID, cmd)
}

func buildCommandRequest(req *execution.ExecuteRequest) (*pb.ServerCommand, error) {
	cmd, err := toCommandRequest(req)
	if err != nil {
		return nil, err
	}

	return &pb.ServerCommand{
		Payload: &pb.ServerCommand_CommandRequest{
			CommandRequest: cmd,
		},
	}, nil
}

// dispatchAsync handles RunModeAsyncTracked requests. The execution record is
// persisted to DB before sending the command, providing exactly-once semantics
// via execution_id deduplication. A duplicate execution_id returns the persisted
// result (if finished) or errInFlight.
func (s *Dispatcher) dispatchAsync(ctx context.Context, req *execution.ExecuteRequest, targetNodeID string, cmd *pb.ServerCommand) (*execution.ExecuteResponse, error) {
	traceID := req.TraceID

	if err := s.execs.Create(ctx, req.ExecutionID, string(req.ActionName), traceID, s.nodeID, targetNodeID); err != nil {
		if errors.Is(err, db.ErrExecutionActionExists) {
			return s.lookupActionResult(ctx, req.ExecutionID, true)
		}
		return nil, fmt.Errorf("failed to create execution record: %w", err)
	}

	if err := s.registry.SendCommand(targetNodeID, cmd); err != nil {
		if _, dbErr := s.execs.MarkTerminal(ctx, traceID, model.ExecutionStatusFailed, false, 0, err.Error()); dbErr != nil {
			s.logger.Warn("Failed to mark execution terminal after send failure",
				"error", dbErr, "trace_id", traceID)
		}
		s.logger.Error("Failed to send async dispatch command",
			"error", err,
			"trace_id", traceID,
			"target_node_id", targetNodeID)
		return nil, fmt.Errorf("failed to send command to agent %s: %w", targetNodeID, err)
	}
	if err := s.execs.MarkDispatched(ctx, traceID); err != nil {
		s.logger.Warn("Failed to mark async execution dispatched", "trace_id", traceID, "error", err)
	}

	return &execution.ExecuteResponse{
		ExecutionID: req.ExecutionID,
		TraceID:     traceID,
		Accepted:    true,
		Success:     true,
	}, nil
}

// dispatchSync handles synchronous (blocking) requests using an in-memory
// pending request channel. On timeout, a CancelCommand is sent to the agent
// before returning the error.
func (s *Dispatcher) dispatchSync(ctx context.Context, req *execution.ExecuteRequest, targetNodeID string, cmd *pb.ServerCommand) (*execution.ExecuteResponse, error) {
	traceID := req.TraceID

	// Pending requests for synchronous calls are in-memory only.
	resultCh := s.registry.CreatePendingRequest(traceID, targetNodeID)
	defer s.registry.DeletePendingRequest(traceID)

	if err := s.registry.SendCommand(targetNodeID, cmd); err != nil {
		s.logger.Error("Failed to send dispatch command",
			"error", err,
			"trace_id", traceID,
			"target_node_id", targetNodeID)
		return nil, fmt.Errorf("failed to send command to agent %s: %w", targetNodeID, err)
	}

	// Guard against Timeout=0: fall back to 30s so the server never races with
	// the launcher's own 30s default timeout.
	requestTimeout := req.Timeout
	if requestTimeout <= 0 {
		requestTimeout = 30
	}
	waitTimeout := time.Duration(requestTimeout)*time.Second + networkLatencyBuffer

	buildCancelCmd := func() *pb.ServerCommand {
		return &pb.ServerCommand{
			Payload: &pb.ServerCommand_CancelCommand{
				CancelCommand: &pb.CancelCommand{TraceId: traceID},
			},
		}
	}
	sendCancel := func(reason string) {
		if sendErr := s.registry.SendCommand(targetNodeID, buildCancelCmd()); sendErr != nil {
			s.logger.Warn("Failed to send cancel command", "reason", reason, "target_node_id", targetNodeID, "error", sendErr)
		}
	}

	result, err := s.waiter(ctx, resultCh, waitTimeout)
	if err != nil {
		if errors.Is(err, errTimeout) {
			s.logger.Error("Dispatch request timeout",
				"trace_id", traceID,
				"target_node_id", targetNodeID,
				"timeout", waitTimeout)
			// Best-effort: tell the agent to stop the in-flight task.
			sendCancel("timeout")
			return nil, fmt.Errorf("timeout waiting for result from agent %s", targetNodeID)
		}

		// Handle context cancellation or other errors.
		s.logger.Warn("Dispatch request cancelled/failed, sending kill signal to agent",
			"trace_id", traceID,
			"target_node_id", targetNodeID,
			"error", err)
		sendCancel("context cancelled")
		return nil, err
	}

	dispatchResult, ok := result.(*pb.CommandResult)
	if !ok {
		if result == nil {
			return nil, fmt.Errorf("received nil result (pending request was cancelled)")
		}
		return nil, fmt.Errorf("unexpected result type: %T", result)
	}

	s.logger.Info("Received dispatched result",
		"trace_id", traceID,
		"success", dispatchResult.Success,
		"exit_code", dispatchResult.ExitCode,
		"duration_ms", dispatchResult.Duration.AsDuration().Milliseconds())

	return &execution.ExecuteResponse{
		ExecutionID: req.ExecutionID,
		TraceID:     traceID,
		Success:     dispatchResult.Success,
		ExitCode:    int(dispatchResult.ExitCode),
		Stdout:      dispatchResult.Stdout,
		Stderr:      dispatchResult.Stderr,
		SystemError: dispatchResult.SystemError,
		Duration:    dispatchResult.Duration.AsDuration().Milliseconds(),
	}, nil
}

// lookupActionResult returns the result of a previously submitted execution_id.
// When includeInFlight=true, pending/dispatched states are returned as errInFlight.
func (s *Dispatcher) lookupActionResult(ctx context.Context, executionID string, includeInFlight bool) (*execution.ExecuteResponse, error) {
	record, err := s.execs.GetByExecutionID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution_id lookup failed: %w", err)
	}

	switch record.Status {
	case model.ExecutionStatusPending, model.ExecutionStatusDispatched:
		if !includeInFlight {
			return nil, fmt.Errorf("execution_id %s not finished (status=%d)", executionID, record.Status)
		}
		return nil, fmt.Errorf("%w: execution_id=%s status=%d", errInFlight, executionID, record.Status)

	case model.ExecutionStatusSucceeded, model.ExecutionStatusFailed,
		model.ExecutionStatusTimedOut, model.ExecutionStatusCancelled:
		s.logger.Info("Returning existing dispatch result for execution_id",
			"execution_id", executionID,
			"trace_id", record.TraceID,
			"status", record.Status)
		return &execution.ExecuteResponse{
			ExecutionID: executionID,
			TraceID:     record.TraceID,
			Success:     record.Success,
			ExitCode:    record.ExitCode,
			SystemError: record.SystemError,
		}, nil
	default:
		return nil, fmt.Errorf("execution_id %s has unknown status: %d", executionID, record.Status)
	}
}

// nodeMatchesSelector reports whether all key-value pairs in selector are present
// in labels with matching values. An empty selector matches any labels map.
func nodeMatchesSelector(labels, selector map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// connectedEligibleAgent returns true when nodeID is connected, not in
// maintenance mode, and its labels satisfy the given selector.
// A DB error is treated the same as maintenance: the node is skipped.
func (s *Dispatcher) connectedEligibleAgent(ctx context.Context, nodeID string, selector map[string]string) bool {
	if !s.registry.IsConnected(nodeID) {
		return false
	}
	node, err := s.nodes.GetByNodeID(ctx, nodeID)
	if err != nil || node.Status == model.NodeStatusMaintenance {
		return false
	}
	return nodeMatchesSelector(node.Labels, selector)
}

// selectTargetAgent picks the target node. For pinned requests (TargetNode set)
// it validates connectivity; for pool-based requests, NodeAffinity governs selection.
func (s *Dispatcher) selectTargetAgent(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	if req.TargetNode != "" {
		// Pinned dispatch: route to the explicitly specified node, no fallback.
		if !s.registry.IsConnected(req.TargetNode) {
			s.logger.Warn("Target agent not connected",
				"target_node", req.TargetNode,
				"trace_id", req.TraceID)
			return "", fmt.Errorf("target agent not connected: node %s is offline or unreachable", req.TargetNode)
		}
		node, err := s.nodes.GetByNodeID(ctx, req.TargetNode)
		if err != nil {
			if !errors.Is(err, common.ErrNotFound) {
				s.logger.Warn("failed to check node maintenance status, denying dispatch",
					"target_node", req.TargetNode, "error", err)
				return "", fmt.Errorf("maintenance check failed: %w", err)
			}
			// Node not found in DB — still connected via registry, allow dispatch
		} else if node.Status == model.NodeStatusMaintenance {
			return "", errNodeInMaintenance
		}
		s.logger.Debug("Using pinned target agent", "target_node_id", req.TargetNode)
		return req.TargetNode, nil
	}

	// Pool-based dispatch: build a candidate pool, then pick via NodeAffinity.
	var targetNodeID string
	switch req.NodeAffinity {
	case execution.NodeAffinityLocalPreferred:
		// Prefer the local node if it is healthy and matches selectors; fall back to least-loaded.
		if s.connectedEligibleAgent(ctx, s.nodeID, req.NodeSelector) {
			targetNodeID = s.nodeID
			s.logger.Debug("Using preferred local agent", "target_node_id", targetNodeID)
		} else {
			targetNodeID = s.selectLeastLoadedAgent(ctx, req.NodeSelector)
		}
	default: // NodeAffinityAny
		targetNodeID = s.selectLeastLoadedAgent(ctx, req.NodeSelector)
	}

	if targetNodeID == "" {
		s.logger.Warn("No agents available for dispatch",
			"trace_id", req.TraceID,
			"execution_id", req.ExecutionID,
			"node_selector", req.NodeSelector,
			"node_affinity", req.NodeAffinity)
		return "", fmt.Errorf("no agents available")
	}
	s.logger.Debug("Using pool-selected agent", "target_node_id", targetNodeID, "affinity", req.NodeAffinity)
	return targetNodeID, nil
}

// selectLeastLoadedAgent selects a lightly loaded agent using the Power of Two
// Choices approach. All connected nodes are batch-loaded from DB in a single
// round-trip, then filtered by maintenance status and label selector.
// Returns empty string if no eligible agents are available.
func (s *Dispatcher) selectLeastLoadedAgent(ctx context.Context, selector map[string]string) string {
	connectedAgents := s.registry.GetConnectedAgents()
	if len(connectedAgents) == 0 {
		return ""
	}

	// Batch load all candidate node records in a single DB round-trip.
	nodes, err := s.nodes.ListByNodeIDs(ctx, connectedAgents)
	if err != nil {
		s.logger.Warn("Failed to batch load node records for dispatch", "error", err)
		return ""
	}
	nodeByID := make(map[string]model.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.NodeID] = n
	}

	// Filter out maintenance mode agents and mismatching selectors.
	var eligible []string
	for _, nodeID := range connectedAgents {
		node, ok := nodeByID[nodeID]
		if !ok || node.Status == model.NodeStatusMaintenance {
			continue
		}
		if !nodeMatchesSelector(node.Labels, selector) {
			continue
		}
		eligible = append(eligible, nodeID)
	}

	if len(eligible) == 0 {
		return ""
	}
	if len(eligible) == 1 {
		return eligible[0]
	}

	// Power of Two Choices
	idx1 := rand.IntN(len(eligible))
	idx2 := rand.IntN(len(eligible))
	if idx1 == idx2 {
		return eligible[idx1]
	}

	node1 := eligible[idx1]
	node2 := eligible[idx2]

	if s.calculateNodeScore(node1) <= s.calculateNodeScore(node2) {
		return node1
	}
	return node2
}

func (s *Dispatcher) calculateNodeScore(nodeID string) float64 {
	snapshot, ok := s.registry.Snapshot(nodeID)
	if !ok || snapshot.ResourceReport == nil {
		return 99999.0 // High penalty for unknown load
	}
	// Task count strongly biases light tasks to least active nodes.
	// CpuUtil (0-1 平均利用率，所有核平均) acts as a reliable tie breaker.
	return float64(snapshot.ResourceReport.ActiveTasks)*100.0 + snapshot.ResourceReport.CpuUtil
}
