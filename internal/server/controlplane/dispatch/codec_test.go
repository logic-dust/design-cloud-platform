package dispatch

import (
	"strings"
	"testing"

	"design-cloud-engine/internal/execution"
)

func TestToCommandRequestEncodesPayloadJSONOutsideEnv(t *testing.T) {
	t.Parallel()

	req := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithPayload(map[string]any{"username": "alice"}),
	)
	command, err := toCommandRequest(req)
	if err != nil {
		t.Fatalf("to command request: %v", err)
	}
	if !strings.Contains(command.GetPayloadJson(), `"username":"alice"`) {
		t.Fatalf("payload_json = %q, want encoded payload", command.GetPayloadJson())
	}
	if _, ok := command.GetEnv()["payload_json"]; ok {
		t.Fatalf("env must not contain payload_json: %#v", command.GetEnv())
	}
}

func TestToCommandRequestRejectsStringPayload(t *testing.T) {
	t.Parallel()

	req := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithPayload(`{"username":"alice"}`),
	)
	if _, err := toCommandRequest(req); err == nil {
		t.Fatalf("expected string payload error")
	}
}
