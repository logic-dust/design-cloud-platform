package dispatch

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"strings"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"

	"google.golang.org/protobuf/types/known/durationpb"
)

func toCommandRequest(req *execution.ExecuteRequest) (*pb.CommandRequest, error) {
	if err := validateExecuteRequest(req); err != nil {
		return nil, err
	}
	payloadJSON, err := marshalPayload(req.Payload)
	if err != nil {
		return nil, err
	}
	return &pb.CommandRequest{
		TraceId:     req.TraceID,
		ExecutionId: req.ExecutionID,
		TargetUser:  req.TargetUser,
		ActionName:  string(req.ActionName),
		PayloadJson: payloadJSON,
		Env:         req.Env,
		Timeout:     durationpb.New(time.Duration(req.Timeout) * time.Second),
	}, nil
}

func ToForwardRequest(sourceNodeID string, leaderEpoch uint64, req *execution.ExecuteRequest) (*pb.ForwardDispatchRequest, error) {
	if sourceNodeID == "" {
		return nil, fmt.Errorf("source_node_id is required")
	}
	if err := validateExecuteRequest(req); err != nil {
		return nil, err
	}
	payloadJSON, err := marshalPayload(req.Payload)
	if err != nil {
		return nil, err
	}
	if req.NodeAffinity != execution.NodeAffinityLocalPreferred && req.NodeAffinity != execution.NodeAffinityAny {
		return nil, fmt.Errorf("invalid node_affinity: %d", req.NodeAffinity)
	}
	if req.Mode != execution.RunModeSync && req.Mode != execution.RunModeAsyncTracked {
		return nil, fmt.Errorf("invalid run_mode: %d", req.Mode)
	}
	return &pb.ForwardDispatchRequest{
		Request: &pb.CommandRequest{
			TraceId:     req.TraceID,
			ExecutionId: req.ExecutionID,
			ActionName:  string(req.ActionName),
			TargetUser:  req.TargetUser,
			PayloadJson: payloadJSON,
			Env:         req.Env,
			Timeout:     durationpb.New(time.Duration(req.Timeout) * time.Second),
		},
		SourceNodeId: sourceNodeID,
		TargetNode:   req.TargetNode,
		NodeSelector: req.NodeSelector,
		NodeAffinity: pb.NodeAffinity(req.NodeAffinity),
		RunMode:      pb.RunMode(req.Mode),
		LeaderEpoch:  leaderEpoch,
		// Hop 固定为 0：拓扑只有 Standby → Leader 一跳，0 是唯一合法入站值。
		// 服务端拒绝 hop >= 1，用来挡住误加的二次转发。
		Hop: 0,
	}, nil
}

func FromForwardRequest(req *pb.ForwardDispatchRequest) (*execution.ExecuteRequest, error) {
	if err := validateForwardDispatch(req); err != nil {
		return nil, err
	}
	payload, err := decodePayload(req.Request.PayloadJson)
	if err != nil {
		return nil, err
	}
	return &execution.ExecuteRequest{
		TraceID:      req.Request.TraceId,
		ExecutionID:  req.Request.ExecutionId,
		ActionName:   execution.ActionName(req.Request.ActionName),
		TargetUser:   req.Request.TargetUser,
		TargetNode:   req.TargetNode,
		NodeSelector: req.NodeSelector,
		NodeAffinity: execution.NodeAffinity(req.NodeAffinity),
		Mode:         execution.RunMode(req.RunMode),
		Payload:      payload,
		Env:          req.Request.Env,
		Timeout:      int(req.Request.Timeout.AsDuration().Seconds()),
	}, nil
}

func ToForwardResponse(resp *execution.ExecuteResponse) *pb.ForwardDispatchResponse {
	if resp == nil {
		return nil
	}
	return &pb.ForwardDispatchResponse{
		ExecutionId: resp.ExecutionID,
		Accepted:    resp.Accepted,
		Result: &pb.CommandResult{
			TraceId:     resp.TraceID,
			Success:     resp.Success,
			ExitCode:    int32(resp.ExitCode),
			Stdout:      resp.Stdout,
			Stderr:      resp.Stderr,
			SystemError: resp.SystemError,
			Duration:    durationpb.New(time.Duration(resp.Duration) * time.Millisecond),
		},
	}
}

func FromForwardResponse(resp *pb.ForwardDispatchResponse) *execution.ExecuteResponse {
	if resp == nil {
		return nil
	}
	var traceID, stdout, stderr, systemError string
	var success bool
	var exitCode int
	var durationMs int64
	if resp.Result != nil {
		traceID = resp.Result.TraceId
		success = resp.Result.Success
		exitCode = int(resp.Result.ExitCode)
		stdout = resp.Result.Stdout
		stderr = resp.Result.Stderr
		systemError = resp.Result.SystemError
		if resp.Result.Duration != nil {
			durationMs = resp.Result.Duration.AsDuration().Milliseconds()
		}
	}
	return &execution.ExecuteResponse{
		ExecutionID: resp.ExecutionId,
		TraceID:     traceID,
		Accepted:    resp.Accepted,
		Success:     success,
		ExitCode:    exitCode,
		Stdout:      stdout,
		Stderr:      stderr,
		SystemError: systemError,
		Duration:    durationMs,
	}
}

func validateExecuteRequest(req *execution.ExecuteRequest) error {
	if req == nil {
		return fmt.Errorf("execution request is nil")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	if req.ExecutionID == "" {
		return fmt.Errorf("execution_id is required")
	}
	return nil
}

func validateForwardDispatch(req *pb.ForwardDispatchRequest) error {
	if req == nil || req.Request == nil {
		return fmt.Errorf("forward dispatch request or request is nil")
	}
	if req.SourceNodeId == "" {
		return fmt.Errorf("source_node_id is required")
	}
	if req.Request.TraceId == "" {
		return fmt.Errorf("trace_id is required")
	}
	if req.Request.ExecutionId == "" {
		return fmt.Errorf("execution_id is required")
	}
	if req.Request.ActionName == "" {
		return fmt.Errorf("action_name is required")
	}
	if req.Request.Timeout == nil {
		return fmt.Errorf("timeout is required")
	}
	if req.Request.Timeout.AsDuration() < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	switch req.NodeAffinity {
	case pb.NodeAffinity_NODE_AFFINITY_LOCAL_PREFERRED, pb.NodeAffinity_NODE_AFFINITY_ANY:
	default:
		return fmt.Errorf("invalid node_affinity: %d", req.NodeAffinity)
	}
	switch req.RunMode {
	case pb.RunMode_RUN_MODE_SYNC, pb.RunMode_RUN_MODE_ASYNC_TRACKED:
	default:
		return fmt.Errorf("invalid run_mode: %d", req.RunMode)
	}
	return nil
}

func marshalPayload(payload any) (string, error) {
	if payload == nil {
		return "", nil
	}
	switch value := payload.(type) {
	case json.RawMessage:
		return string(value), nil
	case string:
		return "", fmt.Errorf("string payload is not supported")
	default:
		data, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal payload: %w", err)
		}
		return string(data), nil
	}
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
