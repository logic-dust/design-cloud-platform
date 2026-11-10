// Package dispatch routes execution requests to connected Agents.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/controlplane/leader"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const networkLatencyBuffer = 5 * time.Second

var (
	errTimeout           = errors.New("operation timed out")
	errInFlight          = errors.New("dispatch already in-flight")
	errNodeInMaintenance = errors.New("target node is in maintenance mode")
)

type Forwarder interface {
	ForwardDispatch(ctx context.Context, leaderID string, leaderEpoch uint64, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error)
}

// Refresher 主动刷新一次 Leader 视图；由转发重试路径在 Leader 信息失效时调用。
type Refresher interface {
	RefreshNow(ctx context.Context)
}

type waitResultFunc func(ctx context.Context, resultCh chan any, timeout time.Duration) (any, error)

// Dispatcher implements execution.Executor using connected Agents.
type Dispatcher struct {
	registry  *agentregistry.Registry
	leader    leader.Reader
	refresher Refresher
	forwarder Forwarder
	logger    *slog.Logger
	nodes     *db.NodeRepo
	execs     *db.ExecutionRepo
	waiter    waitResultFunc
	nodeID    string
}

func New(
	registry *agentregistry.Registry,
	leaderState leader.Reader,
	logger *slog.Logger,
	forwarder Forwarder,
	nodeID string,
	nodes *db.NodeRepo,
	execs *db.ExecutionRepo,
) *Dispatcher {
	d := &Dispatcher{
		registry:  registry,
		leader:    leaderState,
		forwarder: forwarder,
		logger:    logger.With("component", "control_dispatch"),
		nodes:     nodes,
		execs:     execs,
		waiter:    defaultWaitResult,
		nodeID:    nodeID,
	}
	// 可选：Leader 视图主动刷新能力（*leader.Elector 实现），用于转发失败后的有界重试。
	if r, ok := leaderState.(Refresher); ok {
		d.refresher = r
	}
	return d
}

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

// Execute forwards on Standby and dispatches directly on Leader.
func (d *Dispatcher) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if d.leader != nil && !d.leader.IsLeader() {
		leaderID := d.leader.LeaderID()
		if d.forwarder != nil && leaderID != "" {
			resp, err := d.forwarder.ForwardDispatch(ctx, leaderID, d.leader.LeaderEpoch(), req)
			if err != nil && isRetryableForwardError(err) {
				// 有界重试 1 次：先刷新 Leader 视图（可能已切换），再以新的
				// LeaderID/LeaderEpoch 重试；InvalidArgument 等确定性错误不重试。
				if d.refresher != nil {
					rCtx, cancel := context.WithTimeout(context.Background(), forwardRefreshTimeout)
					d.refresher.RefreshNow(rCtx)
					cancel()
				}
				if newLeaderID := d.leader.LeaderID(); newLeaderID != "" {
					resp, err = d.forwarder.ForwardDispatch(ctx, newLeaderID, d.leader.LeaderEpoch(), req)
				}
			}
			if err == nil {
				return resp, nil
			}

			d.logger.Warn("ForwardDispatch failed",
				"leader", leaderID,
				"trace_id", req.TraceID,
				"execution_id", req.ExecutionID,
				"mode", req.Mode,
				"error", err)

			// 服务端转发重试按 ExecutionID 幂等（覆盖全部运行模式）：
			// 转发可能已到达 Leader 并执行，回查已有结果避免重复执行。
			// HTTP 客户端重试会生成全新 ExecutionID，语义为 at-least-once（见 §4.3.3）。
			if req.ExecutionID != "" {
				if dbResp, dbErr := d.lookupActionResult(ctx, req.ExecutionID, false); dbErr == nil {
					// ExecutionRecord 不持久化 stdout/stderr，恢复出的结果只保证
					// outcome 字段可信。依赖 stdout 解析业务标识（如调度器 job ID）
					// 的调用方不得把空输出当作成功载荷。
					d.logger.Warn("Recovered terminal result from DB after forward failure; stdout/stderr is not persisted",
						"execution_id", req.ExecutionID,
						"trace_id", dbResp.TraceID,
						"success", dbResp.Success,
						"action", req.ActionName)
					return dbResp, nil
				}
			}
			return nil, contract.NewUnavailableError(fmt.Sprintf("forward dispatch to leader %s failed", leaderID))
		}
		return nil, fmt.Errorf("cluster leader not available: no forwarder or no leader elected (leader_id=%q)", leaderID)
	}
	return d.DispatchToAgent(ctx, req)
}

// forwardRefreshTimeout 约束转发失败后的 Leader 视图刷新耗时。
const forwardRefreshTimeout = 5 * time.Second

// recordWriteTimeout 约束执行记录状态写入的耗时。记账必须与请求生命周期解耦：
// 请求取消/超时后 ctx 已失效，若继续用原 ctx 写库会失败，记录将永久停留在
// pending/dispatched，后续相同 execution_id 的请求会一直命中 in-flight。
const recordWriteTimeout = 5 * time.Second

// recordWriteContext 派生一个不随请求取消、但有独立上限的记账上下文。
func recordWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
}

func (d *Dispatcher) markDispatched(ctx context.Context, traceID string) {
	wCtx, cancel := recordWriteContext(ctx)
	defer cancel()
	if err := d.execs.MarkDispatched(wCtx, traceID); err != nil {
		d.logger.Warn("Failed to mark execution dispatched", "trace_id", traceID, "error", err)
	}
}

func (d *Dispatcher) markTerminal(ctx context.Context, traceID string, status model.ExecutionStatus, exitCode int, systemError string) {
	wCtx, cancel := recordWriteContext(ctx)
	defer cancel()
	if _, err := d.execs.MarkTerminal(wCtx, traceID, status, exitCode, systemError); err != nil {
		d.logger.Warn("Failed to mark execution terminal", "trace_id", traceID, "status", status, "error", err)
	}
}

// isRetryableForwardError 限定可重试的转发失败：连接不可用、超时、或 Leader 视图失效
// （非 Leader / epoch 过期 / hop 超限）。其余错误（如参数校验）重试无意义。
func isRetryableForwardError(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

// DispatchToAgent sends a request to an Agent and applies its run mode.
func (d *Dispatcher) DispatchToAgent(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	targetNodeID, err := d.selectTargetAgent(ctx, req)
	if err != nil {
		return nil, err
	}

	d.logger.Debug("Dispatching request to agent",
		"trace_id", req.TraceID,
		"execution_id", req.ExecutionID,
		"target_node_id", targetNodeID,
		"mode", req.Mode)

	command, err := buildCommandRequest(req)
	if err != nil {
		return nil, err
	}
	if req.Mode == execution.RunModeAsyncTracked {
		return d.dispatchAsync(ctx, req, targetNodeID, command)
	}
	return d.dispatchSync(ctx, req, targetNodeID, command)
}

func buildCommandRequest(req *execution.ExecuteRequest) (*pb.ServerCommand, error) {
	command, err := toCommandRequest(req)
	if err != nil {
		return nil, err
	}
	return &pb.ServerCommand{
		Payload: &pb.ServerCommand_CommandRequest{CommandRequest: command},
	}, nil
}

func (d *Dispatcher) dispatchAsync(
	ctx context.Context,
	req *execution.ExecuteRequest,
	targetNodeID string,
	command *pb.ServerCommand,
) (*execution.ExecuteResponse, error) {
	traceID := req.TraceID
	if err := d.execs.Create(ctx, req.ExecutionID, string(req.ActionName), traceID, d.nodeID, targetNodeID); err != nil {
		if errors.Is(err, db.ErrExecutionActionExists) {
			return d.lookupActionResult(ctx, req.ExecutionID, true)
		}
		return nil, fmt.Errorf("failed to create execution record: %w", err)
	}

	if err := d.registry.SendCommand(targetNodeID, command); err != nil {
		d.markTerminal(ctx, traceID, model.ExecutionStatusFailed, 0, err.Error())
		d.logger.Error("Failed to send async dispatch command",
			"error", err,
			"trace_id", traceID,
			"target_node_id", targetNodeID)
		return nil, fmt.Errorf("failed to send command to agent %s: %w", targetNodeID, err)
	}
	d.markDispatched(ctx, traceID)

	return &execution.ExecuteResponse{
		ExecutionID: req.ExecutionID,
		TraceID:     traceID,
		Accepted:    true,
		Success:     true,
	}, nil
}

func (d *Dispatcher) dispatchSync(
	ctx context.Context,
	req *execution.ExecuteRequest,
	targetNodeID string,
	command *pb.ServerCommand,
) (*execution.ExecuteResponse, error) {
	traceID := req.TraceID

	// 幂等键覆盖全模式：同步派发同样落执行记录，重复 ExecutionID 回查已有结果。
	if err := d.execs.Create(ctx, req.ExecutionID, string(req.ActionName), traceID, d.nodeID, targetNodeID); err != nil {
		if errors.Is(err, db.ErrExecutionActionExists) {
			return d.lookupActionResult(ctx, req.ExecutionID, true)
		}
		return nil, fmt.Errorf("failed to create execution record: %w", err)
	}
	markFailed := func(cause error) {
		d.markTerminal(ctx, traceID, model.ExecutionStatusFailed, 0, cause.Error())
	}

	resultCh := d.registry.CreatePendingRequest(traceID, targetNodeID)
	defer d.registry.DeletePendingRequest(traceID)

	if err := d.registry.SendCommand(targetNodeID, command); err != nil {
		d.logger.Error("Failed to send dispatch command",
			"error", err,
			"trace_id", traceID,
			"target_node_id", targetNodeID)
		markFailed(err)
		return nil, fmt.Errorf("failed to send command to agent %s: %w", targetNodeID, err)
	}
	d.markDispatched(ctx, traceID)

	requestTimeout := req.Timeout
	if requestTimeout <= 0 {
		requestTimeout = 30
	}
	waitTimeout := time.Duration(requestTimeout)*time.Second + networkLatencyBuffer
	sendCancel := func(reason string) {
		cancelCommand := &pb.ServerCommand{
			Payload: &pb.ServerCommand_CancelCommand{
				CancelCommand: &pb.CancelCommand{TraceId: traceID},
			},
		}
		if err := d.registry.SendCommand(targetNodeID, cancelCommand); err != nil {
			d.logger.Warn("Failed to send cancel command",
				"reason", reason,
				"target_node_id", targetNodeID,
				"error", err)
		}
	}

	result, err := d.waiter(ctx, resultCh, waitTimeout)
	if err != nil {
		if errors.Is(err, errTimeout) {
			d.logger.Error("Dispatch request timeout",
				"trace_id", traceID,
				"target_node_id", targetNodeID,
				"timeout", waitTimeout)
			sendCancel("timeout")
			d.markTerminal(ctx, traceID, model.ExecutionStatusTimedOut, 0, "timeout waiting for result")
			return nil, fmt.Errorf("timeout waiting for result from agent %s", targetNodeID)
		}
		d.logger.Warn("Dispatch request cancelled/failed, sending kill signal to agent",
			"trace_id", traceID,
			"target_node_id", targetNodeID,
			"error", err)
		sendCancel("context cancelled")
		markFailed(err)
		return nil, err
	}

	dispatchResult, ok := result.(*pb.CommandResult)
	if !ok {
		if result == nil {
			err := fmt.Errorf("received nil result (pending request was cancelled)")
			markFailed(err)
			return nil, err
		}
		err := fmt.Errorf("unexpected result type: %T", result)
		markFailed(err)
		return nil, err
	}

	d.logger.Info("Received dispatched result",
		"trace_id", traceID,
		"success", dispatchResult.Success,
		"exit_code", dispatchResult.ExitCode,
		"duration_ms", dispatchResult.Duration.AsDuration().Milliseconds())

	terminalStatus := model.ExecutionStatusSucceeded
	if !dispatchResult.Success {
		terminalStatus = model.ExecutionStatusFailed
	}
	d.markTerminal(ctx, traceID, terminalStatus, int(dispatchResult.ExitCode), dispatchResult.SystemError)

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

func (d *Dispatcher) lookupActionResult(
	ctx context.Context,
	executionID string,
	includeInFlight bool,
) (*execution.ExecuteResponse, error) {
	record, err := d.execs.GetByExecutionID(ctx, executionID)
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
		d.logger.Info("Returning existing dispatch result for execution_id",
			"execution_id", executionID,
			"trace_id", record.TraceID,
			"status", record.Status)
		return &execution.ExecuteResponse{
			ExecutionID: executionID,
			TraceID:     record.TraceID,
			Success:     record.Status == model.ExecutionStatusSucceeded,
			ExitCode:    record.ExitCode,
			SystemError: record.SystemError,
		}, nil
	default:
		return nil, fmt.Errorf("execution_id %s has unknown status: %d", executionID, record.Status)
	}
}
