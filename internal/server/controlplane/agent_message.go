package controlplane

import (
	"context"
	"time"

	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/model"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"

	"google.golang.org/protobuf/proto"
)

// 本文件处理已注册 Agent 通过 Sync 流发往 Control 的消息。

func (s *Server) handleHeartbeat(conn *agentregistry.Connection, _ *pb.HeartbeatRequest) {
	if conn == nil {
		return
	}

	now := time.Now()
	conn.Touch(now)

	if conn.ShouldFlushHeartbeat(now, s.heartbeatFlushThreshold()) {
		if err := s.nodes.TouchHeartbeat(conn.StreamContext(), conn.NodeID); err != nil {
			s.logger.Warn("Failed to update node heartbeat in DB", "nodeID", conn.NodeID, "error", err)
		} else {
			conn.MarkHeartbeatFlushed(now)
			s.logger.Debug("Heartbeat flushed to DB", "nodeID", conn.NodeID)
		}
	} else {
		s.logger.Debug("Heartbeat throttled in memory", "nodeID", conn.NodeID)
	}

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

// handleCommandResult 先唤醒同步等待者，再落库并向 Agent 返回 ACK。
func (s *Server) handleCommandResult(conn *agentregistry.Connection, result *pb.CommandResult) {
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

	// 同步派发无 DB 执行记录；须在落库前投递，避免慢库拖过等待超时。
	s.registry.DeliverResult(result.TraceId, result)

	// 使用 server 级超时而非 stream context，避免断流取消写库导致执行记录卡在 running。
	dbCtx, cancel := context.WithTimeout(s.ctx, nodeStatusUpdateTimeout)
	defer cancel()

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
	persisted, err := s.executions.MarkTerminal(
		dbCtx,
		result.TraceId,
		status,
		int(result.ExitCode),
		result.SystemError,
	)
	if err != nil {
		s.logger.Error("Failed to persist command result", "trace_id", result.TraceId, "error", err)
	} else if !persisted {
		s.logger.Debug("Command result not persisted because no async-tracked execution record exists", "trace_id", result.TraceId)
	}

	if err := conn.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_CommandResultAck{
			CommandResultAck: &pb.CommandResultAck{
				TraceId:   result.TraceId,
				Persisted: persisted,
			},
		},
	}); err != nil {
		s.logger.Warn("Failed to send result ACK", "trace_id", result.TraceId, "error", err)
	}
}

func (s *Server) handleMetrics(ctx context.Context, conn *agentregistry.Connection, metrics *pb.Metrics) {
	if conn == nil {
		return
	}

	const flushThreshold = time.Duration(contract.DefaultMetricsFlushInterval) * time.Second
	now := time.Now()

	// registry 独立持有克隆，避免快照侧 proto.Clone 与当前 goroutine 读取原 metrics 竞态。
	stored := proto.Clone(metrics).(*pb.Metrics)
	conn.UpdateFrame(now, stored)

	// 内存快照与告警评估逐帧更新，node_runtime 侧表按最小间隔节流写入。
	if conn.ShouldFlushMetrics(now, flushThreshold) {
		if err := s.nodes.UpsertRuntime(ctx, toNodeRuntime(conn.NodeID, metrics)); err != nil {
			s.logger.Warn("Failed to persist node metrics", "nodeID", conn.NodeID, "error", err)
		} else {
			conn.MarkMetricsFlushed(now)
		}
	}

	if s.deps.AlertEvaluator != nil {
		if err := s.deps.AlertEvaluator.EvalMetrics(ctx, conn.NodeID, metrics); err != nil {
			s.logger.Debug("Alert evaluation skipped for node metrics", "nodeID", conn.NodeID, "error", err)
		}
	}
}

func toNodeRuntime(nodeID string, metrics *pb.Metrics) *model.NodeRuntime {
	gpuCount := int64(len(metrics.GetGpus()))
	var gpuMemTotal, gpuMemUsed int64
	var gpuUtilSum float64
	for _, gpu := range metrics.GetGpus() {
		gpuMemTotal += gpu.GetMemTotal()
		gpuMemUsed += gpu.GetMemUsed()
		gpuUtilSum += gpu.GetUtilization()
	}

	var gpuUtil float64
	if gpuCount > 0 {
		gpuUtil = gpuUtilSum / float64(gpuCount)
	}

	var diskUsages []model.NodeDiskInfo
	if items := metrics.GetDiskUsages(); len(items) > 0 {
		diskUsages = make([]model.NodeDiskInfo, 0, len(items))
		for _, item := range items {
			diskUsages = append(diskUsages, model.NodeDiskInfo{
				MountPoint:  item.GetMountPoint(),
				Filesystem:  item.GetFilesystem(),
				MonitorName: item.GetMonitorName(),
				Total:       item.GetTotal(),
				Used:        item.GetUsed(),
			})
		}
	}

	return &model.NodeRuntime{
		NodeID:      nodeID,
		ReportedAt:  time.Unix(metrics.GetReportedAt(), 0),
		Uptime:      metrics.GetUptime(),
		ActiveTasks: metrics.GetActiveTasks(),
		CPUCores:    metrics.GetCpuCores(),
		CPUUtil:     metrics.GetCpuUtil(),
		MemTotal:    metrics.GetMemTotal(),
		MemUsed:     metrics.GetMemUsed(),
		SwapTotal:   metrics.GetSwapTotal(),
		SwapFree:    metrics.GetSwapFree(),
		Load1:       metrics.GetLoad1(),
		Load5:       metrics.GetLoad5(),
		Load15:      metrics.GetLoad15(),
		GPUCount:    gpuCount,
		GPUMemTotal: gpuMemTotal,
		GPUMemUsed:  gpuMemUsed,
		GPUUtil:     gpuUtil,
		DiskUsages:  diskUsages,
	}
}

func (s *Server) handleConnectionReport(_ context.Context, conn *agentregistry.Connection, report *pb.ConnectionReport) {
	if conn == nil || report == nil {
		return
	}

	vncSeats := make([]contract.VNCSeatInfo, 0, len(report.GetVncSeats()))
	for _, info := range report.GetVncSeats() {
		if info != nil {
			vncSeats = append(vncSeats, toContractVNCSeatInfo(info))
		}
	}
	xrdpSeats := make([]contract.XRDPSeatInfo, 0, len(report.GetXrdpSeats()))
	for _, info := range report.GetXrdpSeats() {
		if info != nil {
			xrdpSeats = append(xrdpSeats, toContractXRDPSeatInfo(info))
		}
	}
	rdpSessions := make([]contract.RDPSessionInfo, 0, len(report.GetRdpSessions()))
	for _, info := range report.GetRdpSessions() {
		if info != nil {
			rdpSessions = append(rdpSessions, contract.RDPSessionInfo{
				Username:    info.GetUsername(),
				SessionID:   int(info.GetSessionId()),
				SessionName: info.GetSessionName(),
			})
		}
	}

	payload := contract.ConnectionReport{
		ReportedAt:  report.GetReportedAt(),
		VNCSeats:    vncSeats,
		XRDPSeats:   xrdpSeats,
		RDPSessions: rdpSessions,
	}

	// Prefer the coalesce worker (off Sync recv). Tests that build a zero-value Server
	// (no reconcile worker) still sync inline.
	if s.connectionReconcile != nil {
		s.connectionReconcile.Enqueue(conn.NodeID, payload)
		return
	}
	if s.deps.ConnectionReportHandler == nil {
		return
	}
	if err := s.deps.ConnectionReportHandler.SyncConnectionReport(s.ctx, conn.NodeID, payload); err != nil {
		s.logger.Error("Failed to sync connection runtime report",
			"nodeID", conn.NodeID,
			"reported_at", report.GetReportedAt(),
			"vnc_seats", len(vncSeats),
			"xrdp_seats", len(xrdpSeats),
			"error", err)
	}
}

func toContractVNCSeatInfo(info *pb.VNCSeatInfo) contract.VNCSeatInfo {
	if info == nil {
		return contract.VNCSeatInfo{}
	}
	out := contract.VNCSeatInfo{
		ConnectionID: uint(info.GetConnectionId()),
		Name:         info.GetName(),
		Username:     info.GetUsername(),
		Port:         uint(info.GetPort()),
		PID:          uint(info.GetPid()),
	}
	if info.Display != nil && *info.Display >= 0 {
		display := int(*info.Display)
		out.Display = &display
	}
	if info.StartedAt != nil {
		out.StartedAt = *info.StartedAt
	}
	return out
}

func toContractXRDPSeatInfo(info *pb.XRDPSeatInfo) contract.XRDPSeatInfo {
	if info == nil {
		return contract.XRDPSeatInfo{}
	}
	out := contract.XRDPSeatInfo{
		ConnectionID:           uint(info.GetConnectionId()),
		Name:                   info.GetName(),
		Username:               info.GetUsername(),
		PID:                    uint(info.GetPid()),
		SessionEndingConfirmed: info.GetSessionEndingConfirmed(),
	}
	if info.Display != nil && *info.Display >= 0 {
		display := int(*info.Display)
		out.Display = &display
	}
	if info.StartedAt != nil {
		out.StartedAt = *info.StartedAt
	}
	return out
}

func (s *Server) handleStorageProbeReport(ctx context.Context, report *pb.StorageProbeReport) {
	if report == nil {
		return
	}

	reportedAt := time.Unix(report.GetReportedAt(), 0)
	for _, item := range report.GetItems() {
		if item == nil {
			continue
		}
		if err := s.storage.InsertStorageReport(ctx, &model.StorageReport{
			StorageID:  item.GetStorageId(),
			Total:      item.GetTotal(),
			Used:       item.GetUsed(),
			ReportedAt: reportedAt,
		}); err != nil {
			s.logger.Error("Failed to persist storage probe report", "storageID", item.GetStorageId(), "error", err)
		}
		if s.deps.AlertEvaluator != nil {
			if err := s.deps.AlertEvaluator.EvalStorage(ctx, item); err != nil {
				s.logger.Debug("Alert evaluation skipped for storage probe report", "storageID", item.GetStorageId(), "error", err)
			}
		}
	}
}
