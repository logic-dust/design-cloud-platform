package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"
)

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
