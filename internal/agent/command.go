package agent

import (
	"fmt"
	"runtime/debug"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
)

const (
	timeDriftThreshold = 30 * time.Second // Acceptable server-client time drift
)

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
