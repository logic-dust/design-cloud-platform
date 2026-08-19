package agent

import (
	"context"
	"time"

	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/protobuf/types/known/durationpb"
)

// pendingCommandResult holds an unacknowledged command result together with
// the time it was stored, so that stale entries can be evicted by TTL.
type pendingCommandResult struct {
	result   *pb.CommandResult
	storedAt time.Time
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

const (
	// Pending command results
	pendingCommandResultTTL  = 30 * time.Minute // Discard unacked results older than this
	maxPendingCommandResults = 1024             // Max unacked results held before capping
)

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
