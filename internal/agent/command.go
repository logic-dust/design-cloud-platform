package agent

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"

	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	timeDriftThreshold = 30 * time.Second // Acceptable server-client time drift

	// Pending command results
	pendingCommandResultTTL  = 30 * time.Minute // Discard unacked results older than this
	maxPendingCommandResults = 1024             // Max unacked results held before capping
)

// pendingCommandResult holds an unacknowledged command result together with
// the time it was stored, so that stale entries can be evicted by TTL.
type pendingCommandResult struct {
	result   *pb.CommandResult
	storedAt time.Time
}

// commandStreamLoop receives server commands from the Sync stream and dispatches them.
func (a *Agent) commandStreamLoop(stream pb.ControlService_SyncClient) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("stream recv error: %w", err)
		}

		switch payload := msg.Payload.(type) {
		case *pb.ServerCommand_CommandRequest:
			a.executeWg.Go(func() {
				defer func() {
					if r := recover(); r != nil {
						req := payload.CommandRequest
						traceID := ""
						execID := ""
						if req != nil {
							traceID = req.TraceId
							execID = req.ExecutionId
						}
						a.logger.Error("panic recovered during command execution",
							"trace_id", traceID,
							"execution_id", execID,
							"panic", r,
							"stack", string(debug.Stack()),
						)
						if traceID != "" {
							a.sendCommandResult(stream, &pb.CommandResult{
								TraceId:     traceID,
								Success:     false,
								SystemError: fmt.Sprintf("panic: %v", r),
							})
						}
					}
				}()
				a.handleCommandRequest(stream, payload.CommandRequest)
			})

		case *pb.ServerCommand_CancelCommand:
			a.handleCancelCommand(payload.CancelCommand)

		case *pb.ServerCommand_HeartbeatResponse:
			a.handleHeartbeatAck(payload.HeartbeatResponse)

		case *pb.ServerCommand_CommandResultAck:
			a.handleResultAck(payload.CommandResultAck)

		case *pb.ServerCommand_CollectProbeCommand:
			if payload.CollectProbeCommand != nil {
				a.handleProbeCollectCommand(
					payload.CollectProbeCommand.GetProbeName(),
					payload.CollectProbeCommand.GetTraceId(),
					payload.CollectProbeCommand.GetRequestedAt(),
				)
			}

		case *pb.ServerCommand_UpdateSettingsCommand:
			if a.settingsMgr != nil && payload.UpdateSettingsCommand.AgentSettings != nil {
				a.settingsMgr.Update(payload.UpdateSettingsCommand.AgentSettings)
				a.logger.Info("Updated agent settings from server command")
			}

		case *pb.ServerCommand_RedirectCommand:
			return a.handleRedirect(payload.RedirectCommand)

		case *pb.ServerCommand_RenewCertCommand:
			a.handleRenewCertCommand(stream, payload.RenewCertCommand)

		case *pb.ServerCommand_RenewCertResponse:
			return a.handleRenewCertResponse(stream, payload.RenewCertResponse)
		}
	}
}

func (a *Agent) handleRedirect(cmd *pb.RedirectCommand) error {
	if cmd == nil {
		return ErrServerShutdown
	}
	leaderAddr := leaderRedirectAddr(a.cfg.ClusterPort, cmd.GetLeaderNodeId(), cmd.GetLeaderAddr())
	a.logger.Warn("Received redirect command from server",
		"leader_id", cmd.GetLeaderNodeId(),
		"leader_addr", leaderAddr,
		"reason", cmd.GetReason())
	return &redirectError{
		leaderID:   cmd.GetLeaderNodeId(),
		leaderAddr: leaderAddr,
		reason:     cmd.GetReason(),
	}
}

// handleCancelCommand processes a cancel command from the server
func (a *Agent) handleCancelCommand(cmd *pb.CancelCommand) {
	a.logger.Warn("Received cancel command from server", "trace_id", cmd.TraceId)
	a.cancelTask(cmd.TraceId)
}

func (a *Agent) handleProbeCollectCommand(probeName string, traceID string, requestedAt int64) {
	a.logger.Info("Received probe collect command", "probe", probeName, "trace_id", traceID, "requested_at", requestedAt)
	if a.telemetry != nil {
		a.telemetry.TriggerNow(probeName)
	}
}

// handleResultAck processes result acknowledgment from the server
func (a *Agent) handleResultAck(ack *pb.CommandResultAck) {
	if ack == nil {
		return
	}
	a.removePendingResult(ack.TraceId)
	a.logger.Debug("Result acknowledged by server", "trace_id", ack.TraceId, "persisted", ack.Persisted)
}

// handleHeartbeatAck processes heartbeat acknowledgment from the server
// Detects time drift between Agent and Server (warns if > 30 seconds)
func (a *Agent) handleHeartbeatAck(ack *pb.HeartbeatResponse) {
	if ack == nil {
		return
	}

	serverTime := time.Unix(ack.ServerTimestamp, 0)
	localTime := time.Now()
	drift := localTime.Sub(serverTime)

	// 取绝对值
	if drift < 0 {
		drift = -drift
	}

	if drift > timeDriftThreshold {
		a.logger.Warn("Significant time drift detected between Agent and Server",
			"server_time", serverTime.Format(time.RFC3339),
			"local_time", localTime.Format(time.RFC3339),
			"drift", drift.Round(time.Second).String())
	} else {
		a.logger.Debug("Heartbeat acknowledged",
			"server_timestamp", ack.ServerTimestamp,
			"drift", drift.Round(time.Millisecond).String())
	}
}

// handleCommandRequest processes a command request from the server
func (a *Agent) handleCommandRequest(stream pb.ControlService_SyncClient, cmd *pb.CommandRequest) {
	a.logger.Debug("Received command request",
		"trace_id", cmd.TraceId,
		"execution_id", cmd.ExecutionId,
		"action_name", cmd.ActionName,
		"target_user", cmd.TargetUser)

	startTime := time.Now()

	// Use agent-level context as parent to ensure task cleanup on agent stop
	parentCtx := a.ctx
	var ctx context.Context
	var cancel context.CancelFunc

	if cmd.Timeout != nil && cmd.Timeout.AsDuration() > 0 {
		ctx, cancel = context.WithTimeout(parentCtx, cmd.Timeout.AsDuration())
	} else {
		ctx, cancel = context.WithCancel(parentCtx)
	}
	defer cancel()

	// Register task for cancellation
	a.registerTask(cmd.TraceId, cancel)
	defer a.unregisterTask(cmd.TraceId)

	// Execute the command via runner.Service
	result := &pb.CommandResult{
		TraceId: cmd.TraceId,
	}

	if a.executorService != nil {
		execReq, err := fromCommandRequest(cmd)
		if err != nil {
			a.logger.Error("Failed to decode command request", "trace_id", cmd.TraceId, "execution_id", cmd.ExecutionId, "error", err)
			result.Success = false
			result.SystemError = err.Error()
			result.Duration = durationpb.New(time.Since(startTime))
			a.sendCommandResult(stream, result)
			return
		}
		resp, err := a.executorService.Execute(ctx, execReq)

		if err != nil {
			a.logger.Error("Failed to execute command", "trace_id", cmd.TraceId, "execution_id", cmd.ExecutionId, "error", err)
			result.Success = false
			result.SystemError = err.Error()
		} else {
			result.Success = resp.Success && resp.ExitCode == 0
			result.ExitCode = int32(resp.ExitCode)
			result.Stdout = resp.Stdout
			result.Stderr = resp.Stderr
			result.SystemError = resp.SystemError

			// Log error if execution failed
			if !result.Success {
				a.logger.Error("Command execution failed", "trace_id", cmd.TraceId, "execution_id", cmd.ExecutionId, "exit_code", resp.ExitCode, "system_error", resp.SystemError)
			}
		}
	} else {
		result.Success = false
		result.SystemError = "executor service not available"
	}

	result.Duration = durationpb.New(time.Since(startTime))

	a.sendCommandResult(stream, result)
}

func (a *Agent) sendCommandResult(stream pb.ControlService_SyncClient, result *pb.CommandResult) {
	if result == nil {
		return
	}
	a.storePendingResult(result)

	err := a.sendMessage(stream, &pb.AgentMessage{
		Payload: &pb.AgentMessage_CommandResult{
			CommandResult: result,
		},
	})

	if err != nil {
		a.logger.Error("Failed to send command result; queued for replay", "trace_id", result.TraceId, "error", err)
		return
	}
	a.logger.Debug("Command result sent", "trace_id", result.TraceId)
}

func (a *Agent) storePendingResult(result *pb.CommandResult) {
	if result == nil || result.TraceId == "" {
		return
	}
	now := time.Now()
	a.pendingResultsMu.Lock()
	defer a.pendingResultsMu.Unlock()
	if a.pendingResults == nil {
		a.pendingResults = make(map[string]pendingCommandResult)
	}
	a.prunePendingResultsLocked(now)
	if _, exists := a.pendingResults[result.TraceId]; !exists {
		for len(a.pendingResults) >= maxPendingCommandResults {
			droppedID, ok := a.dropOldestPendingResultLocked()
			if !ok {
				break
			}
			a.logger.Warn("pending command results cap reached; discarding oldest result",
				"dropped_trace_id", droppedID,
				"new_trace_id", result.TraceId,
			)
		}
	}
	a.pendingResults[result.TraceId] = pendingCommandResult{result: result, storedAt: now}
}

func (a *Agent) removePendingResult(traceID string) {
	if traceID == "" {
		return
	}
	a.pendingResultsMu.Lock()
	defer a.pendingResultsMu.Unlock()
	delete(a.pendingResults, traceID)
}

func (a *Agent) pendingResultSnapshot() []*pb.CommandResult {
	a.pendingResultsMu.Lock()
	defer a.pendingResultsMu.Unlock()
	a.prunePendingResultsLocked(time.Now())
	results := make([]*pb.CommandResult, 0, len(a.pendingResults))
	for _, entry := range a.pendingResults {
		results = append(results, entry.result)
	}
	return results
}

// prunePendingResultsLocked removes unacknowledged results that have exceeded
// pendingCommandResultTTL. Must be called with pendingResultsMu held.
func (a *Agent) prunePendingResultsLocked(now time.Time) {
	for id, entry := range a.pendingResults {
		if now.Sub(entry.storedAt) > pendingCommandResultTTL {
			a.logger.Warn("discarding unacked command result (TTL exceeded)", "trace_id", id)
			delete(a.pendingResults, id)
		}
	}
}

func (a *Agent) dropOldestPendingResultLocked() (string, bool) {
	var oldestID string
	var oldestAt time.Time
	for id, entry := range a.pendingResults {
		if oldestID == "" || entry.storedAt.Before(oldestAt) {
			oldestID = id
			oldestAt = entry.storedAt
		}
	}
	if oldestID == "" {
		return "", false
	}
	delete(a.pendingResults, oldestID)
	return oldestID, true
}

func (a *Agent) replayPendingResults(stream pb.ControlService_SyncClient) {
	for _, result := range a.pendingResultSnapshot() {
		err := a.sendMessage(stream, &pb.AgentMessage{
			Payload: &pb.AgentMessage_CommandResult{
				CommandResult: result,
			},
		})
		if err != nil {
			a.logger.Warn("Failed to replay pending command result", "trace_id", result.TraceId, "error", err)
			return
		}
		a.logger.Info("Replayed pending command result", "trace_id", result.TraceId)
	}
}

func fromCommandRequest(cmd *pb.CommandRequest) (*execution.ExecuteRequest, error) {
	if err := validateCommandRequest(cmd); err != nil {
		return nil, err
	}

	payload, err := decodePayload(cmd.PayloadJson)
	if err != nil {
		return nil, err
	}

	return &execution.ExecuteRequest{
		TraceID:     cmd.TraceId,
		ExecutionID: cmd.ExecutionId,
		ActionName:  execution.ActionName(cmd.ActionName),
		TargetUser:  cmd.TargetUser,
		Payload:     payload,
		Env:         cmd.Env,
		Timeout:     int(cmd.Timeout.AsDuration().Seconds()),
	}, nil
}

func validateCommandRequest(cmd *pb.CommandRequest) error {
	if cmd == nil {
		return fmt.Errorf("command request is nil")
	}
	if cmd.TraceId == "" {
		return fmt.Errorf("trace_id is required")
	}
	if cmd.ExecutionId == "" {
		return fmt.Errorf("execution_id is required")
	}
	if cmd.ActionName == "" {
		return fmt.Errorf("action_name is required")
	}
	if cmd.Timeout == nil {
		return fmt.Errorf("timeout is required")
	}
	if cmd.Timeout.AsDuration() < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	return nil
}

func decodePayload(payloadJSON string) (any, error) {
	trimmed := strings.TrimSpace(payloadJSON)
	if trimmed == "" {
		return nil, nil
	}

	if !strings.HasPrefix(trimmed, "{") {
		return nil, fmt.Errorf("payload_json must be a JSON object")
	}

	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("payload_json must be valid JSON: %s", trimmed)
	}

	return json.RawMessage(trimmed), nil
}
